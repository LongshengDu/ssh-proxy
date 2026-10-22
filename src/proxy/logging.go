package proxy

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"
)

const (
	defaultMaxLogFiles = 10
	defaultMaxLogMB    = 10
	maxLogMB           = 1024
)

const truncatedMarker = "--- Earlier lines removed: log reached logging.max_size_mb ---\n"

// Match only application run logs, including the original untimestamped names.
var runLogName = regexp.MustCompile(`^(logs|tray)_[0-9a-f]{16}(?:_[0-9]{8}_[0-9]{6}\.[0-9]{9}_[0-9]+)?\.txt$`)

// RunLog appends one run's output to one file. A run is never split into
// several files; when the file would exceed its size limit, the oldest half is
// removed in place and a marker line notes the cut.
type RunLog struct {
	*os.File
	mu    sync.Mutex
	limit int64
}

// NewLog appends for the lifetime of one process, using the default size limit.
func NewLog(path string, foreground bool) (*log.Logger, *RunLog, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, nil, err
	}
	writer := &RunLog{File: file, limit: defaultMaxLogMB << 20}
	var output io.Writer = writer
	if foreground {
		output = io.MultiWriter(writer, os.Stderr)
	}
	return log.New(output, "", log.Ldate|log.Ltime|log.Lmicroseconds), writer, nil
}

// SetLimit applies logging.max_size_mb once the configuration is known.
func (l *RunLog) SetLimit(megabytes int) {
	l.mu.Lock()
	l.limit = int64(megabytes) << 20
	l.mu.Unlock()
}

func (l *RunLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if info, err := l.File.Stat(); err == nil && info.Size()+int64(len(p)) > l.limit {
		// A failed cut must not lose the message; the next write retries.
		_ = l.truncate(info.Size())
	}
	return l.File.Write(p)
}

// truncate keeps the newest whole lines from the last half of the limit.
// It uses a second handle because append-only handles cannot truncate on
// every OS. Other appenders (child output, native ssh -E) continue at the new end.
func (l *RunLog) truncate(size int64) error {
	file, err := os.OpenFile(l.Name(), os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	keep := min(size, l.limit/2)
	tail := make([]byte, keep)
	n, err := file.ReadAt(tail, size-keep)
	if err != nil && err != io.EOF {
		return err
	}
	tail = tail[:n]
	if i := bytes.IndexByte(tail, '\n'); i >= 0 && keep < size {
		tail = tail[i+1:]
	}
	if err := file.Truncate(0); err != nil {
		return err
	}
	_, err = file.WriteAt(append([]byte(truncatedMarker), tail...), 0)
	return err
}

// NewRunLog allocates exactly one file; all later messages use its open writer.
func NewRunLog(p Paths, kind string, foreground bool) (*log.Logger, *RunLog, error) {
	if kind != "logs" && kind != "tray" {
		return nil, nil, fmt.Errorf("unknown run log kind %q", kind)
	}
	if err := os.MkdirAll(p.Logs, 0700); err != nil {
		return nil, nil, err
	}
	f, err := os.CreateTemp(p.Logs, kind+"_"+p.ID+"_"+time.Now().UTC().Format("20060102_150405.000000000")+"_*.txt")
	if err != nil {
		return nil, nil, err
	}
	if err := f.Close(); err != nil {
		return nil, nil, err
	}
	logger, writer, err := NewLog(f.Name(), foreground)
	if err != nil {
		return nil, nil, err
	}
	// Logging must work before SSH/config validation, including malformed TOML.
	var c Config
	c.Logging.MaxLogFiles, c.Logging.MaxSizeMB = defaultMaxLogFiles, defaultMaxLogMB
	if _, err := toml.DecodeFile(p.Config, &c); err != nil {
		c.Logging.MaxLogFiles, c.Logging.MaxSizeMB = defaultMaxLogFiles, defaultMaxLogMB
	}
	if c.Logging.MaxLogFiles < 1 {
		c.Logging.MaxLogFiles = defaultMaxLogFiles
	}
	if c.Logging.MaxSizeMB >= 1 && c.Logging.MaxSizeMB <= maxLogMB {
		writer.SetLimit(c.Logging.MaxSizeMB)
	}
	if err := pruneRunLogs(p.Logs, kind, filepath.Base(writer.Name()), c.Logging.MaxLogFiles); err != nil {
		// Retention failure must not take down a working proxy.
		logger.Printf("Cannot remove old %s logs: %v", kind, err)
	}
	return logger, writer, nil
}

func pruneRunLogs(directory, kind, current string, limit int) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	var old []os.FileInfo
	for _, entry := range entries {
		if !entry.Type().IsRegular() || entry.Name() == current || !strings.HasPrefix(entry.Name(), kind+"_") || !runLogName.MatchString(entry.Name()) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		old = append(old, info)
	}
	sort.Slice(old, func(i, j int) bool {
		if old[i].ModTime().Equal(old[j].ModTime()) {
			return old[i].Name() < old[j].Name()
		}
		return old[i].ModTime().Before(old[j].ModTime())
	})
	// Reserve one slot for this run; the two kinds have independent limits.
	for i := 0; i < len(old)-limit+1; i++ {
		if err := os.Remove(filepath.Join(directory, old[i].Name())); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func latestLog(directory, id string) string {
	// Timestamped names sort by creation time. Keep old installations readable.
	files, _ := os.ReadDir(directory)
	for i := len(files) - 1; i >= 0; i-- {
		name := files[i].Name()
		if !files[i].IsDir() && strings.HasPrefix(name, "logs_"+id+"_") && strings.HasSuffix(name, ".txt") {
			return filepath.Join(directory, name)
		}
	}
	return filepath.Join(directory, "logs_"+id+".txt")
}

func showLogs(ctx context.Context, path string, lines int, follow bool) error {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		_, err := fmt.Printf("No log file yet: %s\n", path)
		return err
	}
	if err != nil {
		return err
	}
	lastInfo, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	offset := lastInfo.Size()
	tail, err := lastLines(f, offset, lines)
	f.Close()
	if err != nil {
		return err
	}
	for _, line := range tail {
		fmt.Println(line)
	}
	if !follow {
		return nil
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			info, err := os.Stat(path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return err
			}
			if !os.SameFile(lastInfo, info) {
				offset = 0 // Replaced file: show it from the start.
			} else if info.Size() < offset {
				// The size limit removed older lines. Skip the kept copy instead of
				// printing it again; lines written since the last poll may be skipped too.
				fmt.Println(strings.TrimSuffix(truncatedMarker, "\n"))
				offset = info.Size()
			}
			lastInfo = info
			if info.Size() > offset {
				f, err := os.Open(path)
				if err != nil {
					continue
				}
				_, err = f.Seek(offset, io.SeekStart)
				if err == nil {
					n, copyErr := io.Copy(os.Stdout, f)
					offset += n
					err = copyErr
				}
				f.Close()
				if err != nil {
					return err
				}
			}
		}
	}
}

// lastLines reads backward from size until it has n whole lines, so a large
// log is not loaded to show its end.
func lastLines(f *os.File, size int64, n int) ([]string, error) {
	if size == 0 {
		return nil, nil
	}
	for block := int64(64 << 10); ; block *= 4 {
		start := max(0, size-block)
		data := make([]byte, size-start)
		if _, err := f.ReadAt(data, start); err != nil && err != io.EOF {
			return nil, err
		}
		lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
		if start > 0 {
			lines = lines[1:] // The first line may be partial.
		}
		if len(lines) >= n || start == 0 {
			lines = lines[max(0, len(lines)-n):]
			for i := range lines {
				lines[i] = strings.TrimSuffix(lines[i], "\r")
			}
			return lines, nil
		}
	}
}

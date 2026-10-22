package proxy

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

const defaultMaxLogFiles = 10

// Match only application run logs, including the original untimestamped names.
var runLogName = regexp.MustCompile(`^(logs|tray)_[0-9a-f]{16}(?:_[0-9]{8}_[0-9]{6}\.[0-9]{9}_[0-9]+)?\.txt$`)

// NewLog appends for the lifetime of one process. A run is never split or rotated.
func NewLog(path string, foreground bool) (*log.Logger, *os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, nil, err
	}
	writer, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return nil, nil, err
	}
	var output io.Writer = writer
	if foreground {
		output = io.MultiWriter(writer, os.Stderr)
	}
	return log.New(output, "", log.Ldate|log.Ltime|log.Lmicroseconds), writer, nil
}

// NewRunLog allocates exactly one file; all later messages use its open writer.
func NewRunLog(p Paths, kind string, foreground bool) (*log.Logger, *os.File, error) {
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
	c.Logging.MaxLogFiles = defaultMaxLogFiles
	if _, err := toml.DecodeFile(p.Config, &c); err != nil || c.Logging.MaxLogFiles < 1 {
		c.Logging.MaxLogFiles = defaultMaxLogFiles
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
	var tail []string
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 4096), 1024*1024)
	for scan.Scan() {
		tail = append(tail, scan.Text())
		if len(tail) > lines {
			tail = tail[1:]
		}
	}
	if err = scan.Err(); err != nil {
		f.Close()
		return err
	}
	for _, line := range tail {
		fmt.Println(line)
	}
	offset, _ := f.Seek(0, io.SeekCurrent)
	lastInfo, _ := f.Stat()
	f.Close()
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
			if info.Size() < offset || lastInfo == nil || !os.SameFile(lastInfo, info) {
				offset = 0
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

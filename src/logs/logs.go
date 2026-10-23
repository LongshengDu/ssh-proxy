// Package logs writes one continuous file per tray or proxy run, with retention and a size limit.
package logs

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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"

	"ssh-proxy/src/config"
	"ssh-proxy/src/platform"
)

// The byte number is cumulative across cuts, excluding marker lines. Followers
// can recover their position even when several cuts happen between polls.
const truncatedMarker = "--- Earlier lines removed: log reached logging.max_size_mb; continuing from byte %d of this run ---\n"

var truncatedLine = regexp.MustCompile(`^--- Earlier lines removed: log reached logging\.max_size_mb; continuing from byte (\d+) of this run ---$`)

// Match only application run logs, including the original untimestamped names.
var runLogName = regexp.MustCompile(`^(logs|tray)_[0-9a-f]{16}(?:_[0-9]{8}_[0-9]{6}\.[0-9]{9}_[0-9]+)?\.txt$`)

// RunLog appends one run's output to one file. A run is never split into
// several files; when the file would exceed its size limit, the oldest half is
// removed in place and a marker line notes the cut.
//
// Only the process that owns the log truncates it. Writes from other processes
// (another RunLog with no limit, native ssh -E) would be lost if they landed
// during a cut, so the owner holds truncation off while they run.
type RunLog struct {
	*os.File
	mu      sync.Mutex
	limit   int64 // 0: never truncate, for appending to a log another process owns.
	holds   int
	release func()
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
	release, err := platform.ProtectLog(path)
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	writer := &RunLog{File: file, limit: config.DefaultMaxLogMB << 20, release: release}
	var output io.Writer = writer
	if foreground {
		output = io.MultiWriter(writer, os.Stderr)
	}
	return log.New(output, "", log.Ldate|log.Ltime|log.Lmicroseconds), writer, nil
}

func (l *RunLog) Close() error {
	err := l.File.Close()
	l.release()
	return err
}

// SetLimit applies logging.max_size_mb once the configuration is known. Zero
// turns truncation off.
func (l *RunLog) SetLimit(megabytes int) {
	l.mu.Lock()
	l.limit = int64(megabytes) << 20
	l.mu.Unlock()
}

// Hold defers truncation while another process appends to this file, such as
// interactive trust with native ssh -E. Writes continue; the first write after
// the last release applies the limit.
func (l *RunLog) Hold() (release func()) {
	l.mu.Lock()
	l.holds++
	l.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			l.holds--
			l.mu.Unlock()
		})
	}
}

func (l *RunLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.limit == 0 || l.holds > 0 {
		return l.File.Write(p)
	}
	n, err := l.File.Write(p)
	if info, statErr := l.File.Stat(); statErr == nil && info.Size() > l.limit {
		// A failed cut must not lose the message; the next write retries.
		_ = l.truncate(info.Size())
	}
	return n, err
}

// truncate keeps the newest whole lines from the last half of the limit.
// It uses a second handle because append-only handles cannot truncate on
// every OS.
func (l *RunLog) truncate(size int64) error {
	file, err := os.OpenFile(l.Name(), os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	head, _ := firstLine(file)
	keep := min(size, l.limit/2)
	tail := make([]byte, keep)
	n, err := file.ReadAt(tail, size-keep)
	if err != nil && err != io.EOF {
		return err
	}
	tail, start := tail[:n], size-keep
	if i := bytes.IndexByte(tail, '\n'); i >= 0 && keep < size {
		tail, start = tail[i+1:], start+int64(i+1)
	}
	if err := file.Truncate(0); err != nil {
		return err
	}
	_, err = file.WriteAt(append([]byte(fmt.Sprintf(truncatedMarker, logOffset(head)+start)), tail...), 0)
	return err
}

// NewRunLog allocates exactly one file; all later messages use its open writer.
func NewRunLog(p config.Paths, kind string, foreground bool) (*log.Logger, *RunLog, error) {
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
	var c config.Config
	c.Logging.MaxLogFiles, c.Logging.MaxSizeMB = config.DefaultMaxLogFiles, config.DefaultMaxLogMB
	if _, err := toml.DecodeFile(p.Config, &c); err != nil {
		c.Logging.MaxLogFiles, c.Logging.MaxSizeMB = config.DefaultMaxLogFiles, config.DefaultMaxLogMB
	}
	if c.Logging.MaxLogFiles < 1 {
		c.Logging.MaxLogFiles = config.DefaultMaxLogFiles
	}
	if c.Logging.MaxSizeMB >= 1 && c.Logging.MaxSizeMB <= config.MaxLogMB {
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
	remaining := len(old) + 1
	for _, info := range old {
		if remaining <= limit {
			break
		}
		removed, err := platform.RemoveInactiveLog(filepath.Join(directory, info.Name()))
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if removed || os.IsNotExist(err) {
			remaining--
		}
	}
	return nil
}

// Show prints the last lines of a run log and optionally follows it.
func Show(ctx context.Context, path string, lines int, follow bool) error {
	var cursor logCursor
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		data, err := cursor.read(path, lines)
		if os.IsNotExist(err) {
			if cursor.info == nil {
				_, err = fmt.Printf("No log file yet: %s\n", path)
				return err
			}
		} else if err != nil {
			return err
		}
		if _, err := os.Stdout.Write(data); err != nil {
			return err
		}
		if !follow && cursor.info != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// logOffset maps a physical file position to its position in the original run.
func logOffset(head string) int64 {
	match := truncatedLine.FindStringSubmatch(head)
	if match == nil {
		return 0
	}
	start, err := strconv.ParseInt(match[1], 10, 64)
	if err != nil {
		return 0
	}
	return start - int64(len(head)+1)
}

type logCursor struct {
	info   os.FileInfo
	head   string
	offset int64
}

// Read a bounded snapshot before printing anything. An in-place cut during the
// read changes the cumulative marker; discard that snapshot and retry next poll.
func (c *logCursor) read(path string, lines int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	head, complete := firstLine(f)
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !complete && c.info != nil && info.Size() < c.offset {
		return nil, nil // Between truncate and rewrite; keep the old cursor.
	}
	offset, missed := int64(0), false
	if c.info != nil && os.SameFile(c.info, info) {
		offset = c.offset
		if head != c.head && truncatedLine.MatchString(head) {
			offset += logOffset(c.head) - logOffset(head)
			body := int64(len(head) + 1)
			missed = offset < body
			offset = max(body, offset)
		} else if info.Size() < offset {
			offset = 0 // External truncation without our marker.
		}
	}
	var data []byte
	if c.info == nil {
		var tail []string
		tail, err = lastLines(f, info.Size(), lines)
		if len(tail) > 0 {
			data = []byte(strings.Join(tail, "\n") + "\n")
		}
	} else {
		data, err = io.ReadAll(io.NewSectionReader(f, offset, max(0, info.Size()-offset)))
		if int64(len(data)) != max(0, info.Size()-offset) {
			return nil, nil // A cut shortened the file while it was read.
		}
	}
	after, _ := firstLine(f)
	if after != head || err == io.EOF {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	*c = logCursor{info: info, head: head, offset: info.Size()}
	if missed {
		data = append([]byte("--- Lines were removed by logging.max_size_mb before they could be shown ---\n"), data...)
	}
	return data, nil
}

// firstLine uses the same handle as the snapshot, including after replacement.
func firstLine(file *os.File) (string, bool) {
	data := make([]byte, 512)
	n, _ := file.ReadAt(data, 0)
	line, _, complete := strings.Cut(string(data[:n]), "\n")
	return line, complete
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
		if _, err := f.ReadAt(data, start); err != nil {
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

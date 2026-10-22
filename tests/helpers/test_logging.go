package testutil

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// SeedRunLogs creates old logs for different config identities in age order.
func SeedRunLogs(t *testing.T, directory, kind string, count int) []string {
	t.Helper()
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	var paths []string
	for i := 0; i < count; i++ {
		path := filepath.Join(directory, fmt.Sprintf("%s_%016x_20000101_000000.000000000_%d.txt", kind, i+1, i))
		if err := os.WriteFile(path, []byte("previous run\n"), 0600); err != nil {
			t.Fatal(err)
		}
		stamp := time.Unix(946684800+int64(i), 0)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	return paths
}

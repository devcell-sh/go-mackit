package testutil

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	mu      sync.Mutex
	dirs    = map[string]string{}
	runTime = time.Now().UTC().Format("20060102T150405")
)

func ResultDir(t *testing.T) string {
	t.Helper()

	mu.Lock()
	defer mu.Unlock()

	key := t.Name()
	if d, ok := dirs[key]; ok {
		return d
	}

	name := sanitizeName(t.Name())
	dirName := runTime + "-" + name

	root := projectRoot()
	dir := filepath.Join(root, "test", "results", dirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("creating result dir: %v", err)
	}

	dirs[key] = dir
	return dir
}

func sanitizeName(name string) string {
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, " ", "_")
	return name
}

func projectRoot() string {
	_, f, _, _ := runtime.Caller(0)
	return filepath.Dir(filepath.Dir(filepath.Dir(f)))
}

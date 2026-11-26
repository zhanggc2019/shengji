package launch

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDataDirKeepsShengjiSeparate checks the default even when a legacy library exists.
func TestDataDirKeepsShengjiSeparate(t *testing.T) {
	base := t.TempDir()
	t.Setenv("LOCALAPPDATA", base)
	legacy := filepath.Join(base, "OddMeta", "OddMinutes Go")
	if err := os.MkdirAll(legacy, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "oddminutes.sqlite3"), []byte("legacy library"), 0600); err != nil {
		t.Fatal(err)
	}

	dir, err := dataDir()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(base, "Shengji")
	if dir != want {
		t.Fatalf("dataDir() = %q, want %q", dir, want)
	}
}

// TestListenServicePortConflict checks actual socket conflicts and an explicit port override.
func TestListenServicePortConflict(t *testing.T) {
	busy, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	address := busy.Addr().String()

	exact, err := listenService(address, false)
	if exact != nil {
		exact.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "已被占用") {
		t.Fatalf("explicit listener returned %v", err)
	}

	fallback, err := listenService(address, true)
	if err != nil {
		t.Fatal(err)
	}
	defer fallback.Close()
	if fallback.Addr().String() == address || !fallback.Addr().(*net.TCPAddr).IP.IsLoopback() {
		t.Fatalf("invalid fallback address %s", fallback.Addr())
	}
}

package launch

import (
	"net"
	"strings"
	"testing"
)

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

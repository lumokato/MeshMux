package runner

import (
	"net"
	"strconv"
	"testing"
)

func TestProxyOwnershipExact(t *testing.T) {
	for _, s := range []string{"localhost:2080", "127.0.0.1:7890", "http=127.0.0.1:2080;https=other:80", "proxy.example:2080", ""} {
		if ownsSystemProxy(s, 2080) {
			t.Fatalf("foreign proxy claimed: %s", s)
		}
	}
	if !ownsSystemProxy("127.0.0.1:2080", 2080) {
		t.Fatal("own endpoint not recognized")
	}
}
func TestLocalProxyReadyTracksListener(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_, p, _ := net.SplitHostPort(listener.Addr().String())
	port, _ := strconv.Atoi(p)
	if !LocalProxyReady(port) {
		t.Fatal("live listener not ready")
	}
	_ = listener.Close()
	if LocalProxyReady(port) {
		t.Fatal("closed listener ready")
	}
}

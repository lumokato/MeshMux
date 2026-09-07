package runner

import (
	"fmt"
	"net"
	"time"
)

func LocalProxyReady(port int) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(port)), 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// Ownership is deliberately exact; never clear an unrelated proxy or PAC setting.
func ownsSystemProxy(server string, port int) bool {
	return server == fmt.Sprintf("127.0.0.1:%d", port)
}

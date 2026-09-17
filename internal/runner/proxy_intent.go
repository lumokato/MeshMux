package runner

import (
	"os"
	"path/filepath"

	"github.com/meshmux/meshmux/internal/config"
)

// The system proxy flag lives in OS settings and does not survive updates,
// reboots, or tray restarts. proxyIntentFile records the user's last explicit
// choice so the tray can restore it instead of either losing it (the 0.3.2+
// behavior) or forcing the proxy on unconditionally at every launch (the
// pre-0.3.2 behavior that annoyed users who wanted it off).
const proxyIntentFile = "proxy-intent"

func ProxyIntentPath() string {
	return filepath.Join(config.LocalDataDir(), proxyIntentFile)
}

// SetProxyIntent records the user's last explicit proxy choice. Callers must
// only invoke it for real user actions (tray toggle, web UI toggle) — never
// for automatic cleanup paths such as OnExit or DisableOwnedProxy, which
// would erase the intent the restore relies on.
func SetProxyIntent(on bool) error {
	value := "off"
	if on {
		value = "on"
	}
	return os.WriteFile(ProxyIntentPath(), []byte(value), 0600)
}

// ProxyWanted reports whether the user last asked for the system proxy on.
// A missing or unreadable file counts as false: a fresh install starts with
// the proxy off until the user turns it on once.
func ProxyWanted() bool {
	data, err := os.ReadFile(ProxyIntentPath())
	if err != nil {
		return false
	}
	return string(data) == "on"
}

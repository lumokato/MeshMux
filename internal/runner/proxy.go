package runner

// Proxy toggles the desktop system proxy. Every platform supplies its own
// platformProxy / platformProxyEnabled / DisableOwnedProxy; platforms without
// an implementation return an explicit error instead of failing silently.
func Proxy(mode string, port int) error {
	return platformProxy(mode, port)
}

func ProxyEnabled() bool {
	return platformProxyEnabled()
}

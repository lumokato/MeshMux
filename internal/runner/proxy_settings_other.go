//go:build !windows

package runner

import "errors"

func platformProxy(string, int) error {
	return errors.New("system proxy is only implemented on Windows")
}
func platformProxyEnabled() bool  { return false }
func DisableOwnedProxy(int) error { return nil }

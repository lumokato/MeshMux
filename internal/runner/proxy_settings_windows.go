//go:build windows

package runner

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const internetSettings = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

func platformProxy(mode string, port int) (result error) {
	if mode == "show" {
		cmd := hiddenCommand("reg", "query", "HKCU\\"+internetSettings)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		return cmd.Run()
	}
	if mode != "on" && mode != "off" {
		return errors.New("proxy expects on, off, or show")
	}
	if mode == "on" && !LocalProxyReady(port) {
		return fmt.Errorf("local proxy on port %d is unavailable; system proxy unchanged", port)
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, internetSettings, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if mode == "off" {
		if err := key.SetDWordValue("ProxyEnable", 0); err != nil {
			return err
		}
		notifyProxyChange()
		return nil
	}
	backup, err := captureProxyValues(key)
	if err != nil {
		return err
	}
	defer func() {
		if result != nil {
			result = errors.Join(result, restoreProxyValues(key, backup))
			notifyProxyChange()
		}
	}()
	// Do not enable before all endpoint settings have been successfully written.
	if err := key.SetStringValue("ProxyServer", fmt.Sprintf("127.0.0.1:%d", port)); err != nil {
		return err
	}
	if err := key.SetStringValue("ProxyOverride", "localhost;127.*;<local>"); err != nil {
		return err
	}
	if err := key.SetDWordValue("ProxyEnable", 1); err != nil {
		return err
	}
	notifyProxyChange()
	return nil
}

type proxyValueBackup struct {
	name      string
	valueType uint32
	data      []byte
	exists    bool
}

func captureProxyValues(key registry.Key) ([]proxyValueBackup, error) {
	var values []proxyValueBackup
	for _, name := range []string{"ProxyServer", "ProxyOverride", "ProxyEnable"} {
		size, typ, err := key.GetValue(name, nil)
		if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
			values = append(values, proxyValueBackup{name: name})
			continue
		}
		if err != nil {
			return nil, err
		}
		data := make([]byte, size)
		_, typ, err = key.GetValue(name, data)
		if err != nil {
			return nil, err
		}
		values = append(values, proxyValueBackup{name: name, valueType: typ, data: data, exists: true})
	}
	return values, nil
}

func restoreProxyValues(key registry.Key, values []proxyValueBackup) error {
	var errs []error
	for _, value := range values {
		var err error
		if !value.exists {
			err = key.DeleteValue(value.name)
			if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
				err = nil
			}
		} else {
			name, e := windows.UTF16PtrFromString(value.name)
			if e != nil {
				errs = append(errs, e)
				continue
			}
			var data *byte
			if len(value.data) > 0 {
				data = &value.data[0]
			}
			code, _, _ := windows.NewLazySystemDLL("advapi32.dll").NewProc("RegSetValueExW").Call(uintptr(key), uintptr(unsafe.Pointer(name)), 0, uintptr(value.valueType), uintptr(unsafe.Pointer(data)), uintptr(len(value.data)))
			if code != 0 {
				err = syscall.Errno(code)
			}
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("restore %s: %w", value.name, err))
		}
	}
	return errors.Join(errs...)
}

func platformProxyEnabled() bool {
	key, err := registry.OpenKey(registry.CURRENT_USER, internetSettings, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()
	enabled, _, err := key.GetIntegerValue("ProxyEnable")
	return err == nil && enabled != 0
}

func DisableOwnedProxy(port int) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, internetSettings, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	server, _, err := key.GetStringValue("ProxyServer")
	if errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
		return nil
	}
	if err != nil {
		return err
	}
	if !ownsSystemProxy(server, port) {
		return nil
	}
	if err := key.SetDWordValue("ProxyEnable", 0); err != nil {
		return err
	}
	notifyProxyChange()
	return nil
}

func notifyProxyChange() {
	proc := windows.NewLazySystemDLL("wininet.dll").NewProc("InternetSetOptionW")
	_, _, _ = proc.Call(0, 39, 0, 0)
	_, _, _ = proc.Call(0, 37, 0, 0)
}

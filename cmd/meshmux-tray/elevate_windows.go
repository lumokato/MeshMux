//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"

	"github.com/meshmux/meshmux/internal/winservice"
)

func setDPIAwareness() {
	user32 := windows.NewLazySystemDLL("user32.dll")
	shcore := windows.NewLazySystemDLL("shcore.dll")
	if proc := user32.NewProc("SetProcessDpiAwarenessContext"); proc.Find() == nil {
		// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2
		_, _, _ = proc.Call(uintptr(^uintptr(3)))
		return
	}
	if proc := shcore.NewProc("SetProcessDpiAwareness"); proc.Find() == nil {
		// PROCESS_PER_MONITOR_DPI_AWARE
		_, _, _ = proc.Call(2)
		return
	}
	if proc := user32.NewProc("SetProcessDPIAware"); proc.Find() == nil {
		_, _, _ = proc.Call()
	}
}

func relaunchElevatedIfNeeded() (bool, error) {
	if winservice.Installed() {
		return false, nil
	}
	if windows.GetCurrentProcessToken().IsElevated() {
		return false, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return false, err
	}
	verb, err := syscall.UTF16PtrFromString("runas")
	if err != nil {
		return false, err
	}
	file, err := syscall.UTF16PtrFromString(exe)
	if err != nil {
		return false, err
	}
	args, err := syscall.UTF16PtrFromString(shellArgs(os.Args[1:]))
	if err != nil {
		return false, err
	}
	cwd, err := syscall.UTF16PtrFromString(filepath.Dir(exe))
	if err != nil {
		return false, err
	}
	if err := windows.ShellExecute(0, verb, file, args, cwd, windows.SW_NORMAL); err != nil {
		return false, err
	}
	return true, nil
}

func shellArgs(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "" || strings.ContainsAny(arg, " \t\"") {
			quoted = append(quoted, `"`+strings.ReplaceAll(arg, `"`, `\"`)+`"`)
		} else {
			quoted = append(quoted, arg)
		}
	}
	return strings.Join(quoted, " ")
}

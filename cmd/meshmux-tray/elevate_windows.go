//go:build windows

package main

import (
	"strings"

	"golang.org/x/sys/windows"
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
	// Only explicit service actions elevate; opening the management UI never does.
	return false, nil
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

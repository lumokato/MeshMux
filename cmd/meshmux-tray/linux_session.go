//go:build linux

package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const linuxSessionBusProbeTimeout = time.Second

type linuxTraySession struct {
	display        string
	waylandDisplay string
	busAddress     string
	runtimeDir     string
	key            string
}

type linuxSessionLock struct {
	file *os.File
}

func validateLinuxTraySession(getenv func(string) string, probeBus func(string) error) (linuxTraySession, error) {
	session := linuxTraySession{
		display:        strings.TrimSpace(getenv("DISPLAY")),
		waylandDisplay: strings.TrimSpace(getenv("WAYLAND_DISPLAY")),
		busAddress:     strings.TrimSpace(getenv("DBUS_SESSION_BUS_ADDRESS")),
		runtimeDir:     strings.TrimSpace(getenv("XDG_RUNTIME_DIR")),
	}
	if session.display == "" && session.waylandDisplay == "" {
		return linuxTraySession{}, errors.New("Linux tray requires DISPLAY or WAYLAND_DISPLAY")
	}
	if session.busAddress == "" {
		return linuxTraySession{}, errors.New("Linux tray requires DBUS_SESSION_BUS_ADDRESS")
	}
	if session.runtimeDir == "" {
		return linuxTraySession{}, errors.New("Linux tray requires XDG_RUNTIME_DIR for its session lock")
	}
	if !filepath.IsAbs(session.runtimeDir) {
		return linuxTraySession{}, fmt.Errorf("Linux tray XDG_RUNTIME_DIR must be absolute: %q", session.runtimeDir)
	}
	info, err := os.Stat(session.runtimeDir)
	if err != nil {
		return linuxTraySession{}, fmt.Errorf("access Linux tray XDG_RUNTIME_DIR: %w", err)
	}
	if !info.IsDir() {
		return linuxTraySession{}, fmt.Errorf("Linux tray XDG_RUNTIME_DIR is not a directory: %s", session.runtimeDir)
	}
	if err := probeBus(session.busAddress); err != nil {
		return linuxTraySession{}, fmt.Errorf("connect to Linux D-Bus session bus: %w", err)
	}
	session.key = linuxTraySessionKey(session.display, session.waylandDisplay, session.busAddress)
	return session, nil
}

func linuxTraySessionKey(display, waylandDisplay, busAddress string) string {
	sum := sha256.Sum256([]byte(display + "\x00" + waylandDisplay + "\x00" + busAddress))
	return fmt.Sprintf("%x", sum[:8])
}

func probeLinuxSessionBus(busAddress string) error {
	endpoint, err := linuxSessionBusEndpoint(busAddress)
	if err != nil {
		return err
	}
	conn, err := net.DialTimeout("unix", endpoint, linuxSessionBusProbeTimeout)
	if err != nil {
		return err
	}
	return conn.Close()
}

func linuxSessionBusEndpoint(busAddress string) (string, error) {
	for _, transport := range strings.Split(busAddress, ";") {
		transport = strings.TrimSpace(transport)
		if !strings.HasPrefix(transport, "unix:") {
			continue
		}
		for _, parameter := range strings.Split(strings.TrimPrefix(transport, "unix:"), ",") {
			key, value, found := strings.Cut(parameter, "=")
			if !found || (key != "path" && key != "abstract") {
				continue
			}
			decoded, err := url.PathUnescape(value)
			if err != nil {
				return "", fmt.Errorf("decode D-Bus %s address: %w", key, err)
			}
			if decoded == "" {
				return "", fmt.Errorf("D-Bus %s address is empty", key)
			}
			if key == "abstract" {
				return "@" + decoded, nil
			}
			return decoded, nil
		}
	}
	return "", fmt.Errorf("unsupported D-Bus session bus address %q", busAddress)
}

func acquireLinuxSessionLock(session linuxTraySession) (*linuxSessionLock, error) {
	path := filepath.Join(session.runtimeDir, "meshmux-tray-"+session.key+".lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("open Linux tray session lock: %w", err)
	}
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("secure Linux tray session lock: %w", err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, fmt.Errorf("MeshMux tray is already running in this graphical session")
		}
		return nil, fmt.Errorf("lock Linux tray graphical session: %w", err)
	}
	if err := file.Truncate(0); err == nil {
		_, _ = file.Seek(0, 0)
		_, _ = fmt.Fprintf(file, "%d\n", os.Getpid())
	}
	return &linuxSessionLock{file: file}, nil
}

func (l *linuxSessionLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	err := errors.Join(unix.Flock(int(l.file.Fd()), unix.LOCK_UN), l.file.Close())
	l.file = nil
	return err
}

func redirectLinuxTrayOutput(dataDir string) error {
	logDir := filepath.Join(dataDir, "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("create Linux tray log directory: %w", err)
	}
	path := filepath.Join(logDir, "tray.log")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return fmt.Errorf("open Linux tray log: %w", err)
	}
	if err := file.Chmod(0600); err != nil {
		_ = file.Close()
		return fmt.Errorf("secure Linux tray log: %w", err)
	}
	if err := unix.Dup2(int(file.Fd()), int(os.Stdout.Fd())); err != nil {
		_ = file.Close()
		return fmt.Errorf("redirect Linux tray stdout: %w", err)
	}
	if err := unix.Dup2(int(file.Fd()), int(os.Stderr.Fd())); err != nil {
		_ = file.Close()
		return fmt.Errorf("redirect Linux tray stderr: %w", err)
	}
	_ = file.Close()
	log.SetOutput(os.Stderr)
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	return nil
}

func logLinuxTrayStartup(session linuxTraySession) {
	log.Printf("MeshMux tray starting: display=%q wayland=%q session=%s", session.display, session.waylandDisplay, session.key)
}

func recordTrayActionError(action string, err error) {
	if err != nil {
		log.Printf("MeshMux tray action %q failed: %v", action, err)
	}
}

//go:build windows

package winservice

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	Name        = "MeshMux"
	displayName = "MeshMux Core"
)

func Install(executable, configPath string) error {
	executable, err := filepath.Abs(executable)
	if err != nil {
		return err
	}
	configPath, err = filepath.Abs(configPath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(executable); err != nil {
		return fmt.Errorf("service executable: %w", err)
	}
	if _, err := os.Stat(configPath); err != nil {
		return fmt.Errorf("service config: %w", err)
	}

	manager, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("connect service manager: %w", err)
	}
	defer manager.Disconnect()

	binaryPath := syscall.EscapeArg(executable) + " _service -config " + syscall.EscapeArg(configPath)
	service, openErr := manager.OpenService(Name)
	if openErr == nil {
		defer service.Close()
		current, err := service.Config()
		if err != nil {
			return err
		}
		current.BinaryPathName = binaryPath
		current.StartType = mgr.StartAutomatic
		current.ErrorControl = mgr.ErrorNormal
		current.DisplayName = displayName
		current.Description = "Starts and supervises the MeshMux mihomo core before user sign-in."
		current.DelayedAutoStart = false
		if err := service.UpdateConfig(current); err != nil {
			return fmt.Errorf("update service: %w", err)
		}
		return setRecovery(service)
	}

	service, err = manager.CreateService(Name, executable, mgr.Config{
		StartType:    mgr.StartAutomatic,
		ErrorControl: mgr.ErrorNormal,
		DisplayName:  displayName,
		Description:  "Starts and supervises the MeshMux mihomo core before user sign-in.",
	}, "_service", "-config", configPath)
	if err != nil {
		return fmt.Errorf("create service: %w", err)
	}
	defer service.Close()
	return setRecovery(service)
}

func setRecovery(service *mgr.Service) error {
	actions := []mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 15 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
	}
	if err := service.SetRecoveryActions(actions, 24*60*60); err != nil {
		return fmt.Errorf("set service recovery: %w", err)
	}
	return nil
}

func Remove() error {
	manager, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer manager.Disconnect()
	service, err := manager.OpenService(Name)
	if errors.Is(err, windows.ERROR_SERVICE_DOES_NOT_EXIST) {
		return nil
	}
	if err != nil {
		return err
	}
	defer service.Close()
	_ = stopService(service, 20*time.Second)
	if err := service.Delete(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_MARKED_FOR_DELETE) {
		return err
	}
	return nil
}

func Control(action string, timeout time.Duration) error {
	manager, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer manager.Disconnect()
	service, err := manager.OpenService(Name)
	if err != nil {
		return err
	}
	defer service.Close()

	switch strings.ToLower(action) {
	case "start":
		status, err := service.Query()
		if err == nil && status.State == svc.Running {
			return nil
		}
		if err := service.Start(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
			return err
		}
		return waitState(service, svc.Running, timeout)
	case "stop":
		return stopService(service, timeout)
	case "restart":
		if err := stopService(service, timeout); err != nil {
			return err
		}
		if err := service.Start(); err != nil {
			return err
		}
		return waitState(service, svc.Running, timeout)
	default:
		return fmt.Errorf("unsupported service action %q", action)
	}
}

func stopService(service *mgr.Service, timeout time.Duration) error {
	status, err := service.Query()
	if err != nil {
		return err
	}
	if status.State == svc.Stopped {
		return nil
	}
	if _, err := service.Control(svc.Stop); err != nil && !errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
		return err
	}
	return waitState(service, svc.Stopped, timeout)
}

func waitState(service *mgr.Service, wanted svc.State, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		status, err := service.Query()
		if err != nil {
			return err
		}
		if status.State == wanted {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("service did not reach state %s", stateName(wanted))
}

func Status() (string, error) {
	status, err := queryStatus()
	if err != nil {
		return "", err
	}
	return stateName(status.State), nil
}

func Installed() bool {
	_, err := queryStatus()
	return err == nil
}

func Running() bool {
	status, err := queryStatus()
	return err == nil && status.State == svc.Running
}

func AutostartEnabled() bool {
	manager, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return false
	}
	defer windows.CloseServiceHandle(manager)
	handle, err := windows.OpenService(manager, syscall.StringToUTF16Ptr(Name), windows.SERVICE_QUERY_CONFIG)
	if err != nil {
		return false
	}
	defer windows.CloseServiceHandle(handle)
	var needed uint32
	err = windows.QueryServiceConfig(handle, nil, 0, &needed)
	if err != nil && !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) {
		return false
	}
	buffer := make([]byte, needed)
	current := (*windows.QUERY_SERVICE_CONFIG)(unsafe.Pointer(&buffer[0]))
	if err := windows.QueryServiceConfig(handle, current, needed, &needed); err != nil {
		return false
	}
	return current.StartType == windows.SERVICE_AUTO_START
}

func DataDir() string {
	root := strings.TrimSpace(os.Getenv("ProgramData"))
	if root == "" {
		root = `C:\ProgramData`
	}
	return filepath.Join(root, Name)
}

func SecureDataDir(path string) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("service data directory is required")
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	commands := [][]string{
		{path, "/inheritance:r"},
		{path, "/grant:r", "*S-1-5-18:(OI)(CI)F", "*S-1-5-32-544:(OI)(CI)F"},
		{path, "/remove:g", "*S-1-5-32-545", "*S-1-5-11", "/T", "/C"},
	}
	for _, args := range commands {
		cmd := exec.Command("icacls.exe", args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if output, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("secure service data: %w: %s", err, strings.TrimSpace(string(output)))
		}
	}
	return nil
}

func RunElevated(action, configPath string) error {
	if action != "start" && action != "stop" && action != "restart" && action != "activate" {
		return fmt.Errorf("unsupported service action %q", action)
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	cli := filepath.Join(filepath.Dir(executable), "meshmux-cli.exe")
	if _, err := os.Stat(cli); err != nil {
		return fmt.Errorf("find service controller: %w", err)
	}
	resultFile, err := os.CreateTemp("", "meshmux-service-result-*")
	if err != nil {
		return err
	}
	resultPath := resultFile.Name()
	if _, err := resultFile.WriteString("pending\n"); err != nil {
		_ = resultFile.Close()
		_ = os.Remove(resultPath)
		return err
	}
	if err := resultFile.Close(); err != nil {
		_ = os.Remove(resultPath)
		return err
	}
	defer os.Remove(resultPath)
	verb, _ := syscall.UTF16PtrFromString("runas")
	file, _ := syscall.UTF16PtrFromString(cli)
	paramsText := "service " + action
	if action != "stop" && strings.TrimSpace(configPath) != "" {
		paramsText += " -config " + syscall.EscapeArg(configPath)
	}
	paramsText += " -result " + syscall.EscapeArg(resultPath)
	params, _ := syscall.UTF16PtrFromString(paramsText)
	cwd, _ := syscall.UTF16PtrFromString(filepath.Dir(cli))
	if err := windows.ShellExecute(0, verb, file, params, cwd, windows.SW_HIDE); err != nil {
		if errors.Is(err, windows.ERROR_CANCELLED) {
			return fmt.Errorf("操作已取消")
		}
		return err
	}
	deadline := time.Now().Add(75 * time.Second)
	for time.Now().Before(deadline) {
		data, readErr := os.ReadFile(resultPath)
		if readErr == nil {
			result := strings.TrimSpace(string(data))
			switch {
			case result == "ok":
				return nil
			case strings.HasPrefix(result, "error:"):
				return errors.New(strings.TrimSpace(strings.TrimPrefix(result, "error:")))
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return errors.New("管理员服务操作在 75 秒内没有返回结果")
}

func queryStatus() (svc.Status, error) {
	manager, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return svc.Status{}, err
	}
	defer windows.CloseServiceHandle(manager)
	service, err := windows.OpenService(manager, syscall.StringToUTF16Ptr(Name), windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return svc.Status{}, err
	}
	defer windows.CloseServiceHandle(service)
	var raw windows.SERVICE_STATUS_PROCESS
	var needed uint32
	err = windows.QueryServiceStatusEx(service, windows.SC_STATUS_PROCESS_INFO, (*byte)(unsafe.Pointer(&raw)), uint32(unsafe.Sizeof(raw)), &needed)
	if err != nil {
		return svc.Status{}, err
	}
	return svc.Status{State: svc.State(raw.CurrentState), ProcessId: raw.ProcessId}, nil
}

func stateName(state svc.State) string {
	switch state {
	case svc.Stopped:
		return "stopped"
	case svc.StartPending:
		return "start-pending"
	case svc.StopPending:
		return "stop-pending"
	case svc.Running:
		return "running"
	case svc.ContinuePending:
		return "continue-pending"
	case svc.PausePending:
		return "pause-pending"
	case svc.Paused:
		return "paused"
	default:
		return fmt.Sprintf("unknown-%d", state)
	}
}

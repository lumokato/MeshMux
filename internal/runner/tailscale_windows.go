//go:build windows

package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"github.com/meshmux/meshmux/internal/config"
	"golang.org/x/sys/windows"
)

// enumerateProcessIDs is the process-table reader used by reapStaleTailscaled.
// It is a variable so tests can drive the reaper without touching real
// processes.
var enumerateProcessIDs = func() []uint32 {
	capacity := 1024
	for attempt := 0; attempt < 4; attempt++ {
		pids := make([]uint32, capacity)
		var needed uint32
		if err := windows.EnumProcesses(pids, &needed); err != nil {
			appendRunnerLog("无法枚举进程列表: %v", err)
			return nil
		}
		count := int(needed) / 4
		if count < len(pids) {
			return pids[:count]
		}
		capacity *= 4
	}
	appendRunnerLog("进程列表在多次扩容后仍不完整，跳过遗留 tailscaled 清理")
	return nil
}

// reapStaleTailscaled terminates daemons that were started from the same
// MeshMux-owned binary but outlived their supervisor. Such a process still holds
// the machine-wide IPC endpoint, so every later start fails in
// safesocket.Listen with "Access is denied" and the data plane stays down until
// the leftover is killed. The daemon is removed before the launch, not after a
// failed one, because the failure is only visible in the daemon's own log.
//
// Only processes running the exact binary MeshMux is about to launch are
// candidates: matching is by resolved path, never by image name, because a
// system-wide Tailscale installation runs an executable with the same name and
// MeshMux must never touch it. An operator-supplied component path is left alone
// for the same reason - it may be that installation - so cleanup is limited to
// the MeshMux-owned default path.
func reapStaleTailscaled(cfg *config.Config) {
	exe, err := tailscaledExecutable(cfg)
	if err != nil {
		return
	}
	if !isDefaultTailscaledExecPath(exe) {
		appendRunnerLog("跳过遗留 tailscaled 清理：组件路径 %s 不是 MeshMux 自带的 %s", filepath.Clean(exe), filepath.Clean(config.DefaultTailscaledPath()))
		return
	}
	expected, err := filepath.Abs(exe)
	if err != nil {
		return
	}
	self := os.Getpid()
	var failures []string
	for _, pid := range enumerateProcessIDs() {
		if pid <= 4 || int(pid) == self {
			continue
		}
		if !managedPIDMatches(int(pid), expected) {
			continue
		}
		if err := processOS.kill(int(pid)); err != nil {
			failures = append(failures, fmt.Sprintf("PID %d: %v", pid, err))
			continue
		}
		appendRunnerLog("已回收遗留 tailscaled 实例 (PID %d)", pid)
	}
	if len(failures) > 0 {
		appendRunnerLog("回收遗留 tailscaled 实例失败: %s", strings.Join(failures, "; "))
	}
}

// assignToKillOnCloseJob puts the daemon into a job object that terminates every
// process still inside it when the last handle closes. Windows does not reap a
// child when its parent dies, so without the job a service crash, a Task Manager
// kill or a failed stop leaves tailscaled behind while its supervisor is gone:
// nothing is left to stop it, and it keeps the IPC endpoint reserved.
//
// The returned release closes the job. Supervision has already stopped the
// daemon by then, or has given up on it after the bounded wait; in the second
// case closing the job is the last chance to release the endpoint, which is
// exactly what the job exists for.
func assignToKillOnCloseJob(pid int) func() {
	if pid <= 0 {
		return func() {}
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		appendRunnerLog("创建 tailscaled 作业对象失败: %v", err)
		return func() {}
	}
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)),
		uint32(unsafe.Sizeof(limits)),
	); err != nil {
		_ = windows.CloseHandle(job)
		appendRunnerLog("配置 tailscaled 作业对象失败: %v", err)
		return func() {}
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		appendRunnerLog("打开 tailscaled (PID %d) 失败: %v", pid, err)
		return func() {}
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		_ = windows.CloseHandle(job)
		// A parent already inside a job without nested-job support rejects the
		// assignment. Supervision still stops the daemon on cancellation, so this
		// only loses the crash guarantee and is not fatal.
		appendRunnerLog("将 tailscaled (PID %d) 加入作业对象失败: %v", pid, err)
		return func() {}
	}
	return func() { _ = windows.CloseHandle(job) }
}

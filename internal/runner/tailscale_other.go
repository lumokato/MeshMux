//go:build !windows

package runner

import "github.com/meshmux/meshmux/internal/config"

// reapStaleTailscaled is a no-op outside Windows. The daemon is reachable
// through a socket inside the state directory rather than one machine-wide pipe,
// and the supervised daemon is stopped on cancellation, so a leftover cannot
// reserve a shared name the way a named pipe can.
func reapStaleTailscaled(cfg *config.Config) {}

// assignToKillOnCloseJob is a no-op outside Windows, which has no job objects.
// A daemon left behind by a supervisor that was killed outright still has to be
// stopped by hand on these platforms; cancellation remains the only mechanism
// that stops it.
func assignToKillOnCloseJob(pid int) func() { return func() {} }

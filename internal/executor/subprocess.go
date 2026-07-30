package executor

import (
	"os/exec"
	"syscall"
	"time"
)

// killGrace is how long a killed process group has to release the pipes the
// parent is still reading, before those reads are abandoned.
const killGrace = 5 * time.Second

// killGroup makes cmd the leader of its own process group and kills the whole
// group, not just the direct child, when the context is cancelled.
//
// A test agent is usually a launcher: npx spawns node, which spawns Playwright,
// which spawns a browser. Killing the child alone orphans the browser, which
// keeps holding the session and the CI machine's memory long after the run
// decided to stop - the cost that cancelling was supposed to avoid.
func killGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		// Negative pid means the process group led by that pid.
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = killGrace
}

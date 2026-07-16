package executor

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// alive reports whether the process is still around. Signal 0 only checks.
func alive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

// Cancelling a node must take the agent's whole process tree with it, not just
// the launcher: an orphaned browser keeps burning the CI machine the run was
// trying to stop paying for.
func TestKillGroup_KillsGrandchildren(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "grandchild.pid")

	// The agent spawns a background grandchild, records its pid, then blocks.
	dirWithAgent := writeAgent(t, "launcher.sh", `
sleep 300 &
echo $! > `+pidFile+`
wait`)

	ctx, cancel := context.WithCancel(context.Background())
	e := NewProcess(dirWithAgent, "")

	errc := make(chan error, 1)
	go func() {
		_, err := e.Run(ctx, node("t", "launcher.sh", nil, nil), Input{Attempt: 1})
		errc <- err
	}()

	pid := waitForPID(t, pidFile)
	if !alive(pid) {
		t.Fatalf("grandchild %d should be running before cancellation", pid)
	}

	cancel()
	select {
	case <-errc:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}

	deadline := time.Now().Add(5 * time.Second)
	for alive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("grandchild %d survived cancellation", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitForPID reads the pid the agent recorded, waiting for it to appear.
func waitForPID(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			if pid, convErr := strconv.Atoi(strings.TrimSpace(string(data))); convErr == nil && pid > 0 {
				return pid
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("the agent never recorded its grandchild pid")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

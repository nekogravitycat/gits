//go:build windows

package git

import (
	"context"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// configureProcessGroup gives the child its own console process group, so a Ctrl+C to gits is not
// also delivered straight to git behind its back.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// killProcessTree terminates the child and everything it spawned.
//
// CRITICAL: Windows has no process-group signal and TerminateProcess ends only one process,
// leaving git's ssh/credential-helper children alive holding gits' pipes (see runner.go
// Architecture Note). taskkill /T walks the tree and ships with every supported Windows; if it is
// somehow unavailable, Process.Kill (parent only) is the fallback.
func killProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	// NOTE: this runs on the timeout path, so the kill itself gets a short deadline -- blocking
	// again is the one thing that must not happen.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pid := strconv.Itoa(cmd.Process.Pid)
	kill := exec.CommandContext(ctx, "taskkill", "/T", "/F", "/PID", pid)
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := kill.Run(); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}

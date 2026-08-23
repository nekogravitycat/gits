//go:build windows

package git

import (
	"context"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// configureProcessGroup gives the child its own console process group, so a Ctrl+C delivered to
// gits is not also delivered straight to git behind its back.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// killProcessTree terminates the child and everything it spawned.
//
// Windows has no process-group signal, and TerminateProcess ends only the one process -- leaving
// git's ssh or credential-helper children alive, still holding the pipes gits waits on. taskkill
// /T walks the tree properly and ships with every supported Windows, so it needs no extra
// dependency. Process.Kill is the fallback if it is somehow unavailable: ending the parent alone
// is better than ending nothing.
func killProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	// The kill itself gets a short deadline: this runs on the timeout path, where the one thing
	// that must not happen is blocking again.
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

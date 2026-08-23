//go:build !windows

package git

import (
	"os/exec"
	"syscall"
)

// configureProcessGroup puts the child in its own process group so the whole tree can be signalled
// at once.
func configureProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessTree signals the child's entire process group.
//
// Killing only git would leave its ssh, credential helper and hook children alive, still holding
// the pipes gits is waiting on -- so the timeout would not actually end anything.
func killProcessTree(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	// The negative pid addresses the process group created above.
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		return cmd.Process.Kill()
	}
	return nil
}

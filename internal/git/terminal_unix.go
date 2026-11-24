//go:build !windows

package git

import (
	"os/exec"
	"syscall"
)

// detachTerminal starts git in a session of its own, with no controlling
// terminal for a prompt to reach.
func detachTerminal(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// interruptGit sends SIGTERM to git's process group, which detachTerminal
// made its own, so a hook it waits on stops too and git removes its lock
// files before it exits.
func interruptGit(cmd *exec.Cmd) error {
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
}

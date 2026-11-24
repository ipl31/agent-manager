//go:build windows

package git

import "os/exec"

// detachTerminal has no session to leave on Windows: git reads no
// controlling tty there, and the environment run sets already turns its
// prompts off.
func detachTerminal(*exec.Cmd) {}

// interruptGit ends git. Windows has no SIGTERM to let it clean up first.
func interruptGit(cmd *exec.Cmd) error {
	return cmd.Process.Kill()
}

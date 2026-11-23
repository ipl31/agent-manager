package terminal

import (
	"errors"
	"os"
	"os/exec"

	tea "github.com/charmbracelet/bubbletea"
)

// ExecLifetime admits the actual terminal operation and counts its return.
// The composing root owns closing admission and joining that work.
type ExecLifetime interface {
	Begin() bool
	Done()
}

var ErrExecStopped = errors.New("manager stopped before terminal command started")

// OwnedExecProcess checks admission when Bubble Tea actually runs the
// command, not when an earlier worker merely constructs its exec message.
func OwnedExecProcess(lifetime ExecLifetime, cmd *exec.Cmd, fn tea.ExecCallback) tea.Cmd {
	if cmd.Stdout == nil {
		cmd.Stdout = os.Stdout
	}
	return Exec(ownedExec{ExecCommand: processCommand{cmd}, lifetime: lifetime}, fn)
}

type ownedExec struct {
	tea.ExecCommand
	lifetime ExecLifetime
}

func (c ownedExec) Run() error {
	if !c.lifetime.Begin() {
		return ErrExecStopped
	}
	defer c.lifetime.Done()
	return c.ExecCommand.Run()
}

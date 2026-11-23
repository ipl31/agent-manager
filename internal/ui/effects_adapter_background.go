package ui

import (
	"context"

	"github.com/YoanWai/agent-manager/internal/ui/effects"
	tea "github.com/charmbracelet/bubbletea"
)

// Background owns one audited leaf's execution, including a late start.
// Batch/Sequence/Exec factories must fence their own leaves instead.
func (m *Model) Background(command tea.Cmd) tea.Cmd {
	return effects.Leaf(m.effects.background, command)
}

func (m *Model) JoinBackground(ctx context.Context) error {
	m.effects.background.CloseAdmission()
	return m.effects.background.Join(ctx)
}

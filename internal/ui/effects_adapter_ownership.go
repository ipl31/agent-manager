package ui

import (
	"context"
	"errors"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

// TakeoverMsg belongs to one runtime; a late message cannot quit a successor.
type TakeoverMsg struct {
	Owner  string
	Reason error
}

func (m *Model) SetOwnerID(id string) { m.effects.ownerID = id }
func (m *Model) TakenOver() bool      { return m.effects.takeover }

func (m *Model) requestTakeover(msg TakeoverMsg) (tea.Model, tea.Cmd) {
	if msg.Owner != m.effects.ownerID || m.effects.takeover {
		return m, nil
	}
	m.effects.takeover = true
	m.effects.takeoverReason = msg.Reason
	if msg.Reason != nil {
		m.reportErr(msg.Reason.Error())
	}
	// A takeover joins an existing drain; requestQuit's second invocation is
	// intentionally a user-forced abandon and must not be synthesized here.
	if m.effects.Quitting {
		return m, nil
	}
	return m.requestQuit()
}

// ConfirmShutdown joins resources after Bubble Tea has stopped. Cancellation
// alone, a closed result stream, or an empty abandoned list is not a fence.
func (m *Model) ConfirmShutdown(ctx context.Context) error {
	m.effects.background.CloseAdmission()
	var errs []error
	effectsJoined := true
	if m.effects.Lifetime != nil {
		m.effects.Lifetime.CloseAdmission()
		if err := m.effects.Lifetime.Join(ctx); err != nil {
			effectsJoined = false
			errs = append(errs, fmt.Errorf("UI effects: %w", err))
		}
	}
	if effectsJoined {
		if err := m.services.lifecycle.ClosePickers(ctx); err != nil {
			errs = append(errs, fmt.Errorf("resume picker: %w", err))
		}
	}
	if err := m.effects.background.Join(ctx); err != nil {
		errs = append(errs, fmt.Errorf("background work: %w", err))
	}
	if watch := m.focus.runtime.Watch; watch != nil {
		if err := watch.CloseContext(ctx); err != nil {
			errs = append(errs, fmt.Errorf("pane watcher: %w", err))
		}
	}
	return errors.Join(errs...)
}

func (m *Model) TakeoverReason() error { return m.effects.takeoverReason }

func (m *Model) ForcedQuit() bool { return m.effects.forcedQuit }

func (m *Model) CloseRuntimeAdmission() {
	m.effects.background.CloseAdmission()
	if m.effects.Lifetime != nil {
		m.effects.Lifetime.CloseAdmission()
	}
}

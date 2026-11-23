//go:build !windows

package ui

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/YoanWai/agent-manager/internal/config"
	"github.com/YoanWai/agent-manager/internal/sessioncmd"
	"github.com/YoanWai/agent-manager/internal/status"
	"github.com/YoanWai/agent-manager/internal/store"
	"github.com/YoanWai/agent-manager/internal/tmux"
	"github.com/YoanWai/agent-manager/internal/ui/effects"
	"github.com/google/uuid"
)

func TestTimedOutEffectJoinKeepsAcceptedPickerFollowupAdmission(t *testing.T) {
	m := buildModel(t)
	dir := t.TempDir()
	ready := filepath.Join(dir, "ready")
	script := filepath.Join(dir, "picker.sh")
	body := "printf 'WAITING\\n'\nwhile [ ! -f " + tmux.ShellQuote(ready) + " ]; do sleep 0.02; done\nprintf '> '\nIFS= read -r keys\nprintf '\\nPICKER_RECEIVED %s\\n' \"$keys\"\nsleep 30\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := m.services.cfg
	cfg.Tools = maps.Clone(cfg.Tools)
	cfg.Tools["ownedpicker"] = config.Tool{Command: "bash " + tmux.ShellQuote(script), InputPrefix: "^>", ResumePickerKeys: "/sessions"}
	dispatch := m.services.lifecycle.Capture(cfg, m.services.setSnapshot)
	sess := store.Session{ID: uuid.NewString()[:8], Name: "accepted-followup", Tool: "ownedpicker", Cwd: dir, Status: status.Dead}
	if err := m.services.store.CreateSession(sess); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.services.tmux.Kill(sess.ID) })
	entered, release := make(chan struct{}), make(chan struct{})
	command := effects.Command(1, m.effects.Lifetime, func() (effects.Result, error) {
		close(entered)
		<-release
		return dispatch.Revive(sess, sessioncmd.PaneSize{Width: 80, Height: 24})
	})
	completed := make(chan struct{})
	go func() { command(); close(completed) }()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	if err := m.ConfirmShutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		cancel()
		t.Fatalf("blocked effect falsely drained: %v", err)
	}
	cancel()
	close(release)
	<-completed
	ctx, cancel = context.WithTimeout(context.Background(), 15*time.Millisecond)
	if err := m.services.lifecycle.ClosePickers(ctx); !errors.Is(err, context.DeadlineExceeded) {
		cancel()
		t.Fatalf("accepted effect lost picker followup after bounded join: %v", err)
	}
	cancel()
	if err := os.WriteFile(ready, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.services.lifecycle.ClosePickers(ctx); err != nil {
		t.Fatal(err)
	}
}

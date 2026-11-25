package ui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/YoanWai/agent-manager/internal/status"
	"github.com/YoanWai/agent-manager/internal/store"
	uilifecycle "github.com/YoanWai/agent-manager/internal/ui/session/lifecycle"
	tea "github.com/charmbracelet/bubbletea"
)

func TestEqualStampListingCannotCrossEffectFence(t *testing.T) {
	t.Parallel()
	m := buildModel(t)
	at := time.Unix(100, 0)
	initial := at.Add(-time.Second)
	m.Update(refreshMsg{listedAt: initial, groupBases: map[string]string{"work": "previous"}})
	m.effects.latestObservation = at

	m.Update(refreshMsg{listedAt: at, groupBases: map[string]string{"work": "sentinel"}})
	if m.workspace.groupBases["work"] != "previous" || m.workspace.lastListedAt != initial {
		t.Fatalf("equal-stamp listing crossed the fence: bases=%v last accepted=%v",
			m.workspace.groupBases, m.workspace.lastListedAt)
	}
	newer := at.Add(time.Second)
	m.Update(refreshMsg{listedAt: newer, groupBases: map[string]string{"work": "sentinel"}})
	if m.workspace.groupBases["work"] != "sentinel" || m.workspace.lastListedAt != newer {
		t.Fatalf("post-effect listing did not install: bases=%v last accepted=%v",
			m.workspace.groupBases, m.workspace.lastListedAt)
	}
}

func TestEqualStampListingCannotConfirmDeletion(t *testing.T) {
	t.Parallel()
	m := buildModel(t)
	at := time.Unix(100, 0)
	m.Update(refreshMsg{listedAt: at.Add(-2 * time.Second)})
	m.effects.latestObservation = at.Add(-time.Second)
	mark := uilifecycle.GoneMark{At: at, Deleted: true}
	deleted := store.Session{ID: "deleted", Name: "deleted"}
	unrelated := store.Session{ID: "unrelated", Name: "unrelated"}
	m.ledger.lifecycle.Gone = map[string]uilifecycle.GoneMark{deleted.ID: mark}

	m.Update(refreshMsg{sessions: []store.Session{deleted, unrelated}, listedAt: at})
	if rows := m.workspace.sessions; len(rows) != 1 || rows[0].ID != unrelated.ID || m.workspace.lastListedAt != at {
		t.Fatalf("equal-to-marker listing did not install only the unrelated row: rows=%v last accepted=%v",
			rows, m.workspace.lastListedAt)
	}
	if got := m.ledger.lifecycle.Gone[deleted.ID]; got != mark {
		t.Fatalf("equal-to-marker listing changed the deletion mark: got %v want %v", got, mark)
	}
	m.Update(refreshMsg{listedAt: at.Add(time.Second)})
	if _, ok := m.ledger.lifecycle.Gone[deleted.ID]; ok {
		t.Fatalf("strictly newer empty listing left deletion markers=%v", m.ledger.lifecycle.Gone)
	}
}

func TestRequestedRefreshRechecksFenceAfterTurnEnds(t *testing.T) {
	t.Parallel()
	m := buildModel(t)
	createSession(t, m, "alpha", t.TempDir(), "")
	sess := askAfterTurn(t, m, "alpha", store.AfterTurnKill)
	if err := m.services.store.UpdateStatus(sess.ID, status.Idle); err != nil {
		t.Fatal(err)
	}
	loadStoredRows(t, m)

	// Keep both scripted pre-action times behind the real effect clock.
	at := time.Now().Add(-time.Second)
	m.workspace.lastListedAt = at.Add(-time.Second)
	m.effects.latestObservation = at
	initial := refreshMsg{listedAt: at, paneLines: map[string]string{sess.ID: "initial"}}
	m.applyTestMsg(t, initial)
	m.drainEffects(t)

	polls := 0
	var finalAt time.Time
	err := m.applyRequestedRefresh(t, initial, func() tea.Msg {
		polls++
		switch polls {
		case 1:
			return refreshMsg{
				listedAt: at.Add(time.Nanosecond), sessions: slices.Clone(m.workspace.sessions),
				turnsEnded: []string{sess.ID}, paneLines: map[string]string{sess.ID: "before kill"},
			}
		case 2:
			if !m.effects.latestObservation.After(at.Add(time.Nanosecond)) {
				t.Fatal("after-turn completion did not advance the fence")
			}
			finalAt = m.effects.latestObservation.Add(time.Nanosecond)
			return refreshMsg{listedAt: finalAt, paneLines: map[string]string{sess.ID: "after kill"}}
		default:
			t.Fatalf("unexpected replacement poll %d", polls)
			return nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := m.services.store.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != status.Dead || got.AfterTurn != "" || m.services.tmux.Exists(sess.ID) {
		t.Fatalf("after-turn kill did not complete: %+v", got)
	}
	if polls != 2 || m.workspace.paneLines[sess.ID] != "after kill" || m.workspace.lastListedAt != finalAt {
		t.Fatalf("final poll did not install: polls=%d pane line=%q last accepted=%v want=%v",
			polls, m.workspace.paneLines[sess.ID], m.workspace.lastListedAt, finalAt)
	}
}

func TestApplyCmdRejectsUnstampedFirstPoll(t *testing.T) {
	t.Parallel()
	m := buildModel(t)
	at := time.Unix(100, 0)
	initial := at.Add(-time.Second)
	m.Update(refreshMsg{listedAt: initial, groupBases: map[string]string{"work": "previous"}})
	m.effects.latestObservation = at

	err := m.applyCmdResult(t, func() tea.Msg {
		return refreshMsg{groupBases: map[string]string{"work": "sentinel"}}
	})
	if m.workspace.groupBases["work"] != "previous" || m.workspace.lastListedAt != initial {
		t.Fatalf("unstamped first poll changed the listing: bases=%v last accepted=%v",
			m.workspace.groupBases, m.workspace.lastListedAt)
	}
	if err == nil || !strings.Contains(err.Error(), "first poll did not return a stamped listing") {
		t.Fatalf("unstamped first poll returned error %v", err)
	}
}

func TestRequestedRefreshRequiresTimestampProgress(t *testing.T) {
	t.Parallel()
	at := time.Unix(100, 0)
	for _, tc := range []struct {
		name         string
		replacements []time.Time
		lastAccepted time.Time
		wantError    string
		wantPolls    int
	}{
		{name: "repeated pre-mutation stamp", replacements: []time.Time{at.Add(-time.Nanosecond)}, wantError: "after 100 replacement polls", wantPolls: 100},
		{name: "equal then older than accepted then newer", replacements: []time.Time{at, at.Add(time.Nanosecond), at.Add(3 * time.Nanosecond)}, lastAccepted: at.Add(2 * time.Nanosecond), wantPolls: 3},
		{name: "missing stamp", replacements: []time.Time{{}}, wantError: "did not return a stamped listing", wantPolls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := buildModel(t)
			m.effects.latestObservation = at
			m.workspace.lastListedAt = tc.lastAccepted
			initial := refreshMsg{listedAt: at.Add(-time.Second)}
			m.applyTestMsg(t, initial)
			m.drainEffects(t)

			polls := 0
			var raw time.Time
			err := m.applyRequestedRefresh(t, initial, func() tea.Msg {
				raw = tc.replacements[min(polls, len(tc.replacements)-1)]
				polls++
				return refreshMsg{listedAt: raw}
			})
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("replacement error=%v, want %q", err, tc.wantError)
				}
			} else if err != nil || m.workspace.lastListedAt != raw {
				t.Fatalf("raw stamp was not accepted: raw=%v last accepted=%v err=%v",
					raw, m.workspace.lastListedAt, err)
			}
			if polls != tc.wantPolls {
				t.Fatalf("replacement polls=%d, want %d", polls, tc.wantPolls)
			}
		})
	}
}

func TestFreshListingAtFollowsEveryObservation(t *testing.T) {
	at := time.Unix(100, 0)
	for _, source := range []string{"listing", "effect", "launch", "session", "group"} {
		t.Run(source, func(t *testing.T) {
			m := &Model{}
			switch source {
			case "listing":
				m.workspace.lastListedAt = at
			case "effect":
				m.effects.latestObservation = at
			case "launch":
				m.ledger.lifecycle.RecordLaunch("row", at)
			case "session":
				m.ledger.lifecycle.Gone = map[string]uilifecycle.GoneMark{"row": {At: at}}
			case "group":
				m.ledger.lifecycle.GoneGroups = map[string]uilifecycle.GoneMark{"group": {At: at}}
			}
			if got := m.freshListingAt(); !got.Equal(at.Add(time.Nanosecond)) {
				t.Fatalf("listing time=%v, want one nanosecond after %s observation %v", got, source, at)
			}
		})
	}
}

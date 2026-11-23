package rail

import (
	"reflect"
	"testing"
	"time"

	"github.com/YoanWai/agent-manager/internal/config"
	"github.com/YoanWai/agent-manager/internal/keybind"
	"github.com/YoanWai/agent-manager/internal/ui/presentation"
	"github.com/YoanWai/agent-manager/internal/ui/theme"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func assertCachedFrame(t *testing.T, cache *RenderCache, m Model, ctx RenderContext, previous Frame, now time.Time) Frame {
	t.Helper()
	got := cache.renderAt(m, ctx, previous, now)
	want := m.renderAt(ctx, previous, now)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cached frame differs from uncached rendering\ngot: %#v\nwant: %#v", got, want)
	}
	return got
}

func TestRenderCacheWarmAndMutableInputs(t *testing.T) {
	now := time.Unix(1700000000, 0)
	cases := map[string]func(*Model, *RenderContext){
		"name":   func(m *Model, _ *RenderContext) { m.snapshot.Sessions[0].Name = "Renamed"; m.rebuildRows() },
		"status": func(m *Model, _ *RenderContext) { m.snapshot.Sessions[0].Status = "waiting"; m.rebuildRows() },
		"queue":  func(m *Model, _ *RenderContext) { m.snapshot.Sessions[0].Queued = 2; m.rebuildRows() },
		"prompt-reply-after-turn": func(m *Model, _ *RenderContext) {
			m.snapshot.Sessions[0].Prompt = "new prompt"
			m.snapshot.Sessions[0].PaneLine = "new reply"
			m.snapshot.Sessions[0].AfterTurn = "archive"
			m.snapshot.Sessions[0].AfterTurnGlyph = "A"
			m.rebuildRows()
		},
		"hidden-rollup": func(m *Model, _ *RenderContext) {
			m.snapshot.Sessions[len(m.snapshot.Sessions)-1].Status = "errored"
			m.rebuildRows()
		},
		"remote": func(m *Model, _ *RenderContext) {
			m.snapshot.Hosts[0].State = HostOffline
			m.snapshot.Hosts[0].Trouble = "network unavailable"
		},
		"remote-session": func(m *Model, _ *RenderContext) {
			m.snapshot.Hosts[0].Sessions[0].Name = "remote rename"
			m.rebuildRows()
		},
		"fold":         func(m *Model, _ *RenderContext) { m.collapsed["project"] = true; m.rebuildRows() },
		"host-fold":    func(m *Model, _ *RenderContext) { m.hostFolds[HostFold{Host: "remote"}] = true; m.rebuildRows() },
		"archive":      func(m *Model, _ *RenderContext) { m.showArchived = true; m.rebuildRows() },
		"archive-map":  func(m *Model, _ *RenderContext) { m.snapshot.ArchivedGroups["project"] = true; m.rebuildRows() },
		"filter":       func(m *Model, _ *RenderContext) { m.filter = statusFilterAttention; m.rebuildRows() },
		"empty-groups": func(m *Model, _ *RenderContext) { m.hideEmptyGroups = true; m.rebuildRows() },
		"search":       func(m *Model, _ *RenderContext) { m.search = "Agent 1"; m.searching = true; m.rebuildRows() },
		"selection":    func(m *Model, _ *RenderContext) { m.Move(1, false) },
		"rename": func(m *Model, c *RenderContext) {
			s, _ := m.Selected()
			c.Rename = Rename{Target: s, View: "edited CURSOR"}
		},
		"menu": func(m *Model, _ *RenderContext) { s, _ := m.Selected(); m.openMenu(s, 25, 15, false) },
		"lift": func(m *Model, _ *RenderContext) {
			s, _ := m.Selected()
			m.enterReorder(s.key())
			m.reorder.drop = &dropTarget{key: m.rows[2].key()}
		},
		"scroll-anchor": func(m *Model, _ *RenderContext) {
			m.reorder.active = true
			m.reorder.autoscroll = autoscrollState{anchored: true, anchor: 35}
		},
		"width":             func(_ *Model, c *RenderContext) { c.Width = 22 },
		"height":            func(_ *Model, c *RenderContext) { c.Height = 10 },
		"terminal-geometry": func(_ *Model, c *RenderContext) { c.TerminalWidth = 40; c.TerminalHeight = 18; c.BodyOriginY = 3 },
		"comfortable":       func(_ *Model, c *RenderContext) { c.ComfortableRows = true },
		"mouse":             func(_ *Model, c *RenderContext) { c.MouseDisabled = true },
		"focus":             func(_ *Model, c *RenderContext) { c.Focused = true; c.EnterFocuses = true },
		"theme":             func(_ *Model, c *RenderContext) { c.Theme = theme.Palettes[1] },
		"glyph":             func(_ *Model, c *RenderContext) { c.GlyphMode = presentation.GlyphModeBasic },
		"phase": func(m *Model, c *RenderContext) {
			m.snapshot.Sessions[0].Status = "starting"
			m.rebuildRows()
			c.StartupPhase = 3
		},
		"foot": func(_ *Model, c *RenderContext) {
			c.Foot[0] = Line{Text: "CPU 90%", Tone: "#ff0000", Hit: Selection{SessionID: "0"}, HitOK: true}
		},
		"keys": func(_ *Model, c *RenderContext) { c.ListKeys = c.ListKeys.With(keybind.NewSession, keybind.Keys()) },
		"key-alias": func(_ *Model, c *RenderContext) {
			k, _ := keybind.Parse("z")
			c.ListKeys.Binding(keybind.NewSession).Keys()[0] = k
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			m, ctx := cacheFixture(60, now)
			var cache RenderCache
			first := assertCachedFrame(t, &cache, m, ctx, Frame{}, now)
			warm := assertCachedFrame(t, &cache, m, ctx, first, now)
			if &first.Lines[0] != &warm.Lines[0] {
				t.Fatal("unchanged render missed cache")
			}
			mutate(&m, &ctx)
			changed := assertCachedFrame(t, &cache, m, ctx, warm, now)
			assertCachedFrame(t, &cache, m, ctx, changed, now)
		})
	}
}

func TestRenderCacheClock(t *testing.T) {
	at := time.Unix(1700000000, 12345)
	for _, boundary := range []time.Duration{0, 5 * time.Second, time.Minute, time.Hour, 24 * time.Hour} {
		for _, delta := range []time.Duration{-time.Nanosecond, 0, time.Nanosecond} {
			m, ctx := cacheFixture(2, at)
			m.snapshot.Sessions[0].CreatedAt = at
			m.snapshot.Sessions[1].CreatedAt = at.Add(-234 * time.Millisecond)
			m.rebuildRows()
			var cache RenderCache
			before := at.Add(boundary - time.Second)
			frame := assertCachedFrame(t, &cache, m, ctx, Frame{}, before)
			frame = assertCachedFrame(t, &cache, m, ctx, frame, at.Add(boundary+delta))
			assertCachedFrame(t, &cache, m, ctx, frame, before)
		}
	}
}

func TestRenderCacheMenuAndProfiles(t *testing.T) {
	previousProfile := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(previousProfile) })
	now := time.Unix(1700000000, 0)
	m, ctx := cacheFixture(5, now)
	var cache RenderCache
	var frame Frame
	for _, profile := range []termenv.Profile{termenv.ANSI256, termenv.TrueColor, termenv.Ascii, termenv.ANSI256} {
		lipgloss.SetColorProfile(profile)
		frame = assertCachedFrame(t, &cache, m, ctx, frame, now)
		s, _ := m.Selected()
		m.openMenu(s, 240, 64, false)
		frame = assertCachedFrame(t, &cache, m, ctx, frame, now)
		m.menu.index = m.menu.nextItem(m.menu.index, 1)
		ctx.TerminalWidth, ctx.TerminalHeight = 80, 24
		frame = assertCachedFrame(t, &cache, m, ctx, frame, now)
		m.menu = rowMenu{}
		frame = assertCachedFrame(t, &cache, m, ctx, frame, now)
	}
}

func TestRenderCacheEveryToolAndEquivalentReconcile(t *testing.T) {
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1700000000, 0)
	for name, tool := range cfg.Tools {
		m, ctx := cacheFixture(1, now)
		m.snapshot.Sessions[0].Tool = name
		m.snapshot.Sessions[0].IsShell = tool.Shell
		m.rebuildRows()
		var cache RenderCache
		frame := assertCachedFrame(t, &cache, m, ctx, Frame{}, now)
		m.Reconcile(m.snapshot)
		got := assertCachedFrame(t, &cache, m, ctx, frame, now)
		if &got.Lines[0] != &frame.Lines[0] {
			t.Fatalf("%s: identical reconcile missed", name)
		}
		for _, mode := range []presentation.GlyphMode{presentation.GlyphModeBasic, presentation.GlyphModeAdvanced} {
			ctx.GlyphMode = mode
			ctx.ComfortableRows = true
			frame = assertCachedFrame(t, &cache, m, ctx, got, now)
		}
	}
}

func TestRenderCacheWindowContinuationAndIsolation(t *testing.T) {
	now := time.Unix(1700000000, 0)
	m, ctx := cacheFixture(100, now)
	m.cursor = 70
	var first, second RenderCache
	frame := assertCachedFrame(t, &first, m, ctx, Frame{}, now)
	if frame.Window.Start == 0 {
		t.Fatal("fixture needs a scrolled frame")
	}
	warm := assertCachedFrame(t, &first, m, ctx, frame, now)
	if &frame.Lines[0] != &warm.Lines[0] {
		t.Fatal("adjusted window seed should hit immediately")
	}
	other := m
	other.cursor = 2
	assertCachedFrame(t, &second, other, ctx, Frame{}, now)
	assertCachedFrame(t, &first, m, ctx, Frame{}, now)
	m.cursor = 2
	frame = assertCachedFrame(t, &first, m, ctx, frame, now)
	ctx.Height = 8
	assertCachedFrame(t, &first, m, ctx, frame, now)
}

func TestRenderCacheCopiesSurviveUnrelatedMiss(t *testing.T) {
	now := time.Unix(1700000000, 0)
	m, ctx := cacheFixture(4, now)
	var cache RenderCache
	frame := assertCachedFrame(t, &cache, m, ctx, Frame{}, now)
	ctx.Foot[0].Text = "new stats"
	frame = assertCachedFrame(t, &cache, m, ctx, frame, now)
	m.snapshot.Sessions[0].Name = "changed after stats miss"
	m.rows[3].sess.Name = "changed row after stats miss"
	m.snapshot.Hosts[0].Sessions[0].Name = "changed remote after stats miss"
	m.snapshot.Hosts[0].ArchivedGroups["remote-group"] = true
	m.collapsed["project"] = true
	key, _ := keybind.Parse("z")
	ctx.ListKeys.Binding(keybind.NewSession).Keys()[0] = key
	frame = assertCachedFrame(t, &cache, m, ctx, frame, now)
	ctx.Width--
	frame = assertCachedFrame(t, &cache, m, ctx, frame, now)
	m.snapshot.Sessions[0].Queued++
	m.rows[3].sess.Queued++
	ctx.Foot[0].Text = "changed stats again"
	assertCachedFrame(t, &cache, m, ctx, frame, now)
}

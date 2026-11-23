package rail

import (
	"maps"
	"slices"
	"time"

	"github.com/YoanWai/agent-manager/internal/keybind"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// RenderCache retains one immutable frame and copies the mutable inputs that
// produced it. Input continues to use the frame actually displayed by root.
type RenderCache struct {
	valid   bool
	model   Model
	ctx     RenderContext
	profile termenv.Profile
	keys    []renderBinding
	now     time.Time
	frame   Frame
}

type renderBinding struct {
	name string
	keys []keybind.Key
}

func (c *RenderCache) Render(m Model, ctx RenderContext, previous Frame) Frame {
	return c.renderAt(m, ctx, previous, time.Now())
}

func (c *RenderCache) renderAt(m Model, ctx RenderContext, previous Frame, now time.Time) Frame {
	profile := lipgloss.ColorProfile()
	if c.valid && c.profile == profile && previous.Window.Start == c.frame.Window.Start &&
		renderContextEqual(c.ctx, ctx) && c.keysEqual(ctx.ListKeys) && renderModelEqual(c.model, m) &&
		sameAges(m.rows, c.now, now) {
		return c.frame
	}
	frame := m.renderAt(ctx, previous, now)
	c.profile, c.now, c.frame = profile, now, frame
	saved := c.model
	c.model = m
	if !c.valid || !renderSnapshotEqual(saved.snapshot, m.snapshot) {
		c.model.snapshot = cloneSnapshot(m.snapshot)
	} else {
		c.model.snapshot = saved.snapshot
	}
	if !c.valid || !slices.Equal(saved.rows, m.rows) {
		c.model.rows = slices.Clone(m.rows)
	} else {
		c.model.rows = saved.rows
	}
	if !c.valid || !maps.Equal(saved.collapsed, m.collapsed) {
		c.model.collapsed = maps.Clone(m.collapsed)
	} else {
		c.model.collapsed = saved.collapsed
	}
	if !c.valid || !maps.Equal(saved.hostFolds, m.hostFolds) {
		c.model.hostFolds = maps.Clone(m.hostFolds)
	} else {
		c.model.hostFolds = saved.hostFolds
	}
	if !c.valid || !slices.Equal(saved.menu.items, m.menu.items) {
		c.model.menu.items = slices.Clone(m.menu.items)
	} else {
		c.model.menu.items = saved.menu.items
	}
	if m.reorder.drop != nil {
		drop := *m.reorder.drop
		c.model.reorder.drop = &drop
	}
	foot := c.ctx.Foot
	c.ctx = ctx
	if !c.valid || !slices.Equal(foot, ctx.Foot) {
		c.ctx.Foot = slices.Clone(ctx.Foot)
	} else {
		c.ctx.Foot = foot
	}
	c.ctx.ListKeys = keybind.Table{}
	if !c.valid || !c.keysEqual(ctx.ListKeys) {
		c.keys = nil
		for _, action := range ctx.ListKeys.Actions() {
			c.keys = append(c.keys, renderBinding{action.Name, slices.Clone(ctx.ListKeys.Binding(action.Name).Keys())})
		}
	}
	c.valid = true
	return frame
}

func (c *RenderCache) keysEqual(table keybind.Table) bool {
	actions := table.Actions()
	if len(actions) != len(c.keys) {
		return false
	}
	for i, action := range actions {
		saved := c.keys[i]
		if saved.name != action.Name || !slices.Equal(saved.keys, table.Binding(action.Name).Keys()) {
			return false
		}
	}
	return true
}

func renderContextEqual(a, b RenderContext) bool {
	return a.GlyphMode == b.GlyphMode && a.Width == b.Width && a.Height == b.Height &&
		a.TerminalWidth == b.TerminalWidth && a.TerminalHeight == b.TerminalHeight &&
		a.BodyOriginY == b.BodyOriginY && a.Theme == b.Theme && a.ComfortableRows == b.ComfortableRows &&
		a.MouseDisabled == b.MouseDisabled && a.Focused == b.Focused && a.EnterFocuses == b.EnterFocuses &&
		a.StartupPhase == b.StartupPhase && a.CursorMarker == b.CursorMarker && a.Rename == b.Rename &&
		slices.Equal(a.Foot, b.Foot)
}

func renderModelEqual(a, b Model) bool {
	if a.cursor != b.cursor || a.showArchived != b.showArchived || a.hideEmptyGroups != b.hideEmptyGroups ||
		a.filter != b.filter || a.search != b.search || a.searching != b.searching ||
		!slices.Equal(a.rows, b.rows) || !maps.Equal(a.collapsed, b.collapsed) || !maps.Equal(a.hostFolds, b.hostFolds) {
		return false
	}
	ar, br := a.reorder, b.reorder
	if ar.active != br.active || ar.key != br.key || ar.autoscroll != br.autoscroll {
		return false
	}
	if (ar.drop == nil) != (br.drop == nil) || ar.drop != nil && *ar.drop != *br.drop {
		return false
	}
	am, bm := a.menu, b.menu
	if am.active != bm.active || am.title != bm.title || am.index != bm.index ||
		am.anchorX != bm.anchorX || am.anchorY != bm.anchorY || !slices.Equal(am.items, bm.items) {
		return false
	}
	return renderSnapshotEqual(a.snapshot, b.snapshot)
}

func renderSnapshotEqual(a, b Snapshot) bool {
	if !slices.Equal(a.Sessions, b.Sessions) || !slices.Equal(a.Groups, b.Groups) ||
		!maps.Equal(a.ArchivedGroups, b.ArchivedGroups) || len(a.Hosts) != len(b.Hosts) {
		return false
	}
	for i, x := range a.Hosts {
		y := b.Hosts[i]
		if x.Name != y.Name || x.State != y.State || x.Err != y.Err || x.Trouble != y.Trouble ||
			!slices.Equal(x.Sessions, y.Sessions) || !slices.Equal(x.Groups, y.Groups) ||
			!maps.Equal(x.ArchivedGroups, y.ArchivedGroups) {
			return false
		}
	}
	return true
}

func sameAges(rows []treeRow, before, now time.Time) bool {
	for _, row := range rows {
		if row.kind != SessionRow {
			continue
		}
		at := row.sess.LastStatusAt
		if at.IsZero() {
			at = row.sess.CreatedAt
		}
		if !at.IsZero() {
			a, au := ageBucket(before.Sub(at))
			b, bu := ageBucket(now.Sub(at))
			if a != b || au != bu {
				return false
			}
		}
	}
	return true
}

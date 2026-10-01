package ui

import (
	"reflect"
	"testing"

	"github.com/YoanWai/agent-manager/internal/store"
)

// Each byte is a focus switch, scroll, or watcher frame. The update path is
// exercised without a tmux process or a clock, so longer event traces stay
// cheap enough for coverage-guided fuzzing.
func FuzzFocusPreviewTrace(f *testing.F) {
	f.Add([]byte{0, 3, 1, 5, 2, 3, 0, 5})
	f.Add([]byte{3, 4, 1, 5, 2, 4, 0, 3})
	f.Fuzz(func(t *testing.T, events []byte) {
		if len(events) > 64 {
			return
		}
		m := &Model{
			mode: modeFocus,
			rows: []treeRow{
				{sess: store.Session{ID: "A"}},
				{sess: store.Session{ID: "B"}},
			},
			preview: "initial",
		}
		for step, raw := range events {
			switch raw % 7 {
			case 0:
				m.cursor = 1 - m.cursor
			case 1:
				m.focusScroll = int(raw>>3)%5 + 1
			case 2:
				m.focusScroll = 0
			default:
				current := m.rows[m.cursor].sess.ID
				id := current
				if raw%7 == 5 {
					id = m.rows[1-m.cursor].sess.ID
				}
				frame := focusPreviewMsg{
					sessID: id, preview: "frame-" + string(rune('a'+step%26)),
					cursorX: step, cursorY: 3, cursorOK: true,
					paneStateOK: true, paneMouse: raw&8 != 0,
					paneSGR: raw&16 != 0, historySize: int(raw >> 5),
				}
				beforePreview, beforePane, beforeScroll := m.preview, m.pane, m.focusScroll
				m.handleMsg(frame)
				if id != current {
					if m.preview != beforePreview || !reflect.DeepEqual(m.pane, beforePane) || m.focusScroll != beforeScroll {
						t.Fatalf("step %d: stale frame changed selected session state", step)
					}
					continue
				}
				if m.pane.forID != current || m.pane.mouse != frame.paneMouse || m.pane.sgr != frame.paneSGR || m.pane.history != frame.historySize {
					t.Fatalf("step %d: current frame state lost", step)
				}
				if m.focusScroll == 0 && m.preview != frame.preview {
					t.Fatalf("step %d: live frame did not replace preview", step)
				}
				if m.focusScroll > 0 && m.preview != beforePreview {
					t.Fatalf("step %d: live frame replaced scrolled history", step)
				}
			}
		}
	})
}

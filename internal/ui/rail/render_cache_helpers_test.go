package rail

import (
	"fmt"
	"github.com/YoanWai/agent-manager/internal/keybind"
	"github.com/YoanWai/agent-manager/internal/ui/presentation"
	"github.com/YoanWai/agent-manager/internal/ui/theme"
	"slices"
	"time"
)

func cacheFixture(n int, now time.Time) (Model, RenderContext) {
	m := New(nil)
	snapshot := Snapshot{Groups: []string{"project", "project/nested"}, ArchivedGroups: map[string]bool{}}
	for i := 0; i < n; i++ {
		snapshot.Sessions = append(snapshot.Sessions, Session{ID: fmt.Sprint(i), Name: fmt.Sprintf("Agent %d", i), Tool: "codex", Group: "project/nested", Status: "idle", CreatedAt: now.Add(-12 * time.Second), Prompt: "Check this implementation", PaneLine: "Implementation checked"})
	}
	snapshot.Hosts = []Host{{Name: "remote", State: HostOnline, Groups: []string{"remote-group"}, ArchivedGroups: map[string]bool{}, Sessions: []Session{{ID: "remote-agent", Name: "Remote", Group: "remote-group", Status: "working"}}}}
	m.Reconcile(snapshot)
	keys := keybind.DefaultList()
	for _, action := range keys.Actions() {
		keys = keys.With(action.Name, keybind.Keys(slices.Clone(keys.Binding(action.Name).Keys())...))
	}
	return m, RenderContext{Width: 64, Height: 59, TerminalWidth: 256, TerminalHeight: 67, Theme: theme.Palettes[0], ListKeys: keys, GlyphMode: presentation.GlyphModeAdvanced, CursorMarker: "CURSOR", Foot: []Line{{Text: "CPU 10%"}}}
}

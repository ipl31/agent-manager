package status

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/YoanWai/agent-manager/internal/config"
)

type paneFixture struct {
	Name        string `json:"name"`
	Tool        string `json:"tool"`
	Source      string `json:"source"`
	Pane        string `json:"pane"`
	Status      string `json:"status"`
	Reply       string `json:"reply"`
	Echo        string `json:"echo"`
	DraftMarker bool   `json:"draft_marker"`
}

func paneCorpus(t testing.TB) (*Engine, []paneFixture, []string) {
	t.Helper()
	cfg, err := config.Default()
	if err != nil {
		t.Fatal(err)
	}
	engine, err := NewEngine(cfg)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("testdata/panes.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []paneFixture
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	return engine, fixtures, cfg.ToolNames()
}

func TestPaneCorpus(t *testing.T) {
	engine, fixtures, tools := paneCorpus(t)
	covered := map[string]bool{}
	for _, fixture := range fixtures {
		if !slices.Contains(tools, fixture.Tool) || fixture.Name == "" || fixture.Source == "" {
			t.Fatalf("invalid fixture metadata: %+v", fixture)
		}
		covered[fixture.Tool] = true
		t.Run(fixture.Name, func(t *testing.T) {
			pane := strings.ReplaceAll(fixture.Pane, "{{DRAFT}}", "fz_draft_original")
			plain := engine.Plain(fixture.Tool, pane)
			if got, _ := engine.Match(fixture.Tool, plain); got != fixture.Status {
				t.Fatalf("Match = %q, want %q", got, fixture.Status)
			}
			assertPaneMeaning(t, engine, fixture, plain)
			if fixture.DraftMarker {
				assertDraftExcluded(t, engine, fixture.Tool, plain, "fz_draft_original")
				if got, ok := engine.InputDraft(fixture.Tool, plain); !ok || !strings.Contains(got, "fz_draft_original") {
					t.Errorf("InputDraft = %q, ok=%t, want current draft", got, ok)
				}
			}
		})
	}
	for _, tool := range tools {
		if !covered[tool] {
			t.Errorf("no pane fixture for built-in tool %q", tool)
		}
	}
}

func assertPaneMeaning(t testing.TB, engine *Engine, fixture paneFixture, pane string) {
	t.Helper()
	if fixture.Reply != "" {
		if got, _, ok := engine.LastMessage(fixture.Tool, pane); !ok || got != fixture.Reply {
			t.Errorf("LastMessage = %q, ok=%t, want %q", got, ok, fixture.Reply)
		}
	}
	if fixture.Echo != "" {
		if got, ok := engine.LastUserEcho(fixture.Tool, pane); !ok || got != fixture.Echo {
			t.Errorf("LastUserEcho = %q, ok=%t, want %q", got, ok, fixture.Echo)
		}
	}
}

func assertDraftExcluded(t testing.TB, engine *Engine, tool, pane, draft string) {
	t.Helper()
	if reply, _, ok := engine.LastMessage(tool, pane); ok && strings.Contains(reply, "fz_draft_") {
		t.Errorf("LastMessage included draft %q: %q", draft, reply)
	}
	if reply, _, ok := engine.FullTurnText(tool, pane); ok && strings.Contains(reply, "fz_draft_") {
		t.Errorf("FullTurnText included draft %q: %q", draft, reply)
	}
}

func FuzzPaneDraftIsolation(f *testing.F) {
	engine, fixtures, _ := paneCorpus(f)
	for i, fixture := range fixtures {
		if fixture.DraftMarker {
			f.Add(uint8(i), []byte("a queued follow up"))
			f.Add(uint8(i), []byte("1. Yes, proceed? esc to interrupt"))
			f.Add(uint8(i), []byte("◆ Thinking… 12s • stop"))
		}
	}
	f.Fuzz(func(t *testing.T, selector uint8, input []byte) {
		if len(input) > 256 {
			return
		}
		fixture := fixtures[int(selector)%len(fixtures)]
		if !fixture.DraftMarker {
			return
		}
		limit := 128
		if fixture.Tool == "grok" {
			limit = 12
		}
		var draft strings.Builder
		draft.WriteString("fz_draft_")
		for _, r := range strings.ToValidUTF8(string(input), "") {
			if draft.Len() >= limit || r == '\n' || r == '\r' {
				break
			}
			if unicode.IsGraphic(r) {
				draft.WriteRune(r)
			}
		}
		pane := engine.Plain(fixture.Tool, strings.ReplaceAll(fixture.Pane, "{{DRAFT}}", draft.String()))
		if got, _ := engine.Match(fixture.Tool, pane); got != fixture.Status {
			t.Fatalf("%s: Match = %q, want %q", fixture.Name, got, fixture.Status)
		}
		assertDraftExcluded(t, engine, fixture.Tool, pane, draft.String())
		assertPaneMeaning(t, engine, fixture, pane)
	})
}

func FuzzPaneRobustness(f *testing.F) {
	engine, fixtures, tools := paneCorpus(f)
	for _, fixture := range fixtures {
		f.Add(fixture.Tool, []byte(strings.ReplaceAll(fixture.Pane, "{{DRAFT}}", "draft")))
	}
	f.Fuzz(func(t *testing.T, tool string, input []byte) {
		if len(input) > 16<<10 || len(tool) > 64 {
			return
		}
		if !slices.Contains(tools, tool) {
			tool = tools[0]
		}
		pane := engine.Plain(tool, string(input))
		if again := engine.Plain(tool, pane); again != pane {
			t.Fatalf("Plain is not idempotent for %q", tool)
		}
		state, _ := engine.Match(tool, pane)
		if !slices.Contains([]string{Working, Waiting, Finished, Errored, Idle, Dead, Starting}, state) {
			t.Fatalf("unknown status %q for %q", state, tool)
		}
		engine.RuleMatch(tool, pane)
		engine.TypingHold(tool, pane)
		engine.ActivityRegion(tool, pane)
		engine.LastMessage(tool, pane)
		engine.FullTurnText(tool, pane)
		engine.LastUserEcho(tool, pane)
		engine.InputDraft(tool, pane)
		for _, row := range strings.Split(pane, "\n") {
			engine.InputPrefix(tool, row)
		}
	})
}

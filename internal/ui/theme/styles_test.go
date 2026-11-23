package theme

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

func TestThemeStylesColorProfiles(t *testing.T) {
	previous := lipgloss.ColorProfile()
	t.Cleanup(func() { lipgloss.SetColorProfile(previous) })
	for _, palette := range Palettes {
		t.Run(palette.Name, func(t *testing.T) {
			// Reuse this set while changing profiles: Apply must not pin a profile.
			styles := newThemeStyles(palette)
			for _, profile := range []termenv.Profile{termenv.TrueColor, termenv.ANSI256, termenv.ANSI, termenv.Ascii} {
				t.Run(profile.Name(), func(t *testing.T) {
					lipgloss.SetColorProfile(profile)
					for _, c := range styleColorCases(styles) {
						for _, text := range []string{"key label", "▶ ▐ ✓ │", "> | done -", "first line\nsecond 界", "1️⃣ é"} {
							for _, width := range []int{0, 9} {
								got := c.style.Width(width).Render(text)
								want := legacyStyle(c).Width(width).Render(text)
								if got != want {
									t.Fatalf("%s width %d: got %q, want %q", c.name, width, got, want)
								}
								if ansi.Strip(got) != ansi.Strip(want) || lipgloss.Width(got) != lipgloss.Width(want) || lipgloss.Height(got) != lipgloss.Height(want) {
									t.Fatalf("%s: changed text or geometry", c.name)
								}
							}
						}
					}
				})
			}
		})
	}
}

func TestThemeStylesApplyReplacement(t *testing.T) {
	previousTheme, previousProfile := Current(), lipgloss.ColorProfile()
	t.Cleanup(func() { Apply(previousTheme); lipgloss.SetColorProfile(previousProfile) })
	changed := Palettes[0]
	changed.Bg, changed.Surface, changed.Bright, changed.Text = "#102030", "#405060", "#708090", "#a0b0c0"
	changed.Dim, changed.Subtle, changed.Accent, changed.Accent2 = "#fedcba", "#abcdef", "#aa3355", "#66aa22"
	changed.Working, changed.Finished, changed.Errored = "#223344", "#556677", "#8899aa"
	for _, palette := range []Theme{Palettes[0], Palettes[Index("paper")], changed, Palettes[0]} {
		Apply(palette)
		if Current() != palette {
			t.Fatal("Apply did not publish the complete palette")
		}
		for _, profile := range []termenv.Profile{termenv.ANSI256, termenv.TrueColor, termenv.Ascii, termenv.ANSI} {
			lipgloss.SetColorProfile(profile)
			for _, c := range styleColorCases(activeStyles.Load()) {
				if got, want := c.style.Render("theme"), legacyStyle(c).Render("theme"); got != want {
					t.Fatalf("%s/%s/%s: got %q, want %q", palette.Name, profile.Name(), c.name, got, want)
				}
			}
			s := activeStyles.Load()
			raw := func(style lipgloss.Style, fg, bg string) lipgloss.Style {
				return legacyStyle(styleColorCase{style: style, foreground: fg, background: bg})
			}
			key, label := raw(s.key, palette.Accent, ""), raw(s.legendLabel, palette.Text, "")
			checks := []struct{ got, want string }{
				{KeyCap("?", "help"), key.Render("?") + " " + label.Render("help")},
				{HintCap("o", "docs"), raw(s.legendBadge, palette.Bg, palette.Accent).Render("o docs")},
				{KeyCapLead("f", "focus"), raw(s.leadKey, palette.Accent, Mix(palette.Bg, palette.Accent, 0.28)).Render("f") + raw(s.leadLabel, palette.Bright, Mix(palette.Bg, palette.Accent, 0.28)).Render("focus")},
				{ImageChip("image"), raw(s.imageChip, palette.Bright, palette.Surface).Render("image")},
				{ImageChipPasting("image"), raw(s.imageChipPasting, palette.Working, palette.Surface).Render("image")},
			}
			for _, check := range checks {
				if check.got != check.want {
					t.Fatalf("helper changed: got %q, want %q", check.got, check.want)
				}
			}
		}
	}
}

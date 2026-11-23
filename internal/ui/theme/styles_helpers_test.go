package theme

import "github.com/charmbracelet/lipgloss"

type styleColorCase struct {
	name                   string
	style                  lipgloss.Style
	foreground, background string
}

func styleColorCases(s *themeStyles) []styleColorCase {
	t := s.theme
	lead := Mix(t.Bg, t.Accent, 0.28)
	return []styleColorCase{
		{"section", s.section, t.Accent, ""},
		{"selectedRow", s.selectedRow, t.Bright, t.Surface},
		{"muted", s.muted, t.Dim, ""},
		{"subtle", s.subtle, t.Subtle, ""},
		{"value", s.value, t.Text, ""},
		{"label", s.label, t.Subtle, ""},
		{"err", s.err, t.Errored, ""},
		{"done", s.done, t.Finished, ""},
		{"warn", s.warn, t.Working, ""},
		{"key", s.key, t.Accent, ""},
		{"annotation", s.annotation, t.Accent, ""},
		{"scopeBadge", s.scopeBadge, t.Bg, t.Accent2},
		{"focusBadge", s.focusBadge, t.Bg, t.Accent},
		{"inboxBadge", s.inboxBadge, t.Accent, ""},
		{"searchMatch", s.searchMatch, t.Bg, t.Accent},
		{"focusEdge", s.focusEdge, t.Accent, ""},
		{"chip", s.chip, "", t.Surface},
		{"imageChip", s.imageChip, t.Bright, t.Surface},
		{"imageChipPasting", s.imageChipPasting, t.Working, t.Surface},
		{"legendTitle", s.legendTitle, t.Subtle, ""},
		{"legendBadge", s.legendBadge, t.Bg, t.Accent},
		{"legendAlert", s.legendAlert, t.Bg, t.Working},
		{"legendLabel", s.legendLabel, t.Text, ""},
		{"leadKey", s.leadKey, t.Accent, lead},
		{"leadLabel", s.leadLabel, t.Bright, lead},
	}
}

func legacyStyle(c styleColorCase) lipgloss.Style {
	s := c.style
	if c.foreground != "" {
		s = s.Foreground(lipgloss.Color(c.foreground))
	}
	if c.background != "" {
		s = s.Background(lipgloss.Color(c.background))
	}
	return s
}

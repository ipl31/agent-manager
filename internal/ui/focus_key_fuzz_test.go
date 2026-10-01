package ui

import (
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func FuzzFocusKeyBytes(f *testing.F) {
	f.Add([]byte("quoted ' text ☃"), false)
	f.Add([]byte("one\ttwo"), true)
	f.Fuzz(func(t *testing.T, input []byte, alt bool) {
		if len(input) == 0 || len(input) > 256 {
			return
		}
		valid := strings.ToValidUTF8(string(input), "")
		if valid == "" {
			return
		}
		msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(valid), Alt: alt}
		command, ok := focusKeyCommand("am_x.0", msg)
		if !ok {
			t.Fatal("rune key was dropped")
		}
		parts := strings.Fields(command)
		if len(parts) < 5 || strings.Join(parts[:4], " ") != "send-keys -t am_x.0 -H" {
			t.Fatalf("unexpected command %q", command)
		}
		var encoded []byte
		for _, part := range parts[4:] {
			value, err := strconv.ParseUint(part, 16, 8)
			if err != nil {
				t.Fatalf("bad byte %q: %v", part, err)
			}
			encoded = append(encoded, byte(value))
		}
		want := []byte(valid)
		if alt {
			want = append([]byte{0x1b}, want...)
		}
		if string(encoded) != string(want) {
			t.Fatalf("encoded %q, want %q", encoded, want)
		}
		msg.Paste = true
		if command, ok := focusKeyCommand("am_x.0", msg); ok || command != "" {
			t.Fatalf("paste took raw-key path: %q", command)
		}
	})
}

package theme

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

var benchmarkStyleOutput string
var benchmarkStyles *themeStyles

func BenchmarkThemeStylesRender(b *testing.B) {
	previous := lipgloss.ColorProfile()
	b.Cleanup(func() { lipgloss.SetColorProfile(previous) })
	styles := newThemeStyles(Palettes[0])
	cases := styleColorCases(styles)
	for _, profile := range []termenv.Profile{termenv.TrueColor, termenv.ANSI256, termenv.ANSI, termenv.Ascii} {
		b.Run(profile.Name(), func(b *testing.B) {
			lipgloss.SetColorProfile(profile)
			for _, legacy := range []bool{false, true} {
				name := "resolved"
				if legacy {
					name = "raw"
				}
				b.Run(name, func(b *testing.B) {
					render := []lipgloss.Style{}
					for _, c := range cases {
						if legacy {
							render = append(render, legacyStyle(c))
						} else {
							render = append(render, c.style)
						}
					}
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						for _, style := range render {
							benchmarkStyleOutput = style.Render("f focus  ▶ active")
						}
					}
				})
			}
		})
	}
}

func BenchmarkThemeStylesBuild(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		benchmarkStyles = newThemeStyles(Palettes[i%len(Palettes)])
	}
}

package rail

import (
	"fmt"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

func BenchmarkRenderCache(b *testing.B) {
	previous := lipgloss.ColorProfile()
	b.Cleanup(func() { lipgloss.SetColorProfile(previous) })
	for _, profile := range []struct {
		name  string
		value termenv.Profile
	}{{"ANSI256", termenv.ANSI256}, {"TrueColor", termenv.TrueColor}} {
		b.Run(profile.name, func(b *testing.B) {
			lipgloss.SetColorProfile(profile.value)
			for _, size := range []struct{ width, height, terminal int }{{64, 59, 256}, {24, 20, 80}} {
				b.Run(fmt.Sprintf("%dx%d", size.terminal, size.height+8), func(b *testing.B) {
					for _, count := range []int{6, 60, 300} {
						b.Run(fmt.Sprintf("sessions%d", count), func(b *testing.B) {
							for _, churn := range []string{"warm", "foot", "status", "phase", "age", "mixed"} {
								for _, cached := range []bool{false, true} {
									name := "uncached"
									if cached {
										name = "cached"
									}
									b.Run(churn+"/"+name, func(b *testing.B) {
										now := time.Unix(1700000000, 0)
										m, ctx := cacheFixture(count, now)
										ctx.Width, ctx.Height, ctx.TerminalWidth = size.width, size.height, size.terminal
										if churn == "phase" || churn == "mixed" {
											m.snapshot.Sessions[0].Status = "working"
											m.snapshot.Sessions[0].PaneLine = ""
											m.rebuildRows()
										}
										var cache RenderCache
										frame := cache.renderAt(m, ctx, Frame{}, now)
										b.ReportAllocs()
										b.ResetTimer()
										for i := 0; i < b.N; i++ {
											switch churn {
											case "foot":
												if i%2 == 0 {
													ctx.Foot[0].Text = "CPU 10%"
												} else {
													ctx.Foot[0].Text = "CPU 90%"
												}
											case "status":
												state := "working"
												if i%2 == 0 {
													state = "idle"
												}
												m.snapshot.Sessions[0].Status = state
												for j := range m.rows {
													if m.rows[j].kind == SessionRow && m.rows[j].sess.ID == "0" {
														m.rows[j].sess.Status = state
													}
												}
											case "phase":
												ctx.StartupPhase = i % 4
											case "mixed":
												ctx.StartupPhase = (i / 5) % 4
												if (i/30)%2 == 0 {
													ctx.Foot[0].Text = "CPU 10%"
												} else {
													ctx.Foot[0].Text = "CPU 90%"
												}
												if i%60 == 0 {
													now = now.Add(5 * time.Second)
												}
											case "age":
												now = now.Add(5 * time.Second)
											}
											if cached {
												frame = cache.renderAt(m, ctx, frame, now)
											} else {
												frame = m.renderAt(ctx, frame, now)
											}
										}
									})
								}
							}
						})
					}
				})
			}
		})
	}
}

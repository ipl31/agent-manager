# Architect review — issue #594 fix

> **Do not include this file in the final PR.** It's a working note from an
> Opus-as-architect review of this branch, kept here for reference while the
> fix is iterated on. Drop it before opening/merging the PR; fold anything
> worth keeping into the PR description's Scope section instead.

Reviewed: `git diff origin/main..HEAD` on `am/issue-594-sidebar`
(commit `c8f772a fix(status): strip opencode's wide sidebar and keep the
prompt during tool output`, plus the docs-only `df945ae`).

## Verdict: needs changes (small)

The approach fits the project. The sidebar column is found in each frame
rather than stored as a fixed width, which follows "Dynamic over hardcoded".
The flags live in `builtinTools`, the same kind of per-tool switch as
`fits_height`. Two correctness problems should be fixed before merge.

## Findings, most important first

1. **`internal/status/status.go:186-190`: the panel column is chosen in
   random order.** The code picks the first column with 3 or more rows by
   walking a Go map, and map order changes on every run. If two columns
   qualify (the panel plus an indented code block or table in the reply),
   the chosen column can change from one poll to the next, so status and
   quotes can flicker. Pick deterministically, for example the column with
   the most rows and then the rightmost.

2. **`status.go:151-185`: normal text can be mistaken for the panel.**
   "Right half" is measured against the widest captured line, not the pane
   width. On a narrow pane with no panel, three rows of indented output past
   that midpoint count as a panel, and `stripSidebar` cuts them off, so
   `LastMessage` and `FullTurnText` lose real reply text. Require the column
   to also appear on rows that have transcript text to its left with a
   blank gap, as real panel rows do in both captures.

3. **`status.go:931-941` (`echo_opens_turn`): edge cases with no test.** The
   scan takes the first echo after the previous turn-end line. An
   interrupted turn may not print the `▣ … · 3.2s` line that `turn_end`
   matches, and a queued follow-up message could also confuse it. In both
   cases the next prompt would likely be quoted as the old one. If the
   prompt row has scrolled off the top, the original bug comes back. Add
   captures for these or name them as limits.

4. **`status.go:1195`: the `normalize` call in `TurnEndedState` does
   nothing.** The poller passes a region that `ActivityRegion` has already
   cleaned and cut above the cutoff line. `activityRegion(region)` then
   finds no cutoff and returns the text unchanged. Under "No speculative
   branches" this is dead code, so remove it. Also at `:935`, the
   `tr.turnEnd != nil` check repeats the check inside
   `previousTurnEndIndex`.

5. **`go.mod` and `status.go:11`: the dependency change is justified but
   avoidable.** `go-runewidth` v0.0.24 was already an indirect dependency
   and is only promoted to direct because of the new import. No version
   changed, so the bump isn't accidental. But the repo measures width with
   `ansi.StringWidth` (charmbracelet/x/ansi, already imported in this file).
   `runewidth.RuneWidth` also depends on locale settings (CJK locales make
   `┃` and `·` 2 cells wide) and ignores emoji sequences. Use
   `ansi.StringWidth` and drop the `go.mod` change.

6. **`status_test.go:1904-1961`: the tests only prove the fix works, not
   what it leaves alone.** Both captures are one wide single-turn session.
   Missing:
   - a narrow opencode pane where `normalize` changes nothing;
   - a reply with an indented block (finding 2);
   - multiple turns with an earlier `▣` line;
   - an interrupted turn;
   - a CJK or emoji row;
   - a pane resized mid-turn, where the panel appears or disappears between
     frames and the region hash changes.

   The existing opencode tests need a green run too.

7. **Scope and "Every tool" (to go in the PR description).** Turning this
   on for opencode only is defensible, since no other built-in tool draws a
   side panel. The Scope section should say so. It should also say that
   `side_panel` does nothing unless the tool defines `input_prefix` and
   `activity_cutoff` (`status.go:123`), and that another tool needs its own
   capture before enabling it. It should explain why `echo_opens_turn` is a
   per-tool flag rather than the default for every tool. That second point
   is a decision for the maintainer.

8. **Not a problem:** the new `echoedText` joins a wrapped prompt's rows
   back together and isn't gated by the flag. For tools without the flag,
   the backward scan already returns the newest echo row, so the join adds
   nothing and their behaviour is unchanged.

9. **Nit:** `REPRO_594.md` at the repo root is a working note. Keep it out
   of the merge and put its content in the PR description.

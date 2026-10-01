# OpenCode server Phase 0 + Phase 1 spike: completion report

Date: 2026-09-30. Plan: `docs/design/opencode-server/plan.md` (issue #594).
No PRs opened; work stays on the two branches below until reviewed.

- Phase 0: `am/opencode-server-phase0-muse` @ `093798a`
  (off `origin/main`)
- Spike: `am/opencode-server-spike-muse` @ `b0fc648`
  (off `origin/am/opencode-api-design`, docs only)

(Branch names carry a `-muse` suffix because the plan's exact names are
shared territory for other agents. Commits carry no co-author trailer;
none was provided.)

## Phase 0 (generic parts of #658)

- The poller prepares one `Engine.Plain` capture per poll and passes it
  to `rowLines`, `derivePaneStatus`,
  `maybeSendPendingInputWhenReady`, and `maybeDeliverInbox`.
  `typeForkKeys` and `sessioncmd.heldReason` use `Engine.Plain`
  instead of `ansi.Strip`.
- `LastUserEcho` joins wrapped echo rows via a new `echoedText`.
  Judgment call, reviewed with the `claude` CLI: #658's forward-only
  join is a strict no-op without `firstEchoBlock` (the backward scan
  lands on the block's last row), so `echoedText` walks back to the
  block start first, using one validity check for both directions.
  No turn tracking, no OpenCode-only patterns.
- All 15 `opencode_*.txt` frames copied from #658;
  `TestOpencodeFallbackFrames` pins their exact pane-path readings
  (sidebar leaks, shell-output-as-prompt, wrapped footer → errored)
  plus a focused wrap-join test.
- Excluded items verified absent: `side_panel`,
  `echo_turn_boundary`, `echo_tool`, `unwrapTurnBoundaries`,
  `firstEchoBlock`, turn tracking, config changes.
- Verified: `gofmt` clean, `go vet` clean, full
  `go test -race ./...` green, and a live OpenCode run confirmed a
  real wrapped prompt now reads whole (previously only its last row).

## Phase 1 spike (no production code)

All 7 checks ran against OpenCode 1.18.33 on Linux (throwaway `HOME`,
dedicated tmux socket, TUI on `127.0.0.1:18456` with
`OPENCODE_SERVER_PASSWORD` set). The default Big Pickle model answered
with no credentials at $0.00, so checks 3–5 cost nothing. Raw captures
are in `docs/design/opencode-server/spike/` on the spike branch
(`00_` baselines, `01–05` status and messages, `06` the full `/doc`
spec, `07–08` pending dialogs, `09–10` tool and compaction parts,
`11` the session list); the full write-up is the "Phase 1 results"
section of `plan.md`.

Findings that change Phases 2–4 (already folded into the plan body):

1. **Bind failure wedges the TUI** (alive, blank screen, no server,
   no retry, ignores `q`). Phase 2 must kill + relaunch once with a
   new port; pane fallback would strand the session.
2. **Idle = absent** from `/session/status` (`{type: idle}` never
   observed); fast turns never show `busy`. Phase 3 must track the
   newest message id (advance-while-absent ⇒ finished).
3. **Open question 1 is unresolvable from the API**: no
   `tui.session.select` on picker switches, `/api/session/active`
   always `{}`, `time.updated` untouched by a switch, and the screen
   can move mid-turn (proven: background session stayed `busy` after
   switching away, rejecting the busy-entry heuristic). Stay with
   launch-time `AgentSessionID`, document the gap, ask upstream.
4. **Tail rules**: newest user *text* part (compaction is a textless
   user message), newest assistant *text* part (reasoning carries
   text; tool calls are separate messages), no text ⇒ unavailable.
   Aborted turns lack `step-finish`.
5. Smaller: auth username is `opencode` (client must send it);
   dialogs keep status `busy` (pending-wins confirmed); retry shape
   from spec only (could not trigger live); polling costs 1–8 ms
   (no SSE); default config auto-approves tools (spike used
   `"permission": {"*": "ask"}`).

Unresolved (noted in the plan): after one API abort returned `true`
with status already clear, a final assistant message completed about
a minute later — genuinely outran the abort, or the abort landed just
after natural completion. A read-only poller never aborts, so this
only matters if `esc` aborts share it.

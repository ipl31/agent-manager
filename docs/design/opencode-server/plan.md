# OpenCode server status source: implementation plan

Goal: read an OpenCode session's status, pending dialogs, prompt, and
reply from OpenCode's own HTTP server, and use the current pane rules
only as a fallback. Background and evidence are in
[findings.md](findings.md). Closes #594, which replaces the parsing half
of #658.

## Shape

This adds a second tier-1 status source next to `claude-hooks`:

```toml
[tools.opencode]
status_source = "opencode-server"
```

It is set in `builtinTools`, not in `starterConfig`, so every install
gets it. Users see no new setting: the manager chooses this source in
code, following "Configurable in the UI, or not configurable".

What changes:

| Concern | Today | After |
|---|---|---|
| status | pane regex + activity hash | `/session/status`, `/permission`, `/question`; pane when unreachable |
| row prompt | `LastUserEcho` on the pane | last user text part from `/session/{id}/message` |
| row reply | `LastMessage` on the pane | last assistant text part |
| input delivery | tmux paste | unchanged (shared across tools) |
| preview | raw capture | unchanged |

## Phase 0: land what's generic from #658

Its own PR, independent of the rest:

- The poller calls `Engine.Plain` once per capture and passes the
  cleaned text to `rowLines`, `derivePaneStatus`,
  `maybeSendPendingInputWhenReady`, and `maybeDeliverInbox`.
  `typeForkKeys` and `sessioncmd.heldReason` use `Engine.Plain` instead
  of `ansi.Strip`.
- `echoedText` joins wrapped echo rows.
- Leave out `side_panel`, `echo_turn_boundary`, `echo_tool`,
  `unwrapTurnBoundaries`, and `firstEchoBlock`.
- Keep #658's `testdata/opencode_*.txt` frames. They become fixtures for
  the fallback path in Phase 4.

## Phase 1: verify the interface (spike, no merge)

Run each check against a real OpenCode on a throwaway `HOME` and its own
tmux socket, as described in CONTRIBUTING's "Capturing a TUI frame".
Record the results in this file before writing production code.

1. **Bind failure.** When the `--port` is already in use, does the TUI
   exit, run without a server, or retry? This decides whether a failed
   bind needs a relaunch or only a fallback to the pane.
2. **Auth.** With `OPENCODE_SERVER_PASSWORD` set, does the TUI still work,
   and does an unauthenticated request get 401?
3. **Status transitions.** Using a cheap or free model, capture
   `/session/status` through idle → busy → idle, through an interrupt
   (`esc`), and through a provider retry. Find out whether an idle
   session appears as `{type: idle}` or is missing from the map.
4. **Dialogs.** Trigger a permission prompt and a `question` tool call.
   Confirm `/permission` and `/question` list them while the dialog is on
   screen and return `[]` once it's answered. Check whether the V2 events
   change what the list endpoints return.
5. **Messages.** Check that `GET /session/{id}/message?limit=2` returns
   the newest messages and in what order. Check how a shell tool, a
   compaction, and an aborted turn appear in `parts`.
6. **Session switching.** Switch sessions with `/sessions` inside the
   TUI. Check whether `/session/status` or the `tui.session.select` event
   reveals which session is on screen. This decides how the poller finds
   the session id (see Open questions).
7. **Cost.** Time the three GETs on an idle and a busy session. The
   poller ticks every second across all sessions.

## Phase 1 results

Ran 2026-09-30 against OpenCode 1.18.33 on Linux: throwaway `HOME`,
dedicated tmux socket, TUI on `127.0.0.1:18456` with
`OPENCODE_SERVER_PASSWORD` set. The default model ("Big Pickle" via
OpenCode Zen) answers with no credentials at $0.00, so every turn below
was free. Raw captures are in
[`spike/`](spike/) (`00_` baselines, `01–05` status and messages,
`06` the full `/doc` spec, `07–08` pending dialogs, `09–10` tool and
compaction parts, `11` the session list).

1. **Bind failure: the TUI wedges, so a failed bind needs a relaunch,
   not a pane fallback.** With the `--port` taken by another listener,
   the process stays alive but never draws (blank screen), serves
   nothing, ignores `q`, and does not recover when the port frees up;
   SIGINT kills it. Phase 2 must kill and relaunch once with a new
   port when the server is unreachable shortly after launch; falling
   back to the pane would strand the session on a blank screen.
2. **Auth: basic, default username `opencode`, whole API gated.**
   The TUI works normally with the password set. Unauthenticated and
   wrong-password requests get 401 with an empty body, including
   `/doc`. With `OPENCODE_SERVER_USERNAME` unset, the username is
   `opencode`; the Phase 3 client authenticates as
   `opencode:<password>`.
3. **Idle sessions are absent from `/session/status`; fast turns never
   appear.** A turn reads `{"ses_…": {"type": "busy"}}` and vanishes
   when done — `{type: idle}` was never observed. There is a brief
   absence right after submit before `busy` appears, so a turn shorter
   than the poll interval looks like idle throughout. Phase 3 must
   treat absence as idle-or-finished AND track the newest message id:
   an id advance while absent means a turn completed unseen.
   Interrupt: `esc` did not stop a long single-step reasoning turn
   (still `busy` after 2.5 minutes and three attempts); aborting via
   `POST /session/{id}/abort` cleared the entry at once and the pane
   showed its usual `· interrupted` footer. A provider retry could not
   be triggered on demand; implement `retry` from the spec
   (`attempt`, `message`, `next`) and treat it as working.
4. **Dialogs list while on screen, clear when answered, and status
   stays `busy` throughout.** `/permission` returns the bash request
   (id, session, patterns, command metadata) and `/question` the
   question (header, options) until answered in the TUI, then `[]`.
   Pending-wins-over-`busy` precedence is confirmed. The V1 lists are
   what real dialogs populate; the V2 surface is a separate `/api/*`
   envelope API that stayed empty — keep to the V1 routes. Note the
   default config auto-approves `bash` and `write`; the spike set
   `"permission": {"*": "ask"}` to trigger the dialog.
5. **`limit=N` returns the newest N messages oldest-first; each step
   is its own message.** A tool call and the reply text are separate
   assistant messages, so the tail must scan newest-first for the
   newest *text* part, not the newest message. Part types observed:
   `step-start`, `reasoning` (carries text — never quote it),
   `text`, `tool`, `step-finish` (with `reason`). A shell call is a
   `tool` part named `bash` with
   `state:{status,input,output,title,metadata:{exit}}`. Compaction
   appends a *user* message whose only part is `{type: compaction}`
   (no text), then an assistant summary — skip textless user messages
   when reading the prompt. An aborted turn has no `step-finish` and
   may have no text at all — no text part means unavailable, not
   empty.
6. **Nothing reveals which session is on screen (Open question 1:
   not resolved).** `/session/status` only names busy sessions;
   `tui.session.select` is not emitted when switching with
   `/sessions` (SSE across a real switch showed only
   `server.connected` and heartbeats); `/api/session/active` is `{}`
   while idle and while busy; `time.updated` is untouched by a
   switch. Worse, the screen can move mid-turn: switching via
   `/tui/select-session` during a `busy` turn moved the pane while
   the background session stayed `busy`, so "follow the busy entry"
   is rejected too. Phase 3 should stay with the launch-time
   `AgentSessionID` and document `/sessions` switches as a known
   gap; asking upstream for an on-screen-session endpoint joins Open
   question 3.
7. **Polling is cheap: 1–8 ms per endpoint, idle or busy.**
   `session/status`, `permission`, `question`, and
   `message?limit=2` each answer in single-digit milliseconds on
   loopback. No SSE needed.

Unresolved: after one API abort returned `true` with status already
clear, a final assistant message completed about a minute later. The
aborted turn may genuinely outrun the abort, or the abort may have
landed just after natural completion; the spike did not retest it.
A read-only poller never aborts, so this only matters if `esc`
aborts share it — Phase 3's pane-dialog-wins rule stays the backstop
either way.

## Phase 2: launch

All four launch paths already go through `launch.Environment`
(`internal/launch/launch.go`): new session from the form, `sessioncmd`
spawn, relaunch in pane, and fork/revive. That makes it the one place to
change.

- When `tool.StatusSource == "opencode-server"`:
  - Choose a port: `net.Listen("tcp", "127.0.0.1:0")`, read the port,
    then close the listener.
  - Generate a 32-byte random password.
  - Write both to `<hooks dir>/opencode-<id>.json` with mode 0600. This
    follows the pattern of `StatusFile`, `NameFile`, and the review
    files. `Remove(id)` and the sweep already clean these up.
  - Append `--port <n>` to the command. Leave `--hostname` at its
    default of `127.0.0.1`.
- **Pass the password without it showing up anywhere visible.**
  `RelaunchInPane` sends the whole env through `send-keys` with
  `tmux.ExportEnv`, so anything in `env` appears in the pane's
  scrollback and shell history, where the pane capture would pick it up.
  Instead, have the command read the password from the file:
  `OPENCODE_SERVER_PASSWORD="$(cat '<file>')" opencode --port <n> …`.
  Only the file path is visible. Set it for this one process, not
  through `exportLines`, so later programs in the pane shell don't
  inherit it.
- Don't store the port or password in SQLite, so no migration is
  needed. If the file is missing, the source is unavailable and the pane
  rules are used.
- Phase 1 shows a bind failure wedges the TUI (alive, blank screen,
  no server, no retry), so a pane fallback would strand the session.
  When the server is unreachable shortly after launch, kill and
  relaunch once with a new port on the first-launch path.

## Phase 3: client and status

New package `internal/opencodeserver`, standard library only:

- `Client{port, password}` with `Status(ctx)`, `Pending(ctx)`
  (permission + question), and `Tail(ctx, sessionID)` (prompt, reply).
  - Each call has a timeout of about 300 ms.
  - Decode into minimal structs and ignore unknown fields.
  - If a required field is missing, return `ok=false`; don't guess.
- Poller (`internal/ui/poller.go`):
  - `statusSources[tool] == "opencode-server"` gets its own branch in
    `derivePaneStatus`, next to the `claude-hooks` branch. Mapping:
    - pending permission or question → `waiting`
    - `busy` → `working`
    - `retry` → `working` (show `retry.message` as the quote)
    - absent after a busy observation, or absent with the newest
      message id advanced since the last poll (a turn completed
      unseen — Phase 1 never observed `{type: idle}`) → `finished`
    - absent otherwise → `idle`
    - `sess.Acked` handled the same way as in `applyHookStatus`
  - Reuse `applyHookStatus`'s rule that a pane dialog wins over a
    stale source, since a dialog on screen is always current.
  - If the agent isn't alive (`!agentAlive`) or the call fails → the
    current pane path.
  - `errored`: the pane's `limit_line` and error rules still apply.
    Phase 1 did not surface a `session.error` payload over plain GETs,
    so nothing is added from the server here.
- All HTTP runs inside the poller's `refreshOnce` command, never in
  `Update`, to keep the "Update never blocks" invariant.
- Start with polling, not SSE. Polling fits the existing tick, has no
  connection lifecycle, and survives OpenCode restarts. Move to SSE only
  if Phase 1's cost numbers call for it.

## Phase 4: row text

- In `rowLines`, generalize the `mcpStyles == "claude"` branch into a
  per-source tail lookup:
  - `claudeTail` for `claude-hooks`
  - `opencodeTail` for `opencode-server`
- For `opencode-server`, the server tail is the primary source, not only
  a recovery path. The screen can't reliably tell prompts from tool
  output, so use the API first and the pane only when it's unavailable.
  Read newest-first: the prompt is the newest user *text* part (skip
  textless compaction markers) and the reply the newest assistant
  *text* part (skip reasoning-only and tool-only messages); a turn
  with no text part counts as unavailable.
- Cache per session, keyed on the newest message id and part count, the
  same way `claudeTailCache` is keyed on file stats.
- Pass the prompt through `typedPrompt` and `isManagerEcho`, as
  `claudeTail` does, so coordination notes and directives stay hidden.

## Phase 5: tests

Each test goes in the `_test.go` file next to the code it covers.

- `internal/opencodeserver`: an `httptest.Server` serving recorded JSON
  from Phase 1:
  - idle, busy, and retry status
  - permission and question pending
  - message tails with text, tool, shell, and aborted parts
  - 401, timeout, and malformed JSON → `ok=false`
- `internal/launch`: the command gets `--port`, the password file is
  0600, and the password appears in neither `env` nor the command
  string.
- `internal/ui/poller_test.go`:
  - the source is preferred when reachable
  - pane fallback when the file is missing or the server is down
  - a dialog on screen overrides a stale `busy`
  - busy → idle gives `finished`, and `idle` once acked
- Fallback regressions: #658's sidebar frames confirm the pane path is
  no worse than today. They are not expected to be fully correct there.
- Live check, required before the PR says "verified": build the binary,
  run it under a throwaway `HOME` on its own socket, drive OpenCode
  through a shell tool, a permission prompt, an interrupt, and a
  two-turn conversation in a pane at least 160 columns wide with the
  sidebar visible, and read the frames back.

## Coverage (AGENTS.md matrix)

- **Tools:** OpenCode only. No other supported CLI exposes a status
  server from its TUI. Codex's app-server and Gemini have no equivalent
  that attaches to the interactive TUI. The `status_source` mechanism
  stays generic, and the PR's Scope section names this limitation.
- **Platforms:** loopback TCP and a 0600 file work the same on Linux,
  macOS, and WSL2, where OpenCode runs on the Linux side with no Windows
  interop. There is no `runtime.GOOS` branch. Verify on macOS and WSL2
  before claiming them.
- **Terminals / SSH:** the manager and OpenCode are on the same host, so
  the user's terminal and SSH play no part. Nothing new talks to the
  terminal.
- **tmux 3.1:** no new tmux features. This deliberately avoids
  `new-session -e`, which needs tmux 3.2.
- **Keyboard / mouse:** no new action, row, or setting.
- **Thin wrapper:** one launch flag and one environment variable, both
  needed for session management. No model, agent, or config override,
  and no plugin written into the user's OpenCode config.

## Open questions

1. **Which session is on screen.** Phase 1 step 6 found no signal:
   `tui.session.select` is not emitted on picker switches,
   `/api/session/active` is always `{}`, `time.updated` is untouched
   by a switch, and the screen can move mid-turn so the busy entry
   need not be on screen. Stay with the launch-time `AgentSessionID`
   and document `/sessions` switches as a known gap. If upstream
   ever exposes the on-screen session, the poller could adopt it
   then — and decide at that point whether to move `AgentSessionID`
   so revive resumes the session the user switched to.
2. **Existing sessions.** Sessions launched before this change have no
   port and keep using the pane rules until they are revived. That's
   acceptable. Say so in the release notes, not in `docs/messages.json`.
3. **Upstream.** Ask OpenCode for `--port 0` to mean "choose a free port
   and report it", or for a Unix socket option. Either would remove the
   port race and the loopback exposure. Ask as well for a query
   reporting which session the TUI currently shows (Phase 1 step 6
   found no such signal). Neither is needed to ship this.

## Order and size

| PR | Content | Rough size |
|---|---|---|
| 1 | Phase 0 (generic parts of #658) | small |
| 2 | Phases 2–3 (launch, client, status) | medium |
| 3 | Phase 4 (row text) + removal of any sidebar workarounds that are no longer needed | small |

Phase 1 comes before PR 2 and its results are added to this document.

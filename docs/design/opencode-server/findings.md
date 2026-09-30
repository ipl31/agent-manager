# OpenCode support: findings

Review of [PR #658](https://github.com/YoanWai/agent-manager/pull/658)
("fix(status): keep OpenCode sidebar and shell output out of session
quotes", closes #594). The question: is there a better way to support
OpenCode fully?

Short answer: yes. OpenCode's TUI runs on top of its own HTTP server, and
that server's API returns session status, pending dialogs, and the
prompt and reply text directly. Reading those facts from the API avoids
the bug class that #658 works around by parsing the screen.

## What #658 does

In a wide pane, OpenCode draws a sidebar (context usage, cost, LSP status,
onboarding) beside the transcript. Shell tools also draw in the same `┃`
gutter as the user's prompt. As a result, the session row's quote and
prompt can come from sidebar text or shell output.

The PR fixes this in the screen parser:

- `side_panel` (`^[ \t]*╹▀+`): takes the composer border's width as the
  transcript width and cuts every row above it to that width.
- `echo_turn_boundary` (`^[ \t]*▣ {2}[^·\n]+ · [^·\n]+`): uses the
  assistant footer to separate turns, and joins footer rows that wrapped.
- `echo_tool` (`^\s*┃\s+(?:\$ |[\x{2800}-\x{28ff}] )`): recognizes a
  foreground shell block so it does not replace the prompt.
- `Engine.Plain` now does all of this. The poller calls it once per
  capture and passes the result to row text, status, `TypingHold`, and
  inbox delivery.

It adds about 220 lines to `internal/status/status.go`, three `Tool`
fields that only OpenCode uses, and 15 captured frames.

## Assessment

### What is worth keeping

- **One cleaned capture per poll.** Before this change, row text, status,
  and the delivery guards each ran their own `ansi.Strip`, so the same
  frame could be read differently by each. Preparing it once and passing
  it along is a correct, tool-agnostic change that stands on its own.
- **Joining wrapped echo rows** (`echoedText`). This is generic and small.

### Concerns

1. **It depends on OpenCode's drawing.** Each new pattern encodes a detail
   of the current layout: the border glyphs, the `▣  Agent · model ·
   duration` footer, the `$ ` shell prefix, and the braille spinner.
   [PRODUCT.md](../../../PRODUCT.md#thin-wrapper-principle) asks us to
   avoid "parsing human-facing output to recreate upstream state". When
   OpenCode redesigns (it has already moved from a Go TUI to OpenTUI),
   these patterns break without an error, and the result is a wrong
   quote, not a failure anyone notices.
2. **Some cases cannot be fixed from the screen.** The PR's own
   limitations section says:
   - a non-shell tool block can't be told apart from a user prompt once
     earlier turns have scrolled away;
   - when the geometry is ambiguous, the sidebar still leaks into the
     quote.

   The information isn't on the screen, so more rules will not help.
3. **The changes reach every reader of the capture.** `unwrapTurnBoundaries`
   joins and blanks rows in the text that status rules, the activity hash,
   `TypingHold`, and the dialog check all read. A regex mistake meant for
   quotes can now affect typing and inbox delivery too.
4. **More upkeep in the busiest code.** `lastEchoIndex` and
   `firstEchoBlock` add turn-tracking logic that has to change whenever
   OpenCode changes how a turn is laid out.

## The better source: OpenCode's server

Checked against OpenCode 1.18.33 on Linux, using a separate tmux socket:

- `opencode --help` lists `--port` (default 0) and `--hostname` (default
  `127.0.0.1`) on the TUI command itself, not only on `opencode serve`.
- With no `--port`, the TUI opens no TCP listener; it talks to its server
  in-process. With `--port N` it listens on `127.0.0.1:N`.
- `GET /doc` serves an OpenAPI 3 spec (`title: opencode`). The published
  `@opencode-ai/sdk` is generated from it.
- The binary reads `OPENCODE_SERVER_USERNAME` and
  `OPENCODE_SERVER_PASSWORD`, which enable basic auth on the listener.

Endpoints that cover what the manager currently parses from the pane:

| Manager needs | Endpoint | Shape |
|---|---|---|
| working / idle | `GET /session/status` | map of session id to `{type: idle \| busy \| retry}`; `retry` carries `attempt`, `message`, `next` |
| waiting (permission) | `GET /permission` | list of pending requests; `[]` when none |
| waiting (question) | `GET /question` | list of pending questions; `[]` when none |
| errored | `session.error` event, `retry` status | error payload |
| prompt and reply | `GET /session/{id}/message?limit=N` | `[{info: {role, …}, parts: [{type: "text", text}, …]}]` |
| push updates | `GET /event` (SSE) | `session.status`, `session.idle`, `permission.asked`, `question.asked`, `message.part.updated`, … |

The SSE stream starts with `server.connected` and then emits `plugin.added`
events and the rest.

### Why this fits the project

- **The manager already does this for Claude Code.** `status_source =
  "claude-hooks"` is a tier-1 status source that `derivePaneStatus` checks
  before the pane regexes (`internal/ui/poller.go`). `claudeTail` reads
  Claude's transcript when the screen can't anchor a quote. OpenCode would
  be the second tool on both paths.
- **It already relies on OpenCode's own interfaces.** Session capture uses
  `opencode session list` and `opencode export` and not OpenCode's storage
  files. The comment in `runOpencode` gives the reason: those files can
  change between releases.
- **PRODUCT.md allows the flag.** It permits "launch flags … required for
  session management". `--port` changes no model, agent, or user setting.
- **Screen rules remain as a fallback.** When the API is unreachable or
  returns something unexpected, the current `[tools.opencode]` pane rules
  run as they do today.

### Risks

- **The API is still evolving.** The spec lists `PermissionV2*` and
  `QuestionV2*` events next to the originals, plus an experimental
  `/api/session/*` set of routes. Use only the long-standing routes listed
  above, decode loosely, and treat any mismatch as "use the pane".
- **Choosing a port.** Default `--port 0` means "no listener", not "pick a
  free port". The manager has to choose the port itself, and another
  process can take it between the choice and OpenCode's bind.
- **Exposure on loopback.** Without auth, any local user could call
  `POST /session/{id}/shell`. A password for each session is required.
- **Session switching.** A user can move the TUI to another session with
  `/sessions`, so the captured `AgentSessionID` is not guaranteed to be
  the session on screen.

## Recommendation

Split #658:

- **Land now:** the single-`Plain`-per-capture refactor and the
  wrapped-echo join, with their tests.
- **Don't land:** `side_panel`, `echo_turn_boundary`, `echo_tool`, and
  the turn-tracking logic in `lastEchoIndex`.

Replace the part that doesn't land with an OpenCode server status source,
as laid out in [plan.md](plan.md). That fixes #594 at its cause and
covers the cases #658 lists as unsolved. The captured frames from #658
remain useful as regression tests for the fallback path.

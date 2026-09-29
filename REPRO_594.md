# Repro / verification guide for agent-manager issue #594

**Branch:** `am/issue-594-sidebar`  
**Fork:** https://github.com/ipl31/agent-manager/tree/am/issue-594-sidebar  
**Related issue:** https://github.com/YoanWai/agent-manager/issues/594

## Conversation summary

- Analyzed the issue: on wide opencode panes (≈150 columns and up) opencode draws a right-hand sidebar beside the transcript. The sidebar strings were being quoted as the reply and the prompt.
- Reviewed the proposed fix with Claude Opus as architect.
- Implemented the fix in an isolated worktree (`issue-594-sidebar`) based on `origin/main`.
- Added two opt-in tool config fields:
  - `side_panel` — detect and strip the right-hand panel per frame.
  - `echo_opens_turn` — read the first user echo after the previous turn boundary so tool output sharing the same gutter is not mistaken for the prompt.
- Enabled both fields for `[tools.opencode]`.
- Added regression tests with real 150-column opencode pane captures.
- Verified `gofmt`, `go vet`, and `go test -race ./...` all pass.
- Built a local binary (`/tmp/agent-manager-new`) and left an original backup (`/tmp/agent-manager-original`) on the Linux machine where the work was done.

## What you need on your WSL machine

- WSL2 with a Linux distro (Ubuntu/Debian work fine)
- `tmux` installed
- `opencode` installed (`1.18.32` is the version from the issue)
- A terminal window that is at least **160 columns wide**
- The branch checked out and built

## Build the branch

```bash
git clone https://github.com/ipl31/agent-manager.git
cd agent-manager
git fetch origin
git checkout am/issue-594-sidebar
go build ./...
```

> If you do not have Go installed on WSL, install it first (e.g. `sudo apt update && sudo apt install golang-go`, or download a release from https://go.dev/dl).

## Reproduce the bug with the original release binary

Install the current release binary (or build from `main`) so you can see the bug first:

```bash
# Example: download the current release or use the binary already on your PATH
agent-manager --version
# Expected: agent-manager 0.38.0 (or whatever release you have)
```

Create a wide 150×45 tmux pane running opencode:

```bash
mkdir -p /tmp/am-repro
tmux -L am-repro new-session -d -s oc -x 150 -y 45 \
  'opencode -m opencode/mimo-v2.6-flash-free'
```

Send the repro prompt:

```bash
tmux -L am-repro send-keys -t oc \
  'Run the shell command `sleep 20; echo second-done` in the foreground and wait for it to finish, then reply with one short sentence.' \
  Enter
```

Start the old agent-manager in a second terminal:

```bash
agent-manager
```

Watch the opencode row in the manager:

- **Reply line (`↳`)** will show sidebar strings such as `$0.00 spent`, `LSPs are disabled`, or `Context`.
- **Prompt line** will show the command row `$ sleep 20; echo second-done` and then `second-done`, instead of your original prompt.

## Verify the fix with the new build

Quit the running agent-manager.

Install the newly built binary temporarily:

```bash
cp /home/ken/.local/bin/agent-manager /tmp/agent-manager-original   # back up old
cp ./agent-manager /home/ken/.local/bin/agent-manager               # install new
agent-manager --version
# Expected: agent-manager dev
```

> Adjust the paths above to wherever your old binary and the newly built binary live.

Start the new agent-manager:

```bash
agent-manager
```

The `oc` tmux session is still there, so the manager will pick it up. If the command already finished, send it again:

```bash
tmux -L am-repro send-keys -t oc \
  'Run the shell command `sleep 20; echo second-done` in the foreground and wait for it to finish, then reply with one short sentence.' \
  Enter
```

Watch the row:

- **Reply line** now shows the actual reply sentence, never sidebar strings.
- **Prompt line** stays on your original prompt while the command runs and after it finishes.

## Revert to the original binary

Quit agent-manager, then restore the original:

```bash
cp /tmp/agent-manager-original /home/ken/.local/bin/agent-manager
agent-manager --version
# Expected: the original version number
```

## Cleanup

```bash
tmux -L am-repro kill-session -t oc
rm -rf /tmp/am-repro /tmp/agent-manager-original
```

## Running the tests

If you want to run the regression tests on WSL:

```bash
cd agent-manager
mkdir -p /tmp/amtest
env -u TMUX TMUX_TMPDIR=/tmp/amtest go test -race ./internal/status/ -run TestOpencodeWidePaneSidebar -v
```

To run the full suite:

```bash
mkdir -p /tmp/amtest
env -u TMUX TMUX_TMPDIR=/tmp/amtest go test -race ./...
```

## Notes

- The sidebar only appears when the tmux pane is at least about 150 columns wide. If you do not see `Context`, token count, or cost on the right side, make the pane wider.
- The free model `opencode/mimo-v2.6-flash-free` is used because it works without adding API keys.
- Do not run two agent-manager binaries at the same time against the same tmux server; quit one before starting the other.

# Voice dictation architecture findings

This note evaluates [issue #29](https://github.com/YoanWai/agent-manager/issues/29),
which proposes free, local dictation into the quick prompt bar on macOS, Linux,
and WSL. The recommendation is a small feasibility spike using FFmpeg and an
externally installed `whisper-cli`, followed by a separate production decision.
The feature would supply editable prompt text through the existing delivery path,
without integrating with individual agent providers.

These are architecture findings, not results from a completed spike. Three
independent approach assessments and a separate Claude Code review informed this
recommendation. Code was reviewed at `cd3ca23`; the relevant findings were
rechecked against `87569e4` on September 30, 2026. No microphone capture,
transcription benchmark, or end-to-end voice implementation has been tested.

## Approach comparison

| Approach | Benefits | Maintenance and coverage costs | Recommendation |
| --- | --- | --- | --- |
| External FFmpeg recorder and `whisper-cli` | Small process boundary; preserves the current Go build; common behavior across agent CLIs | Users obtain executables and weights; usable audio depends on the host; recorder and inference lifecycles need explicit ownership | Use for the spike |
| Bundled native helper with audio capture and whisper.cpp | Controls capture, conversion, and inference; reduces external executable setup | Native artifacts for every release target, permissions, packaging, notices, installer and updater changes; still cannot access an SSH client's microphone | Reconsider only if measured setup friction justifies it |
| Dictation on the terminal user's machine | Local recognition followed by terminal paste can serve remote or headless managers and WSL without an audio bridge | An external shortcut changes the requested interaction; identical quick-bar controls over SSH require a paired companion and explicit transport | Document a verified offline alternative; defer an owned companion |

If a native implementation becomes necessary, prefer an isolated helper to cgo
inside the manager. The current [release build](../.goreleaser.yaml) disables cgo
and targets Darwin/Linux on amd64/arm64. The [installer](../install.sh) and
[self-updater](../internal/update/apply.go) currently install or replace one
manager executable. A bundled helper changes the distribution contract as well
as the build.

Client-side dictation and terminal paste do not require a manager-owned
companion. That alternative must still be verified for the chosen application
and terminal. Generic OS dictation is not proof of offline operation:
[Windows voice typing requires an internet connection](https://support.microsoft.com/en-us/accessibility/windows/use-voice-typing-to-talk-instead-of-type-on-your-pc).

## Findings that affect the design

- **WSLg has a documented microphone path.** Its PulseAudio server supplies a
  microphone source and projects the connection into the user distribution.
  This establishes intended support, not working capture on a particular
  machine. [WSLg architecture](https://github.com/microsoft/wslg#pulse-audio-plugin)
- **Recorder availability is a capability check.** Finding FFmpeg on `PATH`
  does not prove it contains the required input backend or can reach a source.
  Its documented `-devices` output identifies compiled capabilities. On macOS,
  `none:default` selects the default audio input without freezing a device index.
  [FFmpeg device documentation](https://ffmpeg.org/ffmpeg-devices.html)
- **SSH separates the terminal and microphone hosts.** The architectural
  implication of [OpenSSH's remote execution and forwarding interfaces](https://man.openbsd.org/ssh.1)
  is that a remotely launched recorder uses remote audio devices unless an
  additional transport is configured. Successful capture could therefore record
  the wrong room. The existing [remote-terminal check](../internal/termseq/termseq.go)
  is a starting point for refusing capture over detected remote connections;
  detach/reattach and ambiguous client ownership still need testing.
- **The quick bar already has asynchronous identity machinery.** The shared
  [composer](../internal/ui/composer.go) gives each opened box a generation and
  rejects stale clipboard results. However,
  [clearing after send](../internal/ui/quick.go) does not change that generation.
  Dictation needs its own job ID and explicit invalidation as well.
- **The draft has a 2,000-character limit.** Transcript insertion must check
  capacity before using the textarea API. An overflowing transcript must remain
  visibly pending for retry or discard, rather than being silently truncated.
- **Ctrl+R already means review.** Quick mode intercepts keys before the list,
  so reuse is technically possible but confusing. A proposed initial binding
  is Ctrl+G, subject to real terminal testing. Use a toggle; the baseline terminal
  path must not depend on key-release events.

The suggested `base` model is a benchmark candidate, not an application default
or a maintained model catalog. Measure cold-start latency and recognition of
coding terms before claiming near-realtime performance. Also verify spoken
language behavior: the documented CLI defaults to English; selecting multilingual
weights alone does not select language autodetection.
[Whisper CLI options](https://raw.githubusercontent.com/ggml-org/whisper.cpp/master/examples/cli/README.md)

## Platform investigation

All capture paths below are candidates for testing, not verified support claims.

| Environment | Initial candidate | Evidence required |
| --- | --- | --- |
| macOS | FFmpeg AVFoundation using the default microphone | Actual audio, permission attribution through terminal/tmux, denied permission, repeated start/stop, Intel and Apple Silicon coverage |
| Linux with PulseAudio or PipeWire Pulse compatibility | FFmpeg Pulse input using the default source | Compiled input support, reachable server, microphone audio and device-loss behavior |
| Linux with ALSA only or native PipeWire without Pulse compatibility | Probe the actual audio interface; evaluate FFmpeg ALSA or a targeted additional recorder | Successful capture on a representative setup before adding a fallback adapter |
| WSL2 with WSLg | PulseAudio capture through WSLg | Windows microphone permissions, non-silent input, repeated capture and cancellation |
| WSL without a usable bridge | Investigate a Windows recorder separately | Device discovery, path handling, process termination, permissions, and Windows executable distribution |
| SSH client microphone | Client-side offline dictation and terminal paste | Verified insertion with the selected client tool; an explicit companion if quick-bar start/stop parity is required |

Use the terminal's paste action for the client-side alternative. The manager's
Ctrl+V initiates a clipboard read on the manager host.

WSL remains a must-support requirement. Failed WSLg capture blocks a claim that
the feature meets that requirement; it calls for more investigation or a
different approach. The issue's prototype deliverable requires macOS/Linux
demonstrations and WSL findings, but production support needs working WSL evidence.

Integrated SSH client capture and WSL without a usable bridge remain coverage
gaps in the recommended design. An unavailable message preserves ordinary prompt
entry but does not satisfy voice parity. Under [the review policy](../REVIEW.md),
the eventual implementation must name these gaps, describe the work to cover
them, and resolve the intended acceptance boundary before claiming full support.

## Claude Code review and resulting decisions

Claude agreed with external subprocesses, keeping cgo out of the manager, job
identity checks, a recording toggle, and explicit submission. Its most useful
additions were a remote-capture guard, focused macOS permission testing, handling
of silent input, visible overflow recovery, and a clearer separation between
the spike and production work.

The resulting recommendation also qualifies several of its suggestions:

- A PCM pipe may help a recorder notice that its parent disappeared, but it is
  not a complete cleanup guarantee: a blocked recorder may not write again.
  Keep explicit cancellation, bounded termination, and crash tests.
- Silent and empty recordings need dedicated tests. Signal energy alone is not
  an established speech detector, and low volume does not prove permission was
  denied. Do not introduce unsupported heuristics or promises about hallucination
  prevention.
- Model selection/import belongs in Settings. Requiring users to place weights
  in a particular directory should not be the only setup path.
- Keep the service reusable for the shared composer, but retain the quick bar
  as the requested scope. New Session form controls are a separate extension.
- A failed WSL test cannot silently become an exclusion, and OS dictation cannot
  be assumed to satisfy the offline requirement.

## Feasibility spike

Build a small prototype on an isolated branch. It should answer the questions
that determine whether production work is justified, without first implementing
a complete setup UI or expanding the recorder matrix.

1. Demonstrate microphone capture, local transcription, and editable text in the
   quick bar on macOS and Linux. Investigate WSLg with actual microphone input.
2. Exercise startup, stop, cancel, permission denial, missing dependencies, and
   device loss. Check ordinary exits and unexpected parent termination; record
   what happens to the microphone and temporary audio.
3. Measure recording readiness, cold stop-to-text latency, memory, transcript
   quality, silence behavior, and fresh-machine setup effort. Record hardware,
   executable versions, model and language settings with the measurements.
4. Verify the proposed shortcut and mouse interaction in a real terminal and
   tmux. Confirm that a transcript populates a draft and never sends it.
5. Publish the demonstrated platform combinations and unresolved failures, then
   make an explicit production decision. Agree on acceptable latency using the
   measurements; no numerical performance target has been validated yet.

Proceed only when capture and cancellation are dependable, transcription is
useful for the target users, setup is workable, and the outstanding platform
scope is understood. A working happy path alone is insufficient.

## Production implementation after the spike

Create a small `internal/dictation` package that owns child processes, audio,
transcription and cleanup, with no UI dependency. Keep process and filesystem
work in asynchronous commands. Evaluate remote-terminal policy outside the
audio package so it does not acquire a tmux dependency.

The proposed capture contract is bounded 16 kHz mono signed-16-bit PCM from
FFmpeg, with WAV finalization owned by the package. This avoids relying on a
recorder to flush a file header after termination. Establish readiness from
actual samples, keep diagnostics separate and bounded, and read a dedicated
Whisper transcript file only after successful completion. Use argument vectors,
finite recording limits, inference timeouts, and explicit child reaping. Remove
temporary audio after use and define recovery for artifacts left by a crash.

The UI should make starting, recording, stopping, transcribing, pending overflow,
and failure visible. Record/Stop and Cancel need both mouse and keyboard actions
plus footer/help entries. Esc during a job cancels it while preserving the draft;
a subsequent Esc closes the bar. Block submission while dictation is unfinished.
Initially append completed text with appropriate spacing, preserving typed edits;
insertion at a reserved caret position is an alternative to evaluate in the spike.

Apply results only when composer identity and job ID match. Cancel on every
dismissal path, including mouse navigation, and invalidate work when the draft
is cleared. Stop children before the [self-update exec](../main.go); defers alone
do not run when `syscall.Exec` succeeds.

Require installed recorder/transcriber executables initially and expose their
availability and model selection through Settings. Persist choices through
manager-owned configuration. Use the OS default microphone, actionable dependency
hints, and documented upstream interfaces. Avoid hardcoded model catalogs,
guessed installation paths, arbitrary shell-command settings, and new environment
variables. Model acquisition may require a download; subsequent transcription
must work offline. Bundling weights and automatic installation are additional
distribution work, not prerequisites for the spike.

Keep agent prompt delivery unchanged, including shell-target rejection. Verify
the implementation across the tools in `builtinTools`, the release platforms
plus WSL2, keyboard and mouse, and the terminal/tmux matrix. Cover cancellation
before readiness, rapid toggles, timeouts, stale close/reopen/send results,
Unicode, overflow, silence, cleanup and remote refusal with meaningful automated
tests. Follow [the repository checks](../.github/CONTRIBUTING.md#checks) and capture
real TUI sessions on isolated sockets; passing tests alone proves no microphone
or platform behavior.

## Planning allowance

Allow roughly 3–5 engineering days for the spike with representative hardware
available, then approximately 2–3 weeks for a production quick-bar implementation
and validation after a positive decision. A bundled native helper was assessed
at roughly 3–5 engineer-weeks; an integrated client companion at 4–8 weeks.
These are preliminary engineering judgments, not prototype-derived estimates.
Re-estimate after capture, setup and latency findings, especially if the accepted
scope includes Windows interop, additional recorder backends, or SSH control.

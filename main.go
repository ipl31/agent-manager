package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/YoanWai/agent-manager/internal/app"
	"github.com/YoanWai/agent-manager/internal/catalog"
	"github.com/YoanWai/agent-manager/internal/cli"
	"github.com/YoanWai/agent-manager/internal/config"
	"github.com/YoanWai/agent-manager/internal/hooks"
	"github.com/YoanWai/agent-manager/internal/mcpserver"
	"github.com/YoanWai/agent-manager/internal/notify"
	"github.com/YoanWai/agent-manager/internal/sessioncmd"
	"github.com/YoanWai/agent-manager/internal/systheme"
	"github.com/YoanWai/agent-manager/internal/tmux"
	"github.com/YoanWai/agent-manager/internal/ui"
	"github.com/YoanWai/agent-manager/internal/update"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/term"
)

const devVersion = "dev"

var version = devVersion

// A packager building from source can mark the binary as externally
// managed with -X main.buildSource=Homebrew, which disables self-update.
// No official channel sets it; released binaries rely on the install-path
// check in internal/update instead.
var buildSource = ""

// resolveVersion falls back to the module version so `go install` builds, which
// carry no ldflags, report the tag they came from. Pseudo-versions stay "dev":
// they name a commit rather than a release, and the update check compares
// against release tags.
func resolveVersion(embedded string, info *debug.BuildInfo, ok bool) string {
	if embedded != devVersion {
		return strings.TrimPrefix(embedded, "v")
	}
	if !ok || info == nil {
		return devVersion
	}
	if !update.IsRelease(info.Main.Version) {
		return devVersion
	}
	return strings.TrimPrefix(info.Main.Version, "v")
}

func main() {
	info, hasInfo := debug.ReadBuildInfo()
	version = resolveVersion(version, info, hasInfo)
	update.SetBuildSource(buildSource)

	// The macOS notifier bundle runs a copy of this binary; a click on a
	// banner relaunches it with no arguments at all.
	if notify.LaunchedAsHelper() {
		os.Exit(notify.HelperMain(os.Args[1:]))
	}

	if len(os.Args) > 1 {
		if os.Args[1] == "help" || os.Args[1] == "--help" || os.Args[1] == "-h" {
			dir, err := config.Dir()
			if err == nil {
				err = printHelp(os.Stdout, dir)
			}
			if err != nil {
				fmt.Fprintln(os.Stderr, "agent-manager:", err)
				os.Exit(1)
			}
			return
		}
		if os.Args[1] == "--version" || os.Args[1] == "-v" {
			fmt.Println("agent-manager", version)
			return
		}
		if command, ok := subcommands()[os.Args[1]]; ok {
			if err := command(os.Args[2:]); err != nil {
				// A subcommand's -h has already printed its usage, and
				// asking for it is not a failure.
				if errors.Is(err, cli.ErrUsageShown) {
					return
				}
				fmt.Fprintln(os.Stderr, "agent-manager:", err)
				os.Exit(cli.ExitCode(err))
			}
			return
		}
	}
	// A package validator and a script start the binary with no arguments
	// and at least one stream redirected. There is no TUI to open, and
	// probing the multiplexer fails the process on a machine that does
	// not have one. Say so on stderr, print usage on stdout, and
	// succeed; a real terminal still falls through to run.
	if len(os.Args) == 1 && !stdioIsTerminal() {
		fmt.Fprintln(os.Stderr, "agent-manager: no terminal attached; run it in a terminal to open the manager")
		dir, err := config.Dir()
		if err == nil {
			err = printHelp(os.Stdout, dir)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, "agent-manager:", err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "agent-manager:", err)
		os.Exit(1)
	}
}

// stdioIsTerminal reports whether both standard streams are terminals.
// Either one being a pipe or a file is a non-interactive start.
func stdioIsTerminal() bool {
	return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd())
}

func printHelp(w io.Writer, configDir string) error {
	proactive, err := sessioncmd.ProactiveCoordination(configDir)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, cli.Help(version, proactive))
	return err
}

func subcommands() map[string]func(args []string) error {
	table := map[string]func(args []string) error{
		"mcp": withConfigDir(func(args []string, caller func() string, configDir string) error {
			return withBackend(configDir, func(backend *sessioncmd.Backend) error {
				return mcpserver.RunWithBackend(configDir, caller(), version, backend)
			})
		}),
		"serve": func(args []string) error {
			dir, err := config.Dir()
			if err != nil {
				return err
			}
			return runServe(os.Stdout, os.Stderr, args, dir, app.StartDetached)
		},
		"track-conversation": withConfigDir(func(args []string, _ func() string, configDir string) error {
			return sessioncmd.TrackConversation(configDir, args, os.Stdin)
		}),
		"follow-telemetry": withConfigDir(func(args []string, _ func() string, configDir string) error {
			return sessioncmd.FollowTelemetry(configDir, args)
		}),
		// An attached ← runs this on every press and reads its exit status.
		sessioncmd.PromptHeadCommand: withConfigDir(func(args []string, _ func() string, configDir string) error {
			driver, err := tmux.New()
			if err != nil {
				return err
			}
			return app.PromptHead(configDir, driver, args)
		}),
	}
	for name := range cli.Commands(version, ui.SettingSpecs) {
		table[name] = withConfigDir(func(args []string, caller func() string, configDir string) error {
			return withBackend(configDir, func(backend *sessioncmd.Backend) error {
				return cli.CommandsWithBackend(version, ui.SettingSpecs, backend)[name](args, caller, configDir)
			})
		})
	}
	return table
}

const usageServe = "serve [--background]"

// runServe runs a manager with no TUI, for a host reached over SSH where
// nobody keeps one open: it delivers queued messages, tracks status and
// carries out after-turn archives and kills.
func runServe(out, errs io.Writer, args []string, dir string, start app.Starter) error {
	set := flag.NewFlagSet(usageServe, flag.ContinueOnError)
	set.SetOutput(io.Discard)
	background := set.Bool("background", false, "start a detached manager unless one is already running, print {\"started\":bool,\"pid\":N} and return at once")
	if err := set.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(out, "usage: agent-manager "+usageServe)
			set.SetOutput(out)
			set.PrintDefaults()
			return cli.ErrUsageShown
		}
		return fmt.Errorf("%w; usage: agent-manager %s", err, usageServe)
	}
	if set.NArg() != 0 {
		return fmt.Errorf("usage: agent-manager %s", usageServe)
	}
	if *background {
		started, err := app.ServeBackground(dir, time.Now(), start)
		if err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(started)
	}
	release, acquired, err := app.LockServe(dir)
	if err != nil {
		return err
	}
	if !acquired {
		fmt.Fprintln(out, "agent-manager serve: another serve is already running for this profile")
		return nil
	}
	defer release()
	driver, err := tmux.New()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	owner, err := app.AcquireOwnership(ctx, dir, driver.SocketPath(), false, nil)
	if errors.Is(err, app.ErrManagerBusy) {
		fmt.Fprintln(out, "agent-manager serve:", err)
		return nil
	}
	if err != nil {
		return err
	}
	defer owner.Close()
	local, err := app.OpenLocal(dir, driver)
	if err != nil {
		return err
	}
	monitorCtx, cancelMonitor := context.WithCancel(ctx)
	changed := make(chan error, 1)
	go func() { err := owner.Changed(monitorCtx); changed <- err; stop() }()
	local.Serve(ctx, errs)
	cancelMonitor()
	if reason := <-changed; reason != nil && !errors.Is(reason, context.Canceled) {
		fmt.Fprintln(out, "agent-manager serve:", reason)
	}
	return local.Close()
}

func withBackend(configDir string, command func(*sessioncmd.Backend) error) (err error) {
	backend := sessioncmd.OpenBackend(configDir)
	defer func() { err = errors.Join(err, backend.Close()) }()
	return command(backend)
}

func withConfigDir(command cli.Command) func([]string) error {
	return func(args []string) error {
		dir, err := config.Dir()
		if err != nil {
			return err
		}
		return command(args, sync.OnceValue(func() string { return callerSession(dir) }), dir)
	}
}

// callerSession identifies the session a subcommand speaks for. The
// process ancestry decides whenever it can see the tmux server: the
// environment is inherited by whatever a pane starts, and a process that
// outlives its pane carries it on. Claude Code's daemon, started from one
// session, serves every other session's conversations with that session's
// id and pane. The ancestry also names the caller of an MCP server Muse or
// Cursor starts with none of the pane's environment.
//
// A process no managed pane started speaks for the session that owns its
// conversation: a conversation the manager launched keeps its session once
// /background or /fork moves it to the daemon. One nobody launched, such
// as a claude started by hand in a session's shell, has no owner, and the
// process is no session's caller, whatever its environment says.
//
// Inside a sandbox's PID namespace the ancestry is out of sight. The
// conversation's owner still answers when there is one, and otherwise the
// environment variable: an MCP server launched under Codex is handed that
// variable and nothing else of the pane's environment.
//
// Falling back to the pane is what makes a terminal a caller there. A
// terminal opens on the user's shell with no launch command, so tmux gets
// no launch script to export the id from, and the shell would otherwise
// have no way to say which session it is.
func callerSession(configDir string) string {
	id, seen := callerAncestry()
	if id != "" {
		return id
	}
	if owner := callerConversationOwner(configDir); owner != "" || seen {
		return owner
	}
	if id := os.Getenv(hooks.EnvSessionID); id != "" {
		return id
	}
	return sessionFromPane()
}

// callerConversationOwner is sessioncmd.ConversationOwner, swapped out by
// the tests that check the order the ways to identify a caller go in.
var callerConversationOwner = sessioncmd.ConversationOwner

// sessionFromPane asks the manager's tmux server which session owns the
// pane this command runs in. Every failure answers empty: `update` and the
// rest have to keep working outside tmux, and on a machine without tmux
// installed at all.
func sessionFromPane() string {
	tmuxEnv, pane := os.Getenv("TMUX"), os.Getenv("TMUX_PANE")
	if tmuxEnv == "" || pane == "" {
		return ""
	}
	driver, err := tmux.New()
	if err != nil {
		return ""
	}
	id, err := driver.SessionOfPane(tmuxEnv, pane)
	if err != nil {
		return ""
	}
	return id
}

// callerAncestry is sessionFromAncestry, swapped out by the tests that need
// the ancestry hidden the way a PID namespace hides it.
var callerAncestry = sessionFromAncestry

// sessionFromAncestry names the session whose pane started this process.
// seen is false when the ancestry cannot be read, including every failure,
// so the other ways to identify the caller still get their turn.
func sessionFromAncestry() (id string, seen bool) {
	driver, err := tmux.New()
	if err != nil {
		return "", false
	}
	id, seen, err = driver.SessionOfProcess(os.Getpid(), os.Getppid())
	if err != nil {
		return "", false
	}
	return id, seen
}

// programFilter resets the pointer while the screen that showed the hand is
// still up, since kitty keeps one per screen and an interrupt skips Update,
// and reads the modified keys and scheme reports Bubble Tea leaves
// undecoded.
func programFilter(_ tea.Model, msg tea.Msg) tea.Msg {
	switch msg.(type) {
	case tea.QuitMsg, tea.InterruptMsg:
		ui.ResetPointer()
	}
	return ui.ReadColorScheme(ui.ReadExtendedKey(msg))
}

func run() (resultErr error) {
	dir, err := config.Dir()
	if err != nil {
		return err
	}
	driver, err := tmux.New()
	if err != nil {
		return err
	}
	continuation, err := readContinuation(os.Args[1:])
	if err != nil {
		return err
	}
	owner, err := app.AcquireOwnership(context.Background(), dir, driver.SocketPath(), true, continuation)
	if err != nil {
		return err
	}
	releaseOwner := true
	defer func() {
		if releaseOwner {
			owner.Close()
		}
	}()
	local, err := app.OpenLocal(dir, driver)
	if err != nil {
		return err
	}
	closeLocal := true
	defer func() {
		if closeLocal {
			resultErr = errors.Join(resultErr, local.Close())
		}
	}()
	rt := local.Runtime
	model, err := ui.NewWithServices(ui.Dependencies{
		Config: rt.Config, Store: rt.Store, TMux: rt.Driver, Engine: local.Engine, Hooks: rt.Hooks,
		Git: rt.Git, Lifecycle: local.Lifecycle, Execution: local.Execution, ProfileDir: dir,
	}, version)
	if err != nil {
		return err
	}

	model.SetOwnerID(owner.ID())

	// Mouse reporting claims the wheel for the app, so a notch neither
	// scrolls the host's scrollback out from under the manager nor arrives
	// as an arrow key that walks the session cursor. Alternate scroll is
	// cleared too: a crashed earlier run can leave it set.
	program := tea.NewProgram(model,
		tea.WithAltScreen(),
		tea.WithFPS(120),
		tea.WithMouseCellMotion(),
		tea.WithOutput(model.CursorOutput(os.Stdout)),
		tea.WithFilter(programFilter),
	)
	if err := ui.DisableAlternateScroll(); err != nil {
		return err
	}
	ui.SetHostPalette(systheme.QueryPalette())
	if err := ui.EnableColorSchemeReports(); err != nil {
		return err
	}
	// The terminal's own text and background colors follow the theme while
	// the manager runs, so window padding outside the cell grid matches the
	// frame — through tmux's passthrough envelope when a multiplexer is
	// hosting us.
	ui.EnableTerminalPassthrough()
	ui.SyncTerminalColors()
	ctx, cancel := context.WithCancel(context.Background())
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		reason := owner.Changed(ctx)
		if ctx.Err() == nil {
			program.Send(ui.TakeoverMsg{Owner: owner.ID(), Reason: reason})
		}
	}()
	done := model.StartPoller(ctx, program.Send)
	final, runErr := program.Run()
	ui.ResetTerminalColors()
	ui.ResetPointer()
	runErr = errors.Join(runErr, ui.DisableColorSchemeReports())
	cancel()
	model.CloseRuntimeAdmission()
	shutdown := startRuntimeShutdown(func(shutdownCtx context.Context) error {
		catalogErr := catalog.Close(shutdownCtx)
		modelErr := model.ConfirmShutdown(shutdownCtx)
		controlErr := driver.CloseControls(shutdownCtx)
		<-done
		<-monitorDone
		return errors.Join(catalogErr, modelErr, controlErr)
	})
	forceCtx, stopForce := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopForce()
	forced := awaitRuntimeShutdown(shutdown.done, forceCtx.Done(), model.ForcedQuit() || errors.Is(runErr, tea.ErrInterrupted), 5*time.Second, func() {
		reason := "quitting"
		if model.TakenOver() {
			reason = "takeover"
		} else if model.RestartPath() != "" {
			reason = "restarting"
		}
		fmt.Fprintf(os.Stderr, "agent-manager: waiting for active work to finish before %s; Ctrl+C forces exit\n", reason)
	})
	if forced {
		// Even an explicit force never authorizes self-restart. A child
		// may outlive this process; this is not a clean handoff claim.
		select {
		case <-shutdown.done:
		default:
			closeLocal = false
			releaseOwner = false
			owner.RetainUntilExit(local, model, program, shutdown)
			runErr = errors.Join(runErr, shutdown.uncertainty(), errors.New("forced exit before active work finished; check its result"))
		}
	}

	if abandoned := model.AbandonedEffects(); len(abandoned) > 0 {
		runErr = errors.Join(runErr, fmt.Errorf("quit before these finished, so check their result: %s", strings.Join(abandoned, "; ")))
	}
	if model.TakenOver() {
		reason := model.TakeoverReason()
		if reason == nil {
			reason = app.ErrManagerSuperseded
		}
		fmt.Fprintln(os.Stderr, "agent-manager:", reason)
		if !errors.Is(reason, app.ErrManagerSuperseded) {
			runErr = errors.Join(runErr, reason)
		}
		return runErr
	}
	if runErr == nil && !forced {
		if finished, ok := final.(*ui.Model); ok && finished.RestartPath() != "" {
			request, err := owner.ReserveContinuation(context.Background())
			if errors.Is(err, app.ErrManagerSuperseded) {
				fmt.Fprintln(os.Stderr, "agent-manager: another manager took over this profile and server")
				return nil
			}
			if err != nil {
				return err
			}
			if err := local.Close(); err != nil {
				return err
			}
			closeLocal = false
			owner.ReleaseContinuation()
			releaseOwner = false
			return restart(finished.RestartPath(), continuationArgs(request))
		}
	}
	return runErr
}

// tui-check runs the fast CLI contract tests, bounded fuzz discovery, or
// captures the startup screens of installed agents on an isolated tmux socket.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/YoanWai/agent-manager/internal/config"
	"github.com/YoanWai/agent-manager/internal/status"
	"github.com/YoanWai/agent-manager/internal/tmux"
)

type liveResult struct {
	Tool      string `json:"tool"`
	Version   string `json:"version,omitempty"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Status    string `json:"status"`
	Matched   bool   `json:"matched"`
	Hold      string `json:"typing_hold,omitempty"`
	Ready     bool   `json:"composer_ready"`
	Outcome   string `json:"outcome"`
	Capture   string `json:"capture"`
	StartedAt string `json:"started_at"`
}

func main() {
	var goBinary, selected, output string
	var currentHome bool
	var seconds int
	flag.StringVar(&goBinary, "go", "go", "Go executable for quick and fuzz modes")
	flag.StringVar(&selected, "tools", "codex,claude,grok,muse,opencode", "comma-separated tools for live mode")
	flag.StringVar(&output, "output", "", "capture directory for live mode (default: temporary directory)")
	flag.BoolVar(&currentHome, "current-home", false, "use current CLI accounts and settings in live mode")
	flag.IntVar(&seconds, "seconds", 5, "discovery time for each fuzz target")
	flag.Parse()
	if flag.NArg() != 1 {
		fatalf("usage: tui-check [flags] quick|fuzz|live")
	}
	var err error
	switch flag.Arg(0) {
	case "quick":
		err = runGo(goBinary, "test", "./internal/status", "./internal/tmux", "./internal/ui", "./internal/agentsession", "./internal/sessioncmd", "-run", "^TestPaneCorpus$|^TestPasteKeepsBytesAndTargetsPaneZero$|^TestFocusKeyCommand$|^TestMouseReportEncodings$|^TestFocusPasteKeepsPromptInComposer$|^TestPendingInputLandsOnAnErroredPane$|^TestCaptureAgentSessionIDs|^TestSnapshotRelaunch|^FuzzPane|^FuzzFocusPreviewTrace$|^FuzzFocusKeyBytes$|^FuzzSessionCandidates$|^FuzzOpencodeExportParser$")
	case "fuzz":
		for _, target := range []struct{ pkg, name string }{
			{"./internal/status", "FuzzPaneDraftIsolation"},
			{"./internal/status", "FuzzPaneRobustness"},
			{"./internal/ui", "FuzzFocusPreviewTrace"},
			{"./internal/ui", "FuzzFocusKeyBytes"},
			{"./internal/agentsession", "FuzzSessionCandidates"},
			{"./internal/agentsession", "FuzzOpencodeExportParser"},
		} {
			if err = runGo(goBinary, "test", target.pkg, "-run", "^$", "-fuzz", "^"+target.name+"$", "-fuzztime", fmt.Sprintf("%ds", seconds), "-parallel", "2"); err != nil {
				break
			}
		}
	case "live":
		err = runLive(selected, output, currentHome)
	default:
		fatalf("unknown mode %q", flag.Arg(0))
	}
	if err != nil {
		fatalf("%v", err)
	}
}

func runGo(binary string, args ...string) error {
	socketDir, err := os.MkdirTemp("/tmp", "amtest-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(socketDir)
	cmd := exec.Command(binary, args...)
	for _, variable := range os.Environ() {
		if !strings.HasPrefix(variable, "TMUX=") && !strings.HasPrefix(variable, "TMUX_TMPDIR=") {
			cmd.Env = append(cmd.Env, variable)
		}
	}
	cmd.Env = append(cmd.Env, "TMUX_TMPDIR="+socketDir)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func runLive(selected, output string, currentHome bool) error {
	if !currentHome {
		kept := map[string]string{}
		for _, key := range []string{"PATH", "TERM", "LANG", "LC_ALL", "USER", "LOGNAME", "SHELL"} {
			if value, ok := os.LookupEnv(key); ok {
				kept[key] = value
			}
		}
		os.Clearenv()
		for key, value := range kept {
			os.Setenv(key, value)
		}
	}
	socketDir, err := os.MkdirTemp("/tmp", "amcheck-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(socketDir)
	oldSocketDir, hadSocketDir := os.LookupEnv("TMUX_TMPDIR")
	os.Unsetenv("TMUX")
	os.Setenv("TMUX_TMPDIR", socketDir)
	defer func() {
		if hadSocketDir {
			os.Setenv("TMUX_TMPDIR", oldSocketDir)
		} else {
			os.Unsetenv("TMUX_TMPDIR")
		}
	}()
	cfg, err := config.Default()
	if err != nil {
		return err
	}
	engine, err := status.NewEngine(cfg)
	if err != nil {
		return err
	}
	if output == "" {
		output, err = os.MkdirTemp("", "am-tui-check-")
		if err != nil {
			return err
		}
	} else if err := os.MkdirAll(output, 0o700); err != nil {
		return err
	}
	root, err := filepath.Abs(output)
	if err != nil {
		return err
	}
	if !currentHome {
		os.Setenv("HOME", root)
	}
	fmt.Printf("captures: %s\n", root)
	// A short socket name keeps the full tmux socket path below its limit.
	driver, err := tmux.NewWithSocket(fmt.Sprintf("amchk%x", os.Getpid()))
	if err != nil {
		return err
	}
	var failed []string
	for _, name := range strings.Split(selected, ",") {
		name = strings.TrimSpace(name)
		tool, exists := cfg.Tools[name]
		if !exists || tool.Shell || tool.Command == "" {
			failed = append(failed, name+": unknown CLI")
			continue
		}
		words := strings.Fields(tool.Command)
		binary, err := exec.LookPath(words[0])
		if err != nil {
			failed = append(failed, name+": executable missing")
			continue
		}
		version := readVersion(binary)
		home := filepath.Join(root, name+"-home")
		cwd := filepath.Join(root, name+"-cwd")
		env := map[string]string{}
		if !currentHome {
			if err := os.MkdirAll(home, 0o700); err != nil {
				return err
			}
			env = map[string]string{"HOME": home, "XDG_CONFIG_HOME": filepath.Join(home, ".config"), "XDG_DATA_HOME": filepath.Join(home, ".local")}
		}
		if err := os.MkdirAll(cwd, 0o700); err != nil {
			return err
		}
		command := tmux.ShellQuote(binary)
		if len(words) > 1 {
			command += " " + strings.Join(words[1:], " ")
		}
		id := "check-" + name
		if err := driver.Create(id, cwd, command, env, 100, 30); err != nil {
			_ = driver.Kill(id)
			failed = append(failed, name+": launch: "+err.Error())
			continue
		}
		active := id
		defer func() {
			if active != "" {
				_ = driver.Kill(active)
			}
		}()
		for _, size := range [][2]int{{100, 30}, {60, 20}} {
			if size[0] != 100 {
				if err := driver.Resize(id, size[0], size[1]); err != nil {
					failed = append(failed, name+": resize: "+err.Error())
					break
				}
			}
			pane, err := awaitPane(driver, id, name, engine, 8*time.Second)
			if err != nil {
				failed = append(failed, name+": capture: "+err.Error())
				break
			}
			base := fmt.Sprintf("%s-%dx%d", name, size[0], size[1])
			capture := base + ".ansi"
			if err := os.WriteFile(filepath.Join(root, capture), []byte(pane), 0o600); err != nil {
				failed = append(failed, name+": save: "+err.Error())
				break
			}
			plain := engine.Plain(name, pane)
			state, matched := engine.Match(name, plain)
			_, ready := engine.ActivityRegion(name, plain)
			hold := engine.TypingHold(name, plain)
			outcome := "unclassified"
			if ready && hold == "" {
				outcome = "composer"
			} else if matched && state == status.Waiting {
				outcome = "dialog"
			}
			record := liveResult{Tool: name, Version: version, Width: size[0], Height: size[1], Status: state, Matched: matched, Hold: hold, Ready: ready, Outcome: outcome, Capture: capture, StartedAt: time.Now().UTC().Format(time.RFC3339)}
			data, err := json.MarshalIndent(record, "", "  ")
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(root, base+".json"), append(data, '\n'), 0o600); err != nil {
				return err
			}
			fmt.Printf("%-13s %3dx%-3d %-8s outcome=%s hold=%s\n", name, size[0], size[1], state, outcome, hold)
			if outcome == "unclassified" {
				failed = append(failed, name+": "+base+" unclassified startup screen")
			}
		}
		if err := driver.Kill(id); err != nil {
			failed = append(failed, name+": cleanup: "+err.Error())
		} else {
			active = ""
		}
	}
	if len(failed) != 0 {
		return errors.New(strings.Join(failed, "\n"))
	}
	return nil
}

func awaitPane(driver *tmux.Driver, id, tool string, engine *status.Engine, limit time.Duration) (string, error) {
	deadline := time.Now().Add(limit)
	var last string
	for time.Now().Before(deadline) {
		pane, err := driver.CapturePane(id)
		if err != nil {
			return "", err
		}
		plain := engine.Plain(tool, pane)
		state, matched := engine.Match(tool, plain)
		_, ready := engine.ActivityRegion(tool, plain)
		if strings.TrimSpace(pane) != "" && (ready || matched && state == status.Waiting) && pane == last {
			return pane, nil
		}
		last = pane
		time.Sleep(150 * time.Millisecond)
	}
	if strings.TrimSpace(last) == "" {
		return "", errors.New("no screen drawn before deadline")
	}
	return last, nil
}

func readVersion(binary string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

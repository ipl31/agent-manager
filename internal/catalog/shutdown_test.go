package catalog

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestCloseHelper(t *testing.T) {
	mode := ""
	for _, arg := range os.Args {
		if arg == "catalog-idle" || arg == "catalog-close" {
			mode = arg
		}
	}
	if mode == "" {
		return
	}
	if mode == "catalog-idle" {
		fmt.Println("ready")
		bufio.NewScanner(os.Stdin).Scan()
		os.Exit(0)
	}
	child, err := startArgs(os.Args[0], []string{"-test.run=^TestCloseHelper$", "--", "catalog-idle"}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := child.next(ctx); err != nil {
		t.Fatal(err)
	}
	if err := Close(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-child.exited:
	default:
		t.Fatal("close returned before reap")
	}
	if late, err := startArgs(os.Args[0], []string{"-test.run=^TestCloseHelper$", "--", "catalog-idle"}, "", false); err == nil {
		late.stop()
		t.Fatal("late catalog child started after shutdown snapshot")
	}
}

func TestCloseReapsChildrenAndRejectsLateStartsInSeparateProcess(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestCloseHelper$", "--", "catalog-close")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("catalog shutdown helper: %v\n%s", err, out)
	}
}

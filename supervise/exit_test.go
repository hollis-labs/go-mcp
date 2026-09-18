package supervise

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestClassifyExitClean(t *testing.T) {
	cmd := exec.Command("true")
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	at := time.Now().UTC()
	exit := ClassifyExit(cmd.ProcessState, at)
	if exit.Kind != Clean {
		t.Errorf("Kind = %q, want %q", exit.Kind, Clean)
	}
	if exit.Code != 0 {
		t.Errorf("Code = %d, want 0", exit.Code)
	}
	if exit.Signal != "" {
		t.Errorf("Signal = %q, want empty", exit.Signal)
	}
	if !exit.At.Equal(at) {
		t.Errorf("At = %v, want %v", exit.At, at)
	}
}

func TestClassifyExitError(t *testing.T) {
	cmd := exec.Command("false")
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected a nonzero-exit error")
	}
	exit := ClassifyExit(cmd.ProcessState, time.Now().UTC())
	if exit.Kind != Error {
		t.Errorf("Kind = %q, want %q", exit.Kind, Error)
	}
	if exit.Code != 1 {
		t.Errorf("Code = %d, want 1", exit.Code)
	}
}

func TestClassifyExitSignal(t *testing.T) {
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal: %v", err)
	}
	_ = cmd.Wait()
	exit := ClassifyExit(cmd.ProcessState, time.Now().UTC())
	if exit.Kind != Signal {
		t.Fatalf("Kind = %q, want %q", exit.Kind, Signal)
	}
	if exit.Signal != "terminated" {
		t.Errorf("Signal = %q, want %q", exit.Signal, "terminated")
	}
}

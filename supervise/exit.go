package supervise

import (
	"os"
	"syscall"
	"time"
)

// Kind classifies how a supervised process ended.
type Kind string

const (
	// Clean means the process exited with status 0.
	Clean Kind = "clean"
	// Error means the process exited with a nonzero status.
	Error Kind = "error"
	// Signal means the process was terminated by a signal.
	Signal Kind = "signal"
)

// Exit is the observed outcome of a supervised process, classified from its
// terminal os.ProcessState.
type Exit struct {
	Kind   Kind      `json:"kind"`
	Code   int       `json:"code"`
	Signal string    `json:"signal,omitempty"`
	At     time.Time `json:"at"`
}

// ClassifyExit builds an Exit from a process's terminal os.ProcessState,
// captured immediately after (*os.Process).Wait or (*exec.Cmd).Wait returns,
// and the time that observation was made. It does not call Wait itself and
// does not touch the process -- the caller owns spawning, waiting, and
// deciding what to do next; this only interprets the state Wait already
// produced.
//
// On a platform where ProcessState.Sys() does not report a syscall.WaitStatus
// (notably non-Unix), a signaled exit is reported as Error rather than
// Signal -- the exit code is still correct, only the signal detail is
// unavailable there.
func ClassifyExit(state *os.ProcessState, at time.Time) Exit {
	exit := Exit{Kind: Error, Code: state.ExitCode(), At: at}
	if state.Success() {
		exit.Kind = Clean
	}
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		exit.Kind = Signal
		exit.Signal = status.Signal().String()
	}
	return exit
}

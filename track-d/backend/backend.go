// Package backend defines the seam between the manager UI and the underlying
// Crafting/Claude infrastructure. Both tracks (C: tmux layout, D: bubbleterm)
// share this interface byte-for-byte so the comparison stays apples-to-apples
// and a winning renderer can drop onto either backend later.
package backend

import (
	"context"
	"io"
	"time"
)

// Session is one Claude Code background session living inside a Crafting
// sandbox. It is keyed by (Sandbox, ID); ID is Claude's short id (the
// directory name under ~/.claude/jobs/).
type Session struct {
	Sandbox string
	ID      string
	Name    string
	State   string // working | needs-input | idle | completed | failed | stopped
	PR      string
	Age     time.Duration
}

// Backend is the only seam between the local UI and Crafting. The live
// implementation shells out to `cs` and `claude`; the mock fakes everything so
// the UI runs and demos without any Crafting access.
type Backend interface {
	// List returns every session across every (non-suspended) sandbox,
	// flattened. Implementations should poll per-sandbox `claude agents
	// --json` concurrently and cache the result. Suspended sandboxes may
	// be returned with their last-known sessions (the UI dims them).
	List(ctx context.Context) ([]Session, error)

	// Attach returns a PTY-backed bidirectional stream for the given
	// session. Closing the stream detaches but leaves the session
	// running in the supervisor.
	//
	// Track C consumes this differently: it respawns a tmux pane to
	// `cs ssh -t <sandbox> -- claude attach <id>` rather than reading
	// the stream itself, but the interface is identical so the seam is
	// still swappable.
	Attach(ctx context.Context, sandbox, id string) (io.ReadWriteCloser, error)

	// Dispatch starts a new background session in the given sandbox and
	// returns its session id. Equivalent to:
	//   cs ssh <sandbox> -- claude --bg --name "<name>" "<prompt>"
	Dispatch(ctx context.Context, sandbox, name, prompt string) (id string, err error)

	// Stop terminates a running session via `claude stop <id>`.
	Stop(ctx context.Context, sandbox, id string) error

	// Respawn restarts stopped sessions in a sandbox via
	// `claude respawn --all`. Typically called by the workspace
	// on_resume hook, but exposed here for parity and dev use.
	Respawn(ctx context.Context, sandbox string) error
}

// Session state constants. Values mirror what `claude agents --json` is
// expected to emit; confirm against real output during VERIFY.
const (
	StateWorking    = "working"
	StateNeedsInput = "needs-input"
	StateIdle       = "idle"
	StateCompleted  = "completed"
	StateFailed     = "failed"
	StateStopped    = "stopped"
)

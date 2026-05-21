// Package crafting is the live Backend implementation. It shells out
// to `cs` and `claude` per the spec. It is intentionally a partial
// stub overnight; the gaps are listed in NOTES.md.
//
// Wire format assumptions (call out in VERIFY.md):
//   - `cs list --json` enumerates sandboxes with at least an Id/Name
//     and a State field.
//   - `cs ssh <sandbox> -- claude agents --json` returns
//     {sessions: [{id, name, state, pr, age_seconds}, ...]} per
//     sandbox.
//   - `cs ssh -t <sandbox> -- claude attach <id>` is the interactive
//     entrypoint we wrap in a local PTY.
//
// All four are spec-D open questions, not verified shapes; the JSON
// decode struct is best-effort and lenient.
package crafting

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"

	"agentmgr/backend"
)

// Backend is the live implementation.
type Backend struct {
	// poolSize bounds concurrent per-sandbox polls so we don't fan out
	// to dozens of `cs ssh` invocations at once. 4 is a reasonable
	// default; raise it once we have real fan-out numbers.
	poolSize int

	mu    sync.Mutex
	cache []backend.Session
}

// New returns a CraftingBackend with sensible defaults.
func New() *Backend {
	return &Backend{poolSize: 4}
}

// csListEntry is the lenient decode of `cs list --json`. Fields are
// optional because we have not verified the exact shape; only Id
// matters here.
type csListEntry struct {
	Id    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
}

// claudeAgentsResponse is the lenient decode of
// `claude agents --json`.
type claudeAgentsResponse struct {
	Sessions []claudeSession `json:"sessions"`
}

type claudeSession struct {
	Id         string  `json:"id"`
	Name       string  `json:"name"`
	State      string  `json:"state"`
	PR         string  `json:"pr"`
	AgeSeconds float64 `json:"age_seconds"`
}

// List enumerates every running sandbox and aggregates its sessions
// via concurrent `cs ssh ... claude agents --json` calls.
func (b *Backend) List(ctx context.Context) ([]backend.Session, error) {
	sandboxes, err := b.listSandboxes(ctx)
	if err != nil {
		return nil, fmt.Errorf("cs list: %w", err)
	}

	sem := make(chan struct{}, b.poolSize)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var out []backend.Session

	for _, sb := range sandboxes {
		sb := sb
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			sessions, err := b.listAgents(ctx, sb.Id)
			if err != nil {
				// Don't blow up the whole roster on one bad sandbox;
				// surface as a synthetic placeholder row so it's
				// visible.
				mu.Lock()
				out = append(out, backend.Session{
					Sandbox: sb.Id,
					ID:      "err",
					Name:    "list failed: " + err.Error(),
					State:   backend.StateFailed,
				})
				mu.Unlock()
				return
			}
			mu.Lock()
			for _, s := range sessions {
				out = append(out, backend.Session{
					Sandbox: sb.Id,
					ID:      s.Id,
					Name:    s.Name,
					State:   s.State,
					PR:      s.PR,
					Age:     time.Duration(s.AgeSeconds * float64(time.Second)),
				})
			}
			mu.Unlock()
		}()
	}
	wg.Wait()

	b.mu.Lock()
	b.cache = out
	b.mu.Unlock()
	return out, nil
}

func (b *Backend) listSandboxes(ctx context.Context) ([]csListEntry, error) {
	out, err := run(ctx, "cs", "list", "--json")
	if err != nil {
		return nil, err
	}
	// `cs list --json` shape unverified; try the obvious top-level
	// array first.
	var arr []csListEntry
	if err := json.Unmarshal(out, &arr); err == nil {
		return arr, nil
	}
	// Fall back to {sandboxes: [...]} or {items: [...]}.
	var wrap struct {
		Sandboxes []csListEntry `json:"sandboxes"`
		Items     []csListEntry `json:"items"`
	}
	if err := json.Unmarshal(out, &wrap); err == nil {
		if len(wrap.Sandboxes) > 0 {
			return wrap.Sandboxes, nil
		}
		return wrap.Items, nil
	}
	return nil, fmt.Errorf("cs list: unrecognized JSON shape")
}

func (b *Backend) listAgents(ctx context.Context, sandbox string) ([]claudeSession, error) {
	out, err := run(ctx, "cs", "ssh", sandbox, "--", "claude", "agents", "--json")
	if err != nil {
		return nil, err
	}
	var resp claudeAgentsResponse
	if err := json.Unmarshal(out, &resp); err == nil {
		return resp.Sessions, nil
	}
	// Fall back to a bare array.
	var arr []claudeSession
	if err := json.Unmarshal(out, &arr); err == nil {
		return arr, nil
	}
	return nil, fmt.Errorf("claude agents: unrecognized JSON shape")
}

// Attach starts `cs ssh -t <sandbox> -- claude attach <id>` under a
// local PTY and returns it as a ReadWriteCloser. The local PTY is
// what lets the remote claude attach see a tty and propagate window
// size; without -t the remote command would refuse interactive mode.
//
// The returned value also satisfies a Resize(cols, rows int) error
// method that the UI can type-assert to keep the local PTY winsize
// in lockstep with the embedded emulator.
func (b *Backend) Attach(ctx context.Context, sandbox, id string) (io.ReadWriteCloser, error) {
	cmd := exec.CommandContext(ctx, "cs", "ssh", "-t", sandbox, "--", "claude", "attach", id)
	ptmx, err := pty.Start(cmd)
	if err != nil {
		return nil, fmt.Errorf("pty.Start cs ssh: %w", err)
	}
	// Give the remote tty a reasonable starting size; the UI will
	// resize again on its first WindowSizeMsg.
	_ = pty.Setsize(ptmx, &pty.Winsize{Cols: 120, Rows: 36})
	return &ptyStream{ptmx: ptmx, cmd: cmd}, nil
}

// ptyStream wraps a creack/pty FD and the cs ssh process so closing
// it cleans up both. The UI side just sees an io.ReadWriteCloser.
type ptyStream struct {
	ptmx *os.File
	cmd  *exec.Cmd
}

func (s *ptyStream) Read(p []byte) (int, error)  { return s.ptmx.Read(p) }
func (s *ptyStream) Write(p []byte) (int, error) { return s.ptmx.Write(p) }
func (s *ptyStream) Close() error {
	_ = s.ptmx.Close()
	if s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
		_, _ = s.cmd.Process.Wait()
	}
	return nil
}

// Resize is exposed so the UI can call pty.Setsize without taking on
// the os.File dependency. The UI does want to keep PTY winsize in
// lockstep with the bubbleterm size; type-assert the stream to
// `interface { Resize(cols, rows int) error }` and call it.
func (s *ptyStream) Resize(cols, rows int) error {
	return pty.Setsize(s.ptmx, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

// Ensure ptyStream satisfies io.ReadWriteCloser at compile time.
var _ io.ReadWriteCloser = (*ptyStream)(nil)

// Dispatch shells out to `cs ssh <sandbox> -- claude --bg --name X "prompt"`.
// The supervisor returns the session id on stdout; we try to parse it
// out as the first non-empty line. Verify the exact stdout shape.
func (b *Backend) Dispatch(ctx context.Context, sandbox, name, prompt string) (string, error) {
	out, err := run(ctx, "cs", "ssh", sandbox, "--",
		"claude", "--bg", "--name", name, prompt)
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		return line, nil
	}
	return "", fmt.Errorf("claude --bg returned no id")
}

// Stop shells out to `cs ssh <sandbox> -- claude stop <id>`.
func (b *Backend) Stop(ctx context.Context, sandbox, id string) error {
	_, err := run(ctx, "cs", "ssh", sandbox, "--", "claude", "stop", id)
	return err
}

// Respawn shells out to `cs ssh <sandbox> -- claude respawn --all`.
func (b *Backend) Respawn(ctx context.Context, sandbox string) error {
	_, err := run(ctx, "cs", "ssh", sandbox, "--", "claude", "respawn", "--all")
	return err
}

func run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("%s %s: %w (stderr: %s)",
				name, strings.Join(args, " "), err,
				strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return out, nil
}

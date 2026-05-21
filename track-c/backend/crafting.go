package backend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// CraftingBackend is the live implementation of Backend. It shells out
// to `cs` and `claude`. The exact JSON shape of `cs list` and
// `claude agents --json` is not yet finalized, so the parsing is best-
// effort and flagged as STUB in NOTES.md.
//
// Track C uses Attach() only on the rare paths that want to consume the
// stream in-process (e.g. for tests). The normal sidebar flow respawns a
// tmux pane to `cs ssh -t <sandbox> -- claude attach <id>` directly, so
// Attach() here is provided for interface completeness rather than the
// hot path.
type CraftingBackend struct {
	// PollTimeout caps how long a single `cs ssh ... claude agents --json`
	// invocation may take. A slow or resuming sandbox must not block the
	// whole roster.
	PollTimeout time.Duration

	// Concurrency bounds how many sandboxes are polled in parallel.
	Concurrency int
}

// NewCraftingBackend returns a CraftingBackend with sensible defaults.
func NewCraftingBackend() *CraftingBackend {
	return &CraftingBackend{
		PollTimeout: 5 * time.Second,
		Concurrency: 8,
	}
}

// csList is the (assumed) shape of `cs list --json`. STUB: confirm field
// names against real output during VERIFY.
type csList struct {
	Sandboxes []struct {
		ID    string `json:"id"`
		Name  string `json:"name"`
		State string `json:"state"` // running | suspended | ...
	} `json:"sandboxes"`
}

// claudeAgents is the (assumed) shape of `claude agents --json`. STUB:
// confirm field names against real output during VERIFY. Field names
// here are the spec's best guess.
type claudeAgents struct {
	Sessions []struct {
		ID           string  `json:"id"`
		Name         string  `json:"name"`
		State        string  `json:"state"`
		LastActivity string  `json:"last_activity"` // RFC3339?
		PR           string  `json:"pr,omitempty"`
		AgeSeconds   float64 `json:"age_seconds,omitempty"`
	} `json:"sessions"`
}

// List queries cs for the sandbox list, then concurrently polls
// `claude agents --json` on each running sandbox and flattens the
// results.
func (c *CraftingBackend) List(ctx context.Context) ([]Session, error) {
	sandboxes, err := c.listSandboxes(ctx)
	if err != nil {
		return nil, err
	}

	sem := make(chan struct{}, c.Concurrency)
	var (
		mu   sync.Mutex
		out  []Session
		errs []string
		wg   sync.WaitGroup
	)
	for _, sb := range sandboxes {
		sb := sb
		// Skip suspended sandboxes for live polling. The orchestrator
		// shows their last-known sessions dimmed; for the POC, we
		// just elide them. STUB: cache last-known and dim instead.
		if sb.state != "running" {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			sub, cancel := context.WithTimeout(ctx, c.PollTimeout)
			defer cancel()
			sessions, perr := c.pollSandbox(sub, sb.id)
			mu.Lock()
			defer mu.Unlock()
			if perr != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", sb.id, perr))
				return
			}
			out = append(out, sessions...)
		}()
	}
	wg.Wait()

	if len(out) == 0 && len(errs) > 0 {
		return nil, fmt.Errorf("polling failed: %s", strings.Join(errs, "; "))
	}
	return out, nil
}

type sandboxInfo struct {
	id    string
	name  string
	state string
}

func (c *CraftingBackend) listSandboxes(ctx context.Context) ([]sandboxInfo, error) {
	out, err := runCmd(ctx, "cs", "list", "--json")
	if err != nil {
		return nil, fmt.Errorf("cs list --json: %w", err)
	}
	var raw csList
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse cs list: %w", err)
	}
	res := make([]sandboxInfo, 0, len(raw.Sandboxes))
	for _, s := range raw.Sandboxes {
		res = append(res, sandboxInfo{id: s.ID, name: s.Name, state: s.State})
	}
	return res, nil
}

func (c *CraftingBackend) pollSandbox(ctx context.Context, sandbox string) ([]Session, error) {
	out, err := runCmd(ctx, "cs", "ssh", sandbox, "--", "claude", "agents", "--json")
	if err != nil {
		return nil, err
	}
	var raw claudeAgents
	if err := json.Unmarshal(out, &raw); err != nil {
		return nil, fmt.Errorf("parse claude agents --json: %w", err)
	}
	res := make([]Session, 0, len(raw.Sessions))
	for _, s := range raw.Sessions {
		age := time.Duration(s.AgeSeconds * float64(time.Second))
		if age == 0 && s.LastActivity != "" {
			if t, perr := time.Parse(time.RFC3339, s.LastActivity); perr == nil {
				age = time.Since(t)
			}
		}
		res = append(res, Session{
			Sandbox: sandbox,
			ID:      s.ID,
			Name:    s.Name,
			State:   s.State,
			PR:      s.PR,
			Age:     age,
		})
	}
	return res, nil
}

// Attach is supplied for interface parity. Track C's normal path runs
// `cs ssh -t … claude attach <id>` directly inside a tmux pane and
// never calls this. The implementation here is a STUB: a PTY-backed
// version belongs in a follow-up if we ever need in-process attach.
func (c *CraftingBackend) Attach(ctx context.Context, sandbox, id string) (io.ReadWriteCloser, error) {
	return nil, errors.New("CraftingBackend.Attach: STUB — Track C attaches via tmux respawn-pane, not in-process. See NOTES.md")
}

func (c *CraftingBackend) Dispatch(ctx context.Context, sandbox, name, prompt string) (string, error) {
	args := []string{"ssh", sandbox, "--", "claude", "--bg"}
	if name != "" {
		args = append(args, "--name", name)
	}
	args = append(args, prompt)
	out, err := runCmd(ctx, "cs", args...)
	if err != nil {
		return "", err
	}
	// `claude --bg` prints a short session id; STUB: confirm exact
	// format and parse robustly. For now, trim and take last token.
	line := strings.TrimSpace(string(out))
	parts := strings.Fields(line)
	if len(parts) == 0 {
		return "", fmt.Errorf("dispatch: empty response")
	}
	return parts[len(parts)-1], nil
}

func (c *CraftingBackend) Stop(ctx context.Context, sandbox, id string) error {
	_, err := runCmd(ctx, "cs", "ssh", sandbox, "--", "claude", "stop", id)
	return err
}

func (c *CraftingBackend) Respawn(ctx context.Context, sandbox string) error {
	_, err := runCmd(ctx, "cs", "ssh", sandbox, "--", "claude", "respawn", "--all")
	return err
}

// runCmd is a small helper around exec.CommandContext that captures
// stdout and returns a structured error including stderr.
func runCmd(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w (%s)", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

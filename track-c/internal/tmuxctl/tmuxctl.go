// Package tmuxctl wraps the `tmux -L agentmgr` commands the sidebar fires
// when the user selects, switches between, or closes session views.
//
// Track C uses tmux as an invisible layout engine: one window per attached
// session lets the user switch between them without losing each one's
// terminal state, which is what the DoD "switch without losing state"
// requirement needs. The sidebar always lives in window 0 ("main"); each
// attached session gets a dedicated window named "<sandbox>/<id>".
package tmuxctl

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Socket is the tmux -L target. Kept as a var so tests can change it.
var Socket = "agentmgr"

// Run executes `tmux -L <Socket> <args...>` and returns combined output.
func Run(args ...string) (string, error) {
	full := append([]string{"-L", Socket}, args...)
	cmd := exec.Command("tmux", full...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("tmux %s: %w (%s)", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// HasWindow reports whether a window with the given name exists in session
// "mgr".
func HasWindow(name string) (bool, error) {
	out, err := Run("list-windows", "-t", "mgr", "-F", "#{window_name}")
	if err != nil {
		// `list-windows` against a missing session is an error, not "no
		// windows"; treat it as "no" so the caller can fall through to
		// new-window in fresh-start mode.
		return false, nil
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == name {
			return true, nil
		}
	}
	return false, nil
}

// WindowName is the canonical name we give a content window for a given
// session. tmux's target parser uses ':' to separate session from
// window, '.' for pane, and '/' is treated specially in some commands.
// '~' is safe and visually distinct in the status bar (when re-enabled).
func WindowName(sandbox, id string) string {
	return fmt.Sprintf("%s~%s", sandbox, id)
}

// AttachInNewWindow creates a new tmux window running `cmd` and switches
// to it. If a window with the same name already exists, it switches to
// it instead (no re-attach, no state lost).
//
// `cmd` is passed to tmux as a single argv string; tmux uses /bin/sh -c
// to execute it.
func AttachInNewWindow(sandbox, id, cmd string) error {
	name := WindowName(sandbox, id)
	exists, err := HasWindow(name)
	if err != nil {
		return err
	}
	if exists {
		_, err = Run("select-window", "-t", "mgr:"+name)
		return err
	}
	// new-window: create, name it, run cmd. The window then has a single
	// pane in which `cmd` is running. -d keeps focus on the current window
	// while we set things up; we explicitly select-window after to switch.
	if _, err := Run("new-window", "-t", "mgr", "-n", name, cmd); err != nil {
		return err
	}
	_, err = Run("select-window", "-t", "mgr:"+name)
	return err
}

// FocusSidebar switches back to the window that holds the sidebar (the
// "main" window). Called when the user hits a key in the sidebar program
// that should bring the sidebar to the front.
func FocusSidebar() error {
	_, err := Run("select-window", "-t", "mgr:main")
	return err
}

// CloseContentWindow kills the content window for (sandbox, id) if it
// exists. Used when the user explicitly closes/detaches a view.
func CloseContentWindow(sandbox, id string) error {
	name := WindowName(sandbox, id)
	exists, err := HasWindow(name)
	if err != nil || !exists {
		return err
	}
	_, err = Run("kill-window", "-t", "mgr:"+name)
	return err
}

// ErrNoTmux is returned by Available() when `tmux` is not on PATH.
var ErrNoTmux = errors.New("tmux not installed")

// Available reports whether tmux is callable. Used by the sidebar so it
// can degrade gracefully (or refuse to start) when tmux is missing.
func Available() error {
	if _, err := exec.LookPath("tmux"); err != nil {
		return ErrNoTmux
	}
	return nil
}

// Package attach implements the `agentmgr attach` subcommand: a small
// child process that runs in a tmux content window and either
//
//   - renders a placeholder until the user picks a session (placeholder mode);
//   - streams the MockBackend's scripted ANSI for a chosen (sandbox, id)
//     when running in mock mode;
//   - execs into `cs ssh -t <sandbox> -- claude attach <id>` when running
//     against the real backend (a thin wrapper around the spec C
//     reconnect loop).
//
// The sidebar shells out to this via `tmux respawn-pane` / `new-window`,
// passing the role and arguments on the command line. Keeping this here
// (instead of inside the sidebar process) lets tmux own the lifecycle of
// the content pane — same model spec C describes.
package attach

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"agentmgr/backend"
)

// Placeholder writes a friendly "no session selected yet" screen and
// blocks until the process is killed (tmux will kill -k when the user
// selects a row).
func Placeholder() error {
	fmt.Print("\x1b[2J\x1b[H")
	fmt.Println("\x1b[1;36m  agentmgr — track C\x1b[0m")
	fmt.Println("\x1b[2m  ─────────────────────────────────\x1b[0m")
	fmt.Println()
	fmt.Println("  Pick a session in the sidebar (left pane) and press \x1b[1mEnter\x1b[0m.")
	fmt.Println()
	fmt.Println("  \x1b[2mF12 toggles focus between the sidebar and this pane.\x1b[0m")
	fmt.Println("  \x1b[2mq in the sidebar (or C-b q) quits.\x1b[0m")
	// Block on a signal: the process exits when tmux respawns / kills
	// the pane on selection.
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	<-ch
	return nil
}

// Mock streams the MockBackend's scripted Attach output for (sandbox, id)
// to stdout. The mock backend's Attach holds its pipe open after the
// script finishes (its documented contract), so we run io.Copy in a
// goroutine and watch for either the script signalling end-of-script
// via a quiescence timer or the process being signalled.
//
// We loop the script with a short pause between iterations so a reviewer
// who lingers on the window keeps seeing motion, and so the "switch and
// come back" demo shows that the window still has state.
func Mock(sandbox, id string) error {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)

	b := backend.NewMockBackend()
	for {
		ctx, cancel := context.WithCancel(context.Background())
		stream, err := b.Attach(ctx, sandbox, id)
		if err != nil {
			cancel()
			return err
		}

		// MockBackend's pipe doesn't close after the script finishes;
		// instead it idles. To detect "script done" without changing
		// the MockBackend contract, we copy through a tee that resets
		// a quiescence timer on every write. When no bytes arrive for
		// quiescence-window the script is considered finished.
		const quiescence = 600 * time.Millisecond
		quiet := time.NewTimer(quiescence)
		copied := make(chan error, 1)
		go func() {
			buf := make([]byte, 4096)
			for {
				n, rerr := stream.Read(buf)
				if n > 0 {
					if _, werr := os.Stdout.Write(buf[:n]); werr != nil {
						copied <- werr
						return
					}
					if !quiet.Stop() {
						select {
						case <-quiet.C:
						default:
						}
					}
					quiet.Reset(quiescence)
				}
				if rerr != nil {
					copied <- rerr
					return
				}
			}
		}()

		select {
		case <-quiet.C:
			// Stream went quiet: script likely finished. Tear down,
			// pause, and replay.
		case err := <-copied:
			if err != nil && err != io.EOF {
				_ = stream.Close()
				cancel()
				return err
			}
		case sig := <-sigs:
			_ = stream.Close()
			cancel()
			fmt.Printf("\r\n(received %s; exiting)\r\n", sig)
			return nil
		}

		_ = stream.Close()
		cancel()

		fmt.Print("\r\n\x1b[2m  (mock stream ended; replaying in 3s — press Ctrl-C to quit)\x1b[0m\r\n")
		select {
		case <-time.After(3 * time.Second):
		case sig := <-sigs:
			fmt.Printf("(received %s; exiting)\r\n", sig)
			return nil
		}
	}
}

// Real execs into `cs ssh -t <sandbox> -- claude attach <id>` and
// auto-reconnects on disconnect, per spec C's reconnect wrapper. Returns
// only on a fatal error; in normal use it runs forever inside the tmux
// content window.
func Real(sandbox, id string) error {
	if _, err := exec.LookPath("cs"); err != nil {
		return fmt.Errorf("`cs` not on PATH: %w", err)
	}
	for {
		cmd := exec.Command("cs", "ssh", "-t", sandbox, "--", "claude", "attach", id)
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			// The session still lives in the supervisor on the
			// sandbox; an exit here is an SSH drop. Wait and
			// reconnect.
			fmt.Fprintf(os.Stderr, "\r\n\x1b[33mclaude attach exited (%v); reconnecting in 1s…\x1b[0m\r\n", err)
		}
		time.Sleep(1 * time.Second)
	}
}

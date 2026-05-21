// Track C — Cross-Sandbox Agent Aggregator (agent view + tmux layout).
//
// One binary, several roles, dispatched on the first positional argument:
//
//	agentmgr sidebar [--real]          run the Bubble Tea sidebar (left pane)
//	agentmgr attach --placeholder      placeholder content for the right pane
//	agentmgr attach --mock <sb> <id>   replay the mock attach stream
//	agentmgr attach --real <sb> <id>   cs ssh -t <sb> -- claude attach <id>
//	agentmgr roster [--real]           one-shot non-tmux roster dump (smoke test)
//
// scripts/launch.sh wires the pieces together via `tmux -L agentmgr`. See
// README.md for the demo flow.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"agentmgr/backend"
	"agentmgr/internal/attach"
	"agentmgr/internal/sidebar"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "sidebar":
		runSidebar(os.Args[2:])
	case "attach":
		runAttach(os.Args[2:])
	case "roster":
		runRoster(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `agentmgr — cross-sandbox Claude Code agent manager (track C: tmux layout)

usage:
  agentmgr sidebar [--real]              run the sidebar (Bubble Tea, intended for tmux left pane)
  agentmgr attach --placeholder          render the idle right-pane screen
  agentmgr attach --mock <sb> <id>       replay the mock attach stream for a session
  agentmgr attach --real <sb> <id>       cs ssh -t <sb> -- claude attach <id> (with reconnect)
  agentmgr roster [--real]               one-shot roster dump (no tmux, no TUI)

env:
  AGENTMGR_BACKEND=mock|crafting         override the default backend (mock).

usually you don't run these directly — use scripts/launch.sh, which sets up
tmux on its own -L agentmgr socket with the agentmgr.conf in this repo.`)
}

// chooseBackend picks the backend based on --real, env, and the default.
// Default is mock so a reviewer without Crafting access can demo.
func chooseBackend(real bool) backend.Backend {
	if real || os.Getenv("AGENTMGR_BACKEND") == "crafting" {
		return backend.NewCraftingBackend()
	}
	return backend.NewMockBackend()
}

// chooseAttachTemplate returns the shell command template the sidebar
// shoves into tmux when the user selects a row. Mock and real are
// different commands; both encode (sandbox, id) via fmt.Sprintf.
func chooseAttachTemplate(real bool) string {
	self, err := os.Executable()
	if err != nil {
		self = "agentmgr"
	}
	if real || os.Getenv("AGENTMGR_BACKEND") == "crafting" {
		return self + " attach --real %s %s"
	}
	return self + " attach --mock %s %s"
}

func runSidebar(args []string) {
	fs := flag.NewFlagSet("sidebar", flag.ExitOnError)
	real := fs.Bool("real", false, "use the live CraftingBackend instead of MockBackend")
	_ = fs.Parse(args)

	b := chooseBackend(*real)
	tpl := chooseAttachTemplate(*real)
	if err := sidebar.Run(b, tpl); err != nil {
		fmt.Fprintln(os.Stderr, "sidebar:", err)
		os.Exit(1)
	}
}

func runAttach(args []string) {
	fs := flag.NewFlagSet("attach", flag.ExitOnError)
	placeholder := fs.Bool("placeholder", false, "render the idle right-pane screen and wait")
	mock := fs.Bool("mock", false, "stream the mock backend's scripted ANSI for (sandbox, id)")
	real := fs.Bool("real", false, "cs ssh -t <sandbox> -- claude attach <id>")
	_ = fs.Parse(args)

	switch {
	case *placeholder:
		if err := attach.Placeholder(); err != nil {
			fmt.Fprintln(os.Stderr, "attach placeholder:", err)
			os.Exit(1)
		}
	case *mock:
		if fs.NArg() < 2 {
			fmt.Fprintln(os.Stderr, "attach --mock requires <sandbox> <id>")
			os.Exit(2)
		}
		if err := attach.Mock(fs.Arg(0), fs.Arg(1)); err != nil {
			fmt.Fprintln(os.Stderr, "attach mock:", err)
			os.Exit(1)
		}
	case *real:
		if fs.NArg() < 2 {
			fmt.Fprintln(os.Stderr, "attach --real requires <sandbox> <id>")
			os.Exit(2)
		}
		if err := attach.Real(fs.Arg(0), fs.Arg(1)); err != nil {
			fmt.Fprintln(os.Stderr, "attach real:", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintln(os.Stderr, "attach: need one of --placeholder, --mock, --real")
		os.Exit(2)
	}
}

func runRoster(args []string) {
	fs := flag.NewFlagSet("roster", flag.ExitOnError)
	real := fs.Bool("real", false, "use the live CraftingBackend instead of MockBackend")
	_ = fs.Parse(args)

	b := chooseBackend(*real)
	sessions, err := b.List(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, "roster:", err)
		os.Exit(1)
	}
	fmt.Printf("%d sessions:\n", len(sessions))
	for _, s := range sessions {
		fmt.Printf("  %-12s  %-6s  %-22s  %-12s  %s\n", s.Sandbox, s.ID, s.Name, s.State, s.PR)
	}
}

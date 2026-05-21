// Cross-Sandbox Agent Aggregator — bubbleterm variant (agent view + native).
//
// Single Go binary that aggregates Claude Code background sessions
// across multiple Crafting sandboxes into one flat list and lets you
// attach to any of them via an embedded terminal emulator.
//
// Defaults to MockBackend so the whole tool runs without `cs` access.
// Pass --real or set AGENTMGR_BACKEND=crafting to use the live
// CraftingBackend (which shells out to `cs` and `claude`).
package main

import (
	"flag"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"

	"agentmgr/backend"
	"agentmgr/backend/crafting"
	"agentmgr/ui"
)

func main() {
	var (
		real = flag.Bool("real", false, "use the live CraftingBackend instead of the mock")
	)
	flag.Parse()

	var be backend.Backend
	useReal := *real || os.Getenv("AGENTMGR_BACKEND") == "crafting"
	if useReal {
		be = crafting.New()
	} else {
		be = backend.NewMockBackend()
	}

	m := ui.New(be)
	p := tea.NewProgram(m)
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "agentmgr:", err)
		os.Exit(1)
	}
}

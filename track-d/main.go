// Track D — Cross-Sandbox Agent Aggregator (agent view + bubbleterm).
// Build session: replace this stub with the real entrypoint per
// spec-d-agentview-bubbleterm.md.
package main

import (
	"context"
	"fmt"
	"os"

	"agentmgr/backend"
)

func main() {
	b := backend.NewMockBackend()
	sessions, err := b.List(context.Background())
	if err != nil {
		fmt.Fprintln(os.Stderr, "list:", err)
		os.Exit(1)
	}
	fmt.Printf("track-d scaffold OK: %d mock sessions across sandboxes\n", len(sessions))
	for _, s := range sessions {
		fmt.Printf("  %-12s  %-6s  %-18s  %s\n", s.Sandbox, s.ID, s.Name, s.State)
	}
}

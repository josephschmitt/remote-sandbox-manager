// Fidelity spike for spec D.
//
// Drives a real subprocess under a creack/pty PTY and feeds its output
// through taigrr/bubbleterm in full-window mode. The point is to judge
// whether bubbleterm renders a real TUI cleanly enough to host
// `claude attach` later: alt-screen, rapid redraws, true/256 color,
// wide/CJK runes, cursor shape, OSC title.
//
// Usage:
//
//	go run ./cmd/spike             # runs bash
//	go run ./cmd/spike vim         # runs vim (good alt-screen test)
//	go run ./cmd/spike claude      # if you have claude
//
// Exit with ctrl-d (sends EOF to the child) or ctrl+\ (force quit).
package main

import (
	"fmt"
	"os"
	"os/exec"

	tea "charm.land/bubbletea/v2"
	"github.com/creack/pty"
	"github.com/taigrr/bubbleterm"
)

type spikeModel struct {
	term *bubbleterm.Model
	ptmx *os.File
	cmd  *exec.Cmd
	w, h int
}

func (m *spikeModel) Init() tea.Cmd {
	return m.term.Init()
}

func (m *spikeModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		// Keep PTY winsize in lockstep with the emulator. Both want
		// the same cols/rows since the spike is full-window.
		_ = pty.Setsize(m.ptmx, &pty.Winsize{
			Cols: uint16(m.w),
			Rows: uint16(m.h),
		})
		// Forward to bubbleterm too. (Its own WindowSizeMsg handler
		// would call emu.Resize(width-2, height) which we don't want
		// for full-window use, so call Resize directly.)
		cmd := m.term.Resize(m.w, m.h)
		return m, cmd
	case tea.KeyPressMsg:
		// Ctrl+\ quits the spike (SIGQUIT-ish, harder to fat-finger).
		if msg.Key().Code == '\\' && msg.Key().Mod.Contains(tea.ModCtrl) {
			return m, tea.Quit
		}
	}
	// Everything else goes to the terminal model. Input keys get
	// re-encoded and written to the PTY by bubbleterm; output frames
	// arrive as terminalOutputMsg internally.
	updated, cmd := m.term.Update(msg)
	if t, ok := updated.(*bubbleterm.Model); ok {
		m.term = t
	}
	return m, cmd
}

func (m *spikeModel) View() tea.View {
	v := m.term.View()
	v.AltScreen = true
	v.MouseMode = tea.MouseModeAllMotion
	return v
}

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		args = []string{"bash"}
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	// Start the command under a real PTY. Default size; the first
	// WindowSizeMsg will resize it.
	ptmx, err := pty.Start(cmd)
	if err != nil {
		fmt.Fprintln(os.Stderr, "pty.Start:", err)
		os.Exit(1)
	}
	defer ptmx.Close()

	const (
		initCols = 120
		initRows = 36
	)
	_ = pty.Setsize(ptmx, &pty.Winsize{Cols: initCols, Rows: initRows})

	term, err := bubbleterm.NewWithPipes(initCols, initRows, ptmx, ptmx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "bubbleterm:", err)
		os.Exit(1)
	}

	m := &spikeModel{term: term, ptmx: ptmx, cmd: cmd, w: initCols, h: initRows}
	p := tea.NewProgram(m)
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "spike:", err)
		os.Exit(1)
	}

	// Tear the child down on exit. Closing the PTY would also do this.
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	_ = cmd.Wait()
}

// Package sidebar implements the Bubble Tea v2 program that runs in tmux's
// left pane. It owns the cross-sandbox aggregator (polling the Backend),
// renders the flat list, handles j/k/enter navigation, and fires the tmux
// commands that open/switch attached-session windows in the right pane.
//
// Everything tmux-shaped lives in package tmuxctl; this file only knows
// about the seam (the Backend), the list state, and key handling.
package sidebar

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"agentmgr/backend"
	"agentmgr/internal/tmuxctl"
)

// pollInterval is how often the sidebar refreshes the aggregated roster.
// Kept short for the demo so the mock's drifting ages are visible.
const pollInterval = 2 * time.Second

// Run boots the Bubble Tea program against the given backend. The
// attachCmd template is the shell command tmux runs in the content
// window when the user selects a row; it must contain two %s
// placeholders, in order: sandbox, id. Examples:
//
//	mock:  agentmgr attach --mock <sb> <id>
//	real:  cs ssh -t <sb> -- claude attach <id>
func Run(b backend.Backend, attachCmdTemplate string) error {
	if err := tmuxctl.Available(); err != nil {
		fmt.Fprintln(os.Stderr, "tmux not found on PATH; the sidebar needs tmux to function.")
		fmt.Fprintln(os.Stderr, "Install tmux or run `agentmgr roster` for a non-tmux smoke test.")
		return err
	}
	m := newModel(b, attachCmdTemplate)
	p := tea.NewProgram(m)
	_, err := p.Run()
	return err
}

// model is the Bubble Tea state. Kept deliberately small: the only
// stateful thing is the cursor and the cached roster.
type model struct {
	backend   backend.Backend
	attachTpl string

	rows   []row
	cursor int

	width, height int

	lastErr      error
	lastPolledAt time.Time
	// activeKey is the (sandbox, id) the user most recently attached to;
	// used for the "current selection" marker on rows that are open in
	// a content window.
	activeKey string
}

// row is one displayed session. Mirrors backend.Session but with the
// pre-computed display marker so View() stays cheap.
type row struct {
	backend.Session
	marker string
}

func newModel(b backend.Backend, tpl string) *model {
	return &model{backend: b, attachTpl: tpl}
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(pollCmd(m.backend), tickCmd())
}

// --- messages ---------------------------------------------------------------

type rosterMsg struct {
	sessions []backend.Session
	err      error
}
type tickMsg struct{}
type attachDoneMsg struct {
	sandbox, id string
	err         error
}

func pollCmd(b backend.Backend) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s, err := b.List(ctx)
		return rosterMsg{sessions: s, err: err}
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(pollInterval, func(time.Time) tea.Msg { return tickMsg{} })
}

func attachCmd(tpl, sandbox, id string) tea.Cmd {
	return func() tea.Msg {
		cmd := fmt.Sprintf(tpl, sandbox, id)
		err := tmuxctl.AttachInNewWindow(sandbox, id, cmd)
		return attachDoneMsg{sandbox: sandbox, id: id, err: err}
	}
}

// --- update -----------------------------------------------------------------

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tickMsg:
		// Refresh on a steady interval. Also schedule the next tick.
		return m, tea.Batch(pollCmd(m.backend), tickCmd())

	case rosterMsg:
		m.lastPolledAt = time.Now()
		if msg.err != nil {
			m.lastErr = msg.err
			return m, nil
		}
		m.lastErr = nil
		m.rows = layout(msg.sessions)
		if m.cursor >= len(m.rows) {
			m.cursor = max(0, len(m.rows)-1)
		}
		return m, nil

	case attachDoneMsg:
		if msg.err != nil {
			m.lastErr = msg.err
			return m, nil
		}
		m.activeKey = key(msg.sandbox, msg.id)
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *model) handleKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "j", "down":
		if m.cursor < len(m.rows)-1 {
			m.cursor++
		}
		return m, nil
	case "k", "up":
		if m.cursor > 0 {
			m.cursor--
		}
		return m, nil
	case "g", "home":
		m.cursor = 0
		return m, nil
	case "G", "end":
		if len(m.rows) > 0 {
			m.cursor = len(m.rows) - 1
		}
		return m, nil
	case "r":
		// Manual refresh.
		return m, pollCmd(m.backend)
	case "enter", " ":
		if m.cursor < len(m.rows) {
			r := m.rows[m.cursor]
			return m, attachCmd(m.attachTpl, r.Sandbox, r.ID)
		}
		return m, nil
	case "x":
		// Close the content window for the highlighted row (if any).
		// Useful for a reviewer demonstrating "switch and close".
		if m.cursor < len(m.rows) {
			r := m.rows[m.cursor]
			_ = tmuxctl.CloseContentWindow(r.Sandbox, r.ID)
			if key(r.Sandbox, r.ID) == m.activeKey {
				m.activeKey = ""
			}
		}
		return m, nil
	}
	return m, nil
}

// --- view -------------------------------------------------------------------

var (
	titleStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#7c3aed")).
			Padding(0, 1)
	headerStyle = lipgloss.NewStyle().Faint(true).Padding(0, 1)
	rowStyle    = lipgloss.NewStyle().Padding(0, 1)
	selStyle    = lipgloss.NewStyle().Padding(0, 1).
			Background(lipgloss.Color("#2a2a3a")).Bold(true)
	activeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#10b981"))
	dimStyle    = lipgloss.NewStyle().Faint(true)
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#ef4444"))
	footStyle   = lipgloss.NewStyle().Faint(true).Padding(0, 1)
)

func (m *model) View() tea.View {
	var b strings.Builder
	b.WriteString(titleStyle.Render("agentmgr (track C)"))
	b.WriteString("\n")
	b.WriteString(headerStyle.Render(fmt.Sprintf("%d sessions across sandboxes", len(m.rows))))
	b.WriteString("\n\n")

	if m.lastErr != nil {
		b.WriteString(errStyle.Render("error: " + m.lastErr.Error()))
		b.WriteString("\n\n")
	}

	if len(m.rows) == 0 {
		b.WriteString(dimStyle.Render("  (no sessions)\n"))
	}

	var currentSandbox string
	for i, r := range m.rows {
		if r.Sandbox != currentSandbox {
			if currentSandbox != "" {
				b.WriteString("\n")
			}
			b.WriteString(headerStyle.Render(strings.ToUpper(r.Sandbox)))
			b.WriteString("\n")
			currentSandbox = r.Sandbox
		}
		line := renderRow(r, key(r.Sandbox, r.ID) == m.activeKey)
		style := rowStyle
		if i == m.cursor {
			style = selStyle
		}
		b.WriteString(style.Render(line))
		b.WriteString("\n")
	}

	b.WriteString("\n")
	help := "j/k move • enter attach • x close • r refresh • F12 focus pane • q quit"
	b.WriteString(footStyle.Render(help))
	if !m.lastPolledAt.IsZero() {
		b.WriteString("\n")
		b.WriteString(footStyle.Render(fmt.Sprintf("last poll: %s ago", time.Since(m.lastPolledAt).Round(time.Second))))
	}
	v := tea.NewView(b.String())
	v.AltScreen = true
	return v
}

// renderRow builds the per-row string. Format:
//
//	● working   refactor-auth      42s   draft #142
//	○ idle      audit-deps         12m
//
// A leading "▸ " marks the row whose content window is currently focused
// (or was most recently opened).
func renderRow(r row, isActive bool) string {
	prefix := "  "
	if isActive {
		prefix = activeStyle.Render("▸ ")
	}
	state := stateLabel(r.State)
	name := r.Name
	if len(name) > 22 {
		name = name[:21] + "…"
	}
	age := formatAge(r.Age)
	pr := ""
	if r.PR != "" {
		pr = dimStyle.Render("  " + r.PR)
	}
	return fmt.Sprintf("%s%s %s %-22s %5s%s", prefix, r.marker, state, name, age, pr)
}

// stateLabel maps backend state strings to a fixed-width colored label.
func stateLabel(s string) string {
	switch s {
	case backend.StateWorking:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#10b981")).Render(fmt.Sprintf("%-11s", "working"))
	case backend.StateNeedsInput:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#f59e0b")).Bold(true).Render(fmt.Sprintf("%-11s", "needs-input"))
	case backend.StateIdle:
		return dimStyle.Render(fmt.Sprintf("%-11s", "idle"))
	case backend.StateCompleted:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#06b6d4")).Render(fmt.Sprintf("%-11s", "completed"))
	case backend.StateFailed:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#ef4444")).Render(fmt.Sprintf("%-11s", "failed"))
	case backend.StateStopped:
		return dimStyle.Render(fmt.Sprintf("%-11s", "stopped"))
	default:
		return fmt.Sprintf("%-11s", s)
	}
}

// stateMarker returns a single glyph for each state. Used in renderRow.
func stateMarker(s string) string {
	switch s {
	case backend.StateWorking:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#10b981")).Render("●")
	case backend.StateNeedsInput:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#f59e0b")).Bold(true).Render("!")
	case backend.StateIdle:
		return dimStyle.Render("○")
	case backend.StateCompleted:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#06b6d4")).Render("✓")
	case backend.StateFailed:
		return lipgloss.NewStyle().Foreground(lipgloss.Color("#ef4444")).Render("✗")
	case backend.StateStopped:
		return dimStyle.Render("■")
	default:
		return "?"
	}
}

func formatAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// layout sorts the flat session list into a stable, sandbox-grouped order
// and attaches state markers. Sandbox order is alphabetic; sessions within
// a sandbox sort by state priority then name so the most "loud" rows
// (needs-input, working) bubble to the top.
func layout(in []backend.Session) []row {
	out := make([]row, len(in))
	for i, s := range in {
		out[i] = row{Session: s, marker: stateMarker(s.State)}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Sandbox != out[j].Sandbox {
			return out[i].Sandbox < out[j].Sandbox
		}
		if statePriority(out[i].State) != statePriority(out[j].State) {
			return statePriority(out[i].State) < statePriority(out[j].State)
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func statePriority(s string) int {
	switch s {
	case backend.StateNeedsInput:
		return 0
	case backend.StateWorking:
		return 1
	case backend.StateIdle:
		return 2
	case backend.StateCompleted:
		return 3
	case backend.StateStopped:
		return 4
	case backend.StateFailed:
		return 5
	default:
		return 6
	}
}

func key(sandbox, id string) string { return sandbox + "/" + id }

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

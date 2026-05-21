// Package ui is the Bubble Tea front end for the cross-sandbox agent
// manager. It owns the sidebar list, the embedded bubbleterm-backed
// content pane, focus routing between the two, and the size pipeline
// that keeps the PTY winsize, the emulator size, and the lipgloss
// content rectangle in lockstep.
package ui

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/taigrr/bubbleterm"

	"agentmgr/backend"
)

// Focus indicates which pane has the keyboard.
type focus int

const (
	focusSidebar focus = iota
	focusContent
)

// session holds the live state for one attached connection. There is
// at most one live attach per (sandbox, id) but we keep them all in a
// map so toggling between sessions preserves each one's scrollback and
// state — that's the "switch without losing state" Definition-of-Done
// requirement.
type session struct {
	sandbox, id string
	term        *bubbleterm.Model
	stream      io.ReadWriteCloser
}

func (s *session) key() string { return s.sandbox + "/" + s.id }

// Model is the root Bubble Tea model.
type Model struct {
	be backend.Backend

	rows []backend.Session // current aggregated roster
	sel  int               // selected row in rows
	err  error             // last list error, if any

	attached map[string]*session // keyed by sandbox/id
	cur      *session            // nil if nothing attached

	foc focus

	// layout: full window size minus chrome.
	w, h    int
	sideW   int
	contentW, contentH int

	// styles
	styles styles

	// last roster timestamp, displayed in the status line
	lastList time.Time
}

// New constructs a Model bound to the given backend.
func New(be backend.Backend) *Model {
	return &Model{
		be:       be,
		attached: map[string]*session{},
		sideW:    36,
		styles:   newStyles(),
	}
}

// listMsg carries a fresh roster from Backend.List.
type listMsg struct {
	sessions []backend.Session
	at       time.Time
	err      error
}

// attachMsg carries a freshly opened attach stream for a (sandbox, id).
type attachMsg struct {
	sandbox, id string
	stream      io.ReadWriteCloser
	term        *bubbleterm.Model
	err         error
}

// pollTick fires the roster refresh.
type pollTick struct{}

func pollEvery(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg { return pollTick{} })
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(
		m.listCmd(),
		pollEvery(2*time.Second),
	)
}

func (m *Model) listCmd() tea.Cmd {
	be := m.be
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		ss, err := be.List(ctx)
		return listMsg{sessions: ss, at: time.Now(), err: err}
	}
}

// attachCmd opens an attach stream and wraps it in a bubbleterm model
// sized to the current content rect. It runs off the Bubble Tea event
// loop because Attach may block (real backend will ssh).
func (m *Model) attachCmd(sandbox, id string) tea.Cmd {
	be := m.be
	w, h := m.contentW, m.contentH
	if w < 4 {
		w = 80
	}
	if h < 2 {
		h = 24
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		stream, err := be.Attach(ctx, sandbox, id)
		if err != nil {
			return attachMsg{sandbox: sandbox, id: id, err: err}
		}
		// If the stream is a real PTY, pin its initial winsize to the
		// emulator size from the same place. (Mock streams don't have
		// a winsize; the type assert fails harmlessly.)
		if r, ok := stream.(interface {
			Resize(cols, rows int) error
		}); ok {
			_ = r.Resize(w, h)
		}
		// bubbleterm.NewWithPipes treats r,w as the child's stdout/stdin
		// from the emulator's point of view: the emulator reads bytes
		// out of r and writes user input into w. Our backend stream is
		// a ReadWriteCloser where both directions work, so we use it
		// for both.
		t, err := bubbleterm.NewWithPipes(w, h, stream, rwcWriter{stream})
		if err != nil {
			_ = stream.Close()
			return attachMsg{sandbox: sandbox, id: id, err: err}
		}
		return attachMsg{sandbox: sandbox, id: id, stream: stream, term: t}
	}
}

// rwcWriter adapts a ReadWriteCloser to a WriteCloser (bubbleterm
// only wants the write half on the input side).
type rwcWriter struct{ io.ReadWriteCloser }

func (w rwcWriter) Write(p []byte) (int, error) { return w.ReadWriteCloser.Write(p) }
func (w rwcWriter) Close() error                { return w.ReadWriteCloser.Close() }

// Update is the message switchboard.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.resize(msg.Width, msg.Height)
		// Don't forward WindowSizeMsg to bubbleterm: its handler
		// resizes to (Width-2, Height) for the whole window, but we
		// already sized each model to the content rect.
		return m, nil

	case pollTick:
		return m, tea.Batch(m.listCmd(), pollEvery(2*time.Second))

	case listMsg:
		m.lastList = msg.at
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.err = nil
		m.rows = msg.sessions
		if m.sel >= len(m.rows) {
			m.sel = len(m.rows) - 1
		}
		if m.sel < 0 {
			m.sel = 0
		}
		return m, nil

	case attachMsg:
		if msg.err != nil {
			m.err = fmt.Errorf("attach %s/%s: %w", msg.sandbox, msg.id, msg.err)
			return m, nil
		}
		s := &session{sandbox: msg.sandbox, id: msg.id, stream: msg.stream, term: msg.term}
		m.attached[s.key()] = s
		m.cur = s
		// Kick the polling chain so frames start arriving.
		return m, s.term.Init()

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}

	// Forward anything else (terminalOutputMsg, terminalErrorMsg) to
	// every attached terminal model. They self-filter by EmulatorID
	// so this broadcast is safe.
	var cmds []tea.Cmd
	for _, s := range m.attached {
		updated, cmd := s.term.Update(msg)
		if t, ok := updated.(*bubbleterm.Model); ok {
			s.term = t
		}
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return m, tea.Batch(cmds...)
}

func (m *Model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	k := msg.Key()

	// Always-on globals.
	switch {
	case k.Code == 'c' && k.Mod.Contains(tea.ModCtrl):
		return m, tea.Quit
	case k.Code == '\\' && k.Mod.Contains(tea.ModCtrl):
		return m, tea.Quit
	case k.Code == tea.KeyTab:
		// Focus toggle. Only flip into content if we have something.
		if m.foc == focusSidebar && m.cur != nil {
			m.foc = focusContent
			m.cur.term.Focus()
		} else {
			m.foc = focusSidebar
			if m.cur != nil {
				m.cur.term.Blur()
			}
		}
		return m, nil
	}

	if m.foc == focusContent && m.cur != nil {
		// Route keys to the attached terminal.
		updated, cmd := m.cur.term.Update(msg)
		if t, ok := updated.(*bubbleterm.Model); ok {
			m.cur.term = t
		}
		return m, cmd
	}

	// Sidebar-focused keys.
	switch {
	case k.Code == 'q':
		return m, tea.Quit
	case k.Code == tea.KeyUp || k.Code == 'k':
		if m.sel > 0 {
			m.sel--
		}
	case k.Code == tea.KeyDown || k.Code == 'j':
		if m.sel < len(m.rows)-1 {
			m.sel++
		}
	case k.Code == tea.KeyHome || k.Code == 'g':
		m.sel = 0
	case k.Code == tea.KeyEnd || k.Code == 'G':
		m.sel = len(m.rows) - 1
	case k.Code == tea.KeyEnter:
		if m.sel < 0 || m.sel >= len(m.rows) {
			return m, nil
		}
		row := m.rows[m.sel]
		// If already attached, re-focus, don't reopen.
		if existing, ok := m.attached[row.Sandbox+"/"+row.ID]; ok {
			m.cur = existing
			m.foc = focusContent
			existing.term.Focus()
			return m, nil
		}
		// Otherwise, lazily start the attach.
		return m, m.attachCmd(row.Sandbox, row.ID)
	}
	return m, nil
}

// resize is the single source of truth for layout. Anywhere that needs
// to react to a new window size goes through here, so the PTY winsize,
// the bubbleterm model size, and the lipgloss content rect can never
// drift apart.
func (m *Model) resize(w, h int) {
	m.w, m.h = w, h
	// Reserve last row for the status line.
	contentH := h - 1
	if contentH < 1 {
		contentH = 1
	}
	// Sidebar: clamp to a sensible range relative to the window.
	side := m.sideW
	if side > w/2 {
		side = w / 2
	}
	if side < 20 {
		side = 20
	}
	if side > w-10 {
		side = w - 10
	}
	if side < 1 {
		side = 1
	}
	m.sideW = side
	contentW := w - side - 1 // 1 col for divider
	if contentW < 1 {
		contentW = 1
	}
	m.contentW = contentW
	m.contentH = contentH

	// Push the new content rect to every attached terminal. They keep
	// their own internal scrollback so resizing isn't destructive.
	// If the underlying stream is a real PTY, resize it in the same
	// step so the remote tty matches what the emulator expects.
	for _, s := range m.attached {
		// (Note: bubbleterm.Resize internally calls emu.Resize(w-2, h)
		// in its WindowSizeMsg path; the direct Resize method uses the
		// values as-is, so we pass the real content rect.)
		_ = s.term.Resize(m.contentW, m.contentH)
		if r, ok := s.stream.(interface {
			Resize(cols, rows int) error
		}); ok {
			_ = r.Resize(m.contentW, m.contentH)
		}
	}
}

// View renders the whole frame.
func (m *Model) View() tea.View {
	if m.w == 0 || m.h == 0 {
		return tea.NewView("initializing…")
	}
	sidebar := m.renderSidebar()
	content := m.renderContent()
	divider := m.styles.divider.Render(strings.Repeat("│", m.contentH))

	body := lipgloss.JoinHorizontal(lipgloss.Top, sidebar, divider, content)
	status := m.renderStatus()

	v := tea.NewView(body + "\n" + status)
	v.AltScreen = true
	return v
}

func (m *Model) renderSidebar() string {
	var b strings.Builder
	title := m.styles.title.Render("agentmgr · sessions")
	b.WriteString(padRight(title, m.sideW))
	b.WriteByte('\n')

	// Group rows by sandbox so the flat list still surfaces structure.
	rowsByBox := groupBySandbox(m.rows)
	line := 1 // we've written the title
	maxLines := m.contentH
	for _, group := range rowsByBox {
		if line >= maxLines {
			break
		}
		b.WriteString(padRight(m.styles.sandbox.Render(group.sandbox), m.sideW))
		b.WriteByte('\n')
		line++
		for _, row := range group.rows {
			if line >= maxLines {
				break
			}
			selected := m.rows[m.sel].Sandbox == row.Sandbox && m.rows[m.sel].ID == row.ID
			b.WriteString(padRight(m.formatRow(row, selected), m.sideW))
			b.WriteByte('\n')
			line++
		}
	}
	for ; line < maxLines; line++ {
		b.WriteString(padRight("", m.sideW))
		b.WriteByte('\n')
	}
	// Trim trailing newline so JoinHorizontal lines up.
	out := strings.TrimRight(b.String(), "\n")
	return out
}

func (m *Model) formatRow(row backend.Session, selected bool) string {
	marker := stateMarker(row.State)
	attachedDot := " "
	if _, ok := m.attached[row.Sandbox+"/"+row.ID]; ok {
		attachedDot = m.styles.attached.Render("•")
	}
	cursor := "  "
	if selected {
		cursor = m.styles.cursor.Render("▸ ")
	}
	name := row.Name
	if name == "" {
		name = row.ID
	}
	// trim
	width := m.sideW - 8
	if width < 8 {
		width = 8
	}
	if len(name) > width {
		name = name[:width-1] + "…"
	}
	line := fmt.Sprintf("%s%s %s %s", cursor, marker, attachedDot, name)
	if selected {
		line = m.styles.selectedRow.Render(line)
	}
	return line
}

type sandboxGroup struct {
	sandbox string
	rows    []backend.Session
}

func groupBySandbox(rows []backend.Session) []sandboxGroup {
	byName := map[string]int{}
	var groups []sandboxGroup
	for _, r := range rows {
		i, ok := byName[r.Sandbox]
		if !ok {
			groups = append(groups, sandboxGroup{sandbox: r.Sandbox})
			i = len(groups) - 1
			byName[r.Sandbox] = i
		}
		groups[i].rows = append(groups[i].rows, r)
	}
	return groups
}

func stateMarker(state string) string {
	st := newStyles()
	switch state {
	case backend.StateWorking:
		return st.working.Render("●")
	case backend.StateNeedsInput:
		return st.needs.Render("◆")
	case backend.StateIdle:
		return st.idle.Render("○")
	case backend.StateCompleted:
		return st.completed.Render("✓")
	case backend.StateFailed:
		return st.failed.Render("✗")
	case backend.StateStopped:
		return st.stopped.Render("■")
	}
	return "?"
}

func (m *Model) renderContent() string {
	if m.cur == nil {
		placeholder := "no session attached\n\n" +
			"select a row in the sidebar and press enter to attach.\n" +
			"tab toggles focus between sidebar and the attached terminal."
		return padBlock(placeholder, m.contentW, m.contentH)
	}
	body := m.cur.term.View().Content
	// Ensure the body fills exactly contentH lines.
	lines := strings.Split(body, "\n")
	if len(lines) < m.contentH {
		for len(lines) < m.contentH {
			lines = append(lines, "")
		}
	} else if len(lines) > m.contentH {
		lines = lines[:m.contentH]
	}
	return strings.Join(lines, "\n")
}

func (m *Model) renderStatus() string {
	var parts []string
	parts = append(parts, fmt.Sprintf("%d sessions", len(m.rows)))
	switch m.foc {
	case focusSidebar:
		parts = append(parts, "focus: sidebar")
	case focusContent:
		parts = append(parts, "focus: content")
	}
	if m.cur != nil {
		parts = append(parts, fmt.Sprintf("attached: %s/%s", m.cur.sandbox, m.cur.id))
	}
	if m.err != nil {
		parts = append(parts, "err: "+m.err.Error())
	}
	parts = append(parts, "[tab focus] [enter attach] [q quit]")
	s := strings.Join(parts, "  ·  ")
	if len(s) > m.w {
		s = s[:m.w]
	}
	return m.styles.status.Render(padRight(s, m.w))
}

// padRight pads s with spaces to width n. Naive byte width because the
// sidebar uses ASCII for everything except state markers, which are
// monospace single-width glyphs.
func padRight(s string, n int) string {
	w := lipgloss.Width(s)
	if w >= n {
		return s
	}
	return s + strings.Repeat(" ", n-w)
}

func padBlock(s string, w, h int) string {
	lines := strings.Split(s, "\n")
	out := make([]string, h)
	for i := 0; i < h; i++ {
		if i < len(lines) {
			out[i] = padRight(lines[i], w)
		} else {
			out[i] = strings.Repeat(" ", w)
		}
	}
	return strings.Join(out, "\n")
}

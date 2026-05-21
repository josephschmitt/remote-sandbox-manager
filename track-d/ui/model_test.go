package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"agentmgr/backend"
)

// keyPress builds a synthetic KeyPressMsg for tests. The real codec
// inside ultraviolet decodes terminal bytes, but for unit tests we
// can hand-construct.
func keyPress(code rune, mod tea.KeyMod, text string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code, Mod: mod, Text: text}
}

// drain repeatedly applies cmd() results to m until nothing else is
// pending. Bubble Tea normally does this on its own; tests have to
// run the loop manually.
//
// Bounded by a step count because bubbleterm's terminal poll loop
// self-perpetuates by design (every terminalOutputMsg triggers
// another pollTerminal). Once we've seen a stable shape, drop out.
func drain(m tea.Model, cmd tea.Cmd) tea.Model {
	const maxSteps = 32
	for i := 0; i < maxSteps && cmd != nil; i++ {
		msg := cmd()
		if msg == nil {
			return m
		}
		next, ncmd := m.Update(msg)
		m, cmd = next, ncmd
	}
	return m
}

func newWithSize(t *testing.T, be backend.Backend, w, h int) *Model {
	t.Helper()
	m := New(be)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return updated.(*Model)
}

func TestList_Populates_Rows(t *testing.T) {
	be := backend.NewMockBackend()
	m := newWithSize(t, be, 120, 30)

	// Run Init's listCmd manually.
	updated := drain(m, m.listCmd())
	m = updated.(*Model)
	if len(m.rows) != 6 {
		t.Fatalf("want 6 rows from mock, got %d", len(m.rows))
	}
	// Three distinct sandboxes.
	seen := map[string]bool{}
	for _, r := range m.rows {
		seen[r.Sandbox] = true
	}
	if len(seen) != 3 {
		t.Fatalf("want 3 sandboxes, got %d (%v)", len(seen), seen)
	}
}

func TestNavigate_Up_Down_Bounds(t *testing.T) {
	be := backend.NewMockBackend()
	m := newWithSize(t, be, 120, 30)
	m = drain(m, m.listCmd()).(*Model)

	if m.sel != 0 {
		t.Fatalf("initial sel = %d, want 0", m.sel)
	}
	// Down 10 times — should saturate at len-1.
	for i := 0; i < 10; i++ {
		next, _ := m.Update(keyPress(tea.KeyDown, 0, ""))
		m = next.(*Model)
	}
	if m.sel != len(m.rows)-1 {
		t.Fatalf("saturated sel = %d, want %d", m.sel, len(m.rows)-1)
	}
	// Up 10 times — should saturate at 0.
	for i := 0; i < 10; i++ {
		next, _ := m.Update(keyPress(tea.KeyUp, 0, ""))
		m = next.(*Model)
	}
	if m.sel != 0 {
		t.Fatalf("up-saturated sel = %d, want 0", m.sel)
	}
}

func TestAttach_Populates_Cur(t *testing.T) {
	be := backend.NewMockBackend()
	m := newWithSize(t, be, 120, 30)
	m = drain(m, m.listCmd()).(*Model)

	// Press enter on the first row.
	next, cmd := m.Update(keyPress(tea.KeyEnter, 0, ""))
	m = next.(*Model)
	if cmd == nil {
		t.Fatal("enter on a row should return an attach Cmd")
	}
	// Drain the attach cmd, which produces an attachMsg.
	m = drain(m, cmd).(*Model)
	if m.cur == nil {
		t.Fatal("cur should be set after a successful attach")
	}
	if m.err != nil {
		t.Fatalf("unexpected attach error: %v", m.err)
	}
	if got := m.cur.sandbox + "/" + m.cur.id; got != m.rows[0].Sandbox+"/"+m.rows[0].ID {
		t.Fatalf("attached to %s, want %s/%s", got, m.rows[0].Sandbox, m.rows[0].ID)
	}
	if len(m.attached) != 1 {
		t.Fatalf("attached map has %d entries, want 1", len(m.attached))
	}
}

func TestFocus_Toggle_Requires_Cur(t *testing.T) {
	be := backend.NewMockBackend()
	m := newWithSize(t, be, 120, 30)
	m = drain(m, m.listCmd()).(*Model)

	if m.foc != focusSidebar {
		t.Fatalf("initial focus = %d, want sidebar", m.foc)
	}
	// Tab with no attach: should stay on sidebar.
	next, _ := m.Update(keyPress(tea.KeyTab, 0, ""))
	m = next.(*Model)
	if m.foc != focusSidebar {
		t.Fatalf("tab w/o attach moved focus to %d", m.foc)
	}
	// Attach + tab: should flip to content.
	next, cmd := m.Update(keyPress(tea.KeyEnter, 0, ""))
	m = next.(*Model)
	m = drain(m, cmd).(*Model)

	next, _ = m.Update(keyPress(tea.KeyTab, 0, ""))
	m = next.(*Model)
	if m.foc != focusContent {
		t.Fatalf("tab after attach left focus at %d", m.foc)
	}
	// Tab again returns to sidebar.
	next, _ = m.Update(keyPress(tea.KeyTab, 0, ""))
	m = next.(*Model)
	if m.foc != focusSidebar {
		t.Fatalf("second tab left focus at %d", m.foc)
	}
}

func TestSwitch_Sessions_Preserves_State(t *testing.T) {
	be := backend.NewMockBackend()
	m := newWithSize(t, be, 120, 30)
	m = drain(m, m.listCmd()).(*Model)

	// Attach first row.
	next, cmd := m.Update(keyPress(tea.KeyEnter, 0, ""))
	m = next.(*Model)
	m = drain(m, cmd).(*Model)
	first := m.cur

	// Move to second row and attach.
	next, _ = m.Update(keyPress(tea.KeyDown, 0, ""))
	m = next.(*Model)
	next, cmd = m.Update(keyPress(tea.KeyEnter, 0, ""))
	m = next.(*Model)
	m = drain(m, cmd).(*Model)
	second := m.cur

	if first == second {
		t.Fatal("switching to a different row should produce a different session")
	}
	if len(m.attached) != 2 {
		t.Fatalf("attached map has %d entries, want 2", len(m.attached))
	}

	// Move back up and re-attach: should reuse the existing session,
	// not open a new one.
	next, _ = m.Update(keyPress(tea.KeyUp, 0, ""))
	m = next.(*Model)
	next, cmd = m.Update(keyPress(tea.KeyEnter, 0, ""))
	m = next.(*Model)
	// No new attach cmd when already attached.
	if cmd != nil {
		// Drain in case (the code returns nil, but defensive)
		m = drain(m, cmd).(*Model)
	}
	if m.cur != first {
		t.Fatal("re-entering a row should reuse the existing session")
	}
	if len(m.attached) != 2 {
		t.Fatalf("attached count changed on reuse: %d", len(m.attached))
	}
}

func TestResize_Updates_ContentRect(t *testing.T) {
	be := backend.NewMockBackend()
	m := newWithSize(t, be, 120, 30)
	if m.contentW <= 0 || m.contentH <= 0 {
		t.Fatalf("contentW=%d contentH=%d", m.contentW, m.contentH)
	}
	beforeW, beforeH := m.contentW, m.contentH

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 200, Height: 50})
	m = updated.(*Model)
	if m.contentW == beforeW && m.contentH == beforeH {
		t.Fatalf("resize did not update content rect: %dx%d", m.contentW, m.contentH)
	}
	if m.contentW <= 0 || m.contentH <= 0 {
		t.Fatalf("post-resize contentW=%d contentH=%d", m.contentW, m.contentH)
	}
}

func TestView_Renders_Without_Panic(t *testing.T) {
	be := backend.NewMockBackend()
	m := newWithSize(t, be, 120, 30)
	m = drain(m, m.listCmd()).(*Model)

	v := m.View()
	if v.Content == "" {
		t.Fatal("View() returned empty content")
	}
	// At least one mock sandbox name should appear in the rendered body.
	if !strings.Contains(v.Content, "sb-alpha") {
		t.Fatal("expected sb-alpha to appear in the rendered sidebar")
	}
}

// Belt-and-braces: ensure the list refresh tick keeps roster fresh
// (Age values drift forward in the mock).
func TestPollTick_Refreshes(t *testing.T) {
	be := backend.NewMockBackend()
	m := newWithSize(t, be, 120, 30)
	m = drain(m, m.listCmd()).(*Model)
	firstAge := m.rows[0].Age

	time.Sleep(20 * time.Millisecond)
	m = drain(m, m.listCmd()).(*Model)
	if m.rows[0].Age <= firstAge {
		t.Fatalf("age did not drift: before=%s after=%s", firstAge, m.rows[0].Age)
	}
}

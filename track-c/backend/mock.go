package backend

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"
)

// MockBackend fakes a fixed roster of background sessions across three
// pretend sandboxes so the UI is runnable without any Crafting access. On
// Attach it streams a short scripted ANSI sequence (color, redraw, cursor
// move) so the renderer has something real to show.
type MockBackend struct {
	mu       sync.Mutex
	sessions []Session
	started  time.Time
}

// NewMockBackend returns a MockBackend pre-populated with sessions in a
// mix of states across three sandboxes (one of which is "suspended", with
// zero live sessions).
func NewMockBackend() *MockBackend {
	now := time.Now()
	m := &MockBackend{started: now}
	m.sessions = []Session{
		{Sandbox: "payments-api", ID: "j7K2", Name: "refactor-auth", State: StateWorking, PR: "draft #142", Age: 42 * time.Second},
		{Sandbox: "payments-api", ID: "p3Q9", Name: "fix-flaky-test", State: StateNeedsInput, Age: 5 * time.Minute},
		{Sandbox: "payments-api", ID: "x1B4", Name: "audit-deps", State: StateIdle, Age: 12 * time.Minute},
		{Sandbox: "analytics-ingest", ID: "k8M1", Name: "ingest-pipeline", State: StateWorking, PR: "open #87", Age: 18 * time.Second},
		{Sandbox: "analytics-ingest", ID: "z5N7", Name: "doc-pass", State: StateCompleted, PR: "merged #84", Age: 47 * time.Minute},
		{Sandbox: "observability", ID: "v2R6", Name: "spike-tracing", State: StateFailed, Age: 31 * time.Minute},
	}
	return m
}

// List returns the current fake roster. Ages drift forward as a simple way
// to make polling visibly change something between refreshes.
func (m *MockBackend) List(ctx context.Context) ([]Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Session, len(m.sessions))
	delta := time.Since(m.started)
	for i, s := range m.sessions {
		s.Age += delta
		out[i] = s
	}
	return out, nil
}

// Attach returns a stream that emits a short, deterministic ANSI sequence
// keyed off (sandbox, id), then idles. Writes from the renderer are
// accepted and discarded. Closing the stream is the equivalent of a
// detach.
func (m *MockBackend) Attach(ctx context.Context, sandbox, id string) (io.ReadWriteCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	pr, pw := io.Pipe()
	s := &mockStream{r: pr, w: pw, done: make(chan struct{})}

	go func() {
		defer pw.Close()
		script := buildMockScript(sandbox, id)
		for _, chunk := range script {
			select {
			case <-s.done:
				return
			case <-ctx.Done():
				return
			case <-time.After(chunk.delay):
			}
			if _, err := pw.Write([]byte(chunk.bytes)); err != nil {
				return
			}
		}
		// Idle: hold the pipe open until the caller closes us.
		<-s.done
	}()

	return s, nil
}

func (m *MockBackend) Dispatch(ctx context.Context, sandbox, name, prompt string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	id := fmt.Sprintf("m%03d", len(m.sessions))
	m.sessions = append(m.sessions, Session{
		Sandbox: sandbox,
		ID:      id,
		Name:    name,
		State:   StateWorking,
		Age:     0,
	})
	return id, nil
}

func (m *MockBackend) Stop(ctx context.Context, sandbox, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, s := range m.sessions {
		if s.Sandbox == sandbox && s.ID == id {
			m.sessions[i].State = StateStopped
			return nil
		}
	}
	return fmt.Errorf("mock: no session %s/%s", sandbox, id)
}

func (m *MockBackend) Respawn(ctx context.Context, sandbox string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, s := range m.sessions {
		if s.Sandbox == sandbox && (s.State == StateStopped || s.State == StateFailed) {
			m.sessions[i].State = StateIdle
		}
	}
	return nil
}

type mockStream struct {
	r        *io.PipeReader
	w        *io.PipeWriter
	done     chan struct{}
	closeOnce sync.Once
}

func (s *mockStream) Read(p []byte) (int, error)  { return s.r.Read(p) }
func (s *mockStream) Write(p []byte) (int, error) { return len(p), nil }
func (s *mockStream) Close() error {
	s.closeOnce.Do(func() { close(s.done) })
	_ = s.w.Close()
	return s.r.Close()
}

type scriptChunk struct {
	delay time.Duration
	bytes string
}

// buildMockScript returns a short ANSI sequence: clear screen, paint a
// banner with the (sandbox, id), draw a few colored lines, then move the
// cursor and animate a working spinner for a few frames. Just enough for
// a renderer to demonstrate it can handle color, redraws, and cursor
// motion.
func buildMockScript(sandbox, id string) []scriptChunk {
	const (
		reset = "\x1b[0m"
		bold  = "\x1b[1m"
		cyan  = "\x1b[36m"
		green = "\x1b[32m"
		yellow = "\x1b[33m"
		dim   = "\x1b[2m"
		clear = "\x1b[2J\x1b[H"
	)
	banner := fmt.Sprintf("%s%s[%s/%s]%s mock attach stream\r\n", bold, cyan, sandbox, id, reset)
	lines := []scriptChunk{
		{0, clear},
		{50 * time.Millisecond, banner},
		{40 * time.Millisecond, dim + "─────────────────────────────────\r\n" + reset},
		{60 * time.Millisecond, green + "✓ supervisor reached\r\n" + reset},
		{60 * time.Millisecond, green + "✓ session attached\r\n" + reset},
		{80 * time.Millisecond, yellow + "› running tool: read_file\r\n" + reset},
	}
	spinner := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	for i := 0; i < 24; i++ {
		frame := spinner[i%len(spinner)]
		lines = append(lines, scriptChunk{
			delay: 100 * time.Millisecond,
			bytes: fmt.Sprintf("\x1b[s\x1b[7;1H%s%s working%s\x1b[u", cyan, frame, reset),
		})
	}
	lines = append(lines,
		scriptChunk{200 * time.Millisecond, "\r\n" + green + "✓ done\r\n" + reset},
	)
	return lines
}

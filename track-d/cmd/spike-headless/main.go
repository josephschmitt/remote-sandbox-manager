// Headless fidelity check.
//
// The interactive spike (cmd/spike) needs a real TTY. This one drives
// the bubbleterm emulator directly with the same kind of ANSI traffic
// our backend produces, then prints the resulting frame rows to stdout
// for visual inspection. Useful in environments without a TTY, and as a
// regression check that the emulator handles the constructs we depend
// on: CSI cursor moves, SGR colors, alt-screen, save/restore, ED/EL,
// wide runes.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/taigrr/bubbleterm/emulator"
)

func main() {
	const cols, rows = 80, 24
	pr, pw := io.Pipe()
	emu, err := emulator.NewFromPipes(cols, rows, pr, nopWC{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "emulator:", err)
		os.Exit(1)
	}
	defer emu.Close()

	// Stream a sequence that exercises:
	//   - alt-screen enter (CSI ? 1049 h)
	//   - clear (CSI 2 J + CUP)
	//   - 256/true color SGR
	//   - save/restore cursor (DECSC/DECRC via ESC 7 / ESC 8)
	//   - cursor positioning
	//   - wide/CJK rune
	traffic := []string{
		"\x1b[?1049h",          // alt-screen
		"\x1b[2J\x1b[H",       // clear + home
		"\x1b[1;36mhello\x1b[0m world\r\n",
		"\x1b[38;5;208m256-color orange\x1b[0m\r\n",
		"\x1b[38;2;200;100;255mtrue-color violet\x1b[0m\r\n",
		"\x1b7\x1b[5;10H[saved]\x1b8",                       // save/move/restore
		"\x1b[10;1Hwide: 漢字 emoji: 🌟 box: ╔═╗\r\n",
		"\x1b[20;1Hcursor parked here\r\n",
	}
	go func() {
		defer pw.Close()
		for _, chunk := range traffic {
			_, _ = pw.Write([]byte(chunk))
			time.Sleep(10 * time.Millisecond)
		}
		time.Sleep(150 * time.Millisecond) // let the read loop drain
	}()

	// Let the read loop and parser catch up.
	time.Sleep(400 * time.Millisecond)
	frame := emu.GetScreen()

	fmt.Println("--- frame ---")
	for i, line := range frame.Rows {
		// Strip ANSI for a stable plain-text snapshot, but show length.
		plain := stripANSI(line)
		fmt.Printf("%2d | %s\n", i, strings.TrimRight(plain, " "))
	}
	fmt.Println("--- end ---")
	fmt.Printf("rows=%d damage=%d\n", len(frame.Rows), len(frame.Damage))
}

type nopWC struct{}

func (nopWC) Write(p []byte) (int, error) { return len(p), nil }
func (nopWC) Close() error                { return nil }

// stripANSI removes CSI/SGR escape sequences for readable inspection.
// Not a parser; just good enough for the snapshot.
func stripANSI(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != 0x1b {
			b.WriteByte(s[i])
			continue
		}
		// Skip ESC and look for terminator.
		if i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) {
				c := s[j]
				if c >= 0x40 && c <= 0x7e {
					j++
					break
				}
				j++
			}
			i = j - 1
			continue
		}
		// ESC X: skip ESC + 1 byte.
		i++
	}
	return b.String()
}

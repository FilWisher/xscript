package xscript

import (
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/hinshun/vt10x"
)

// Command is one shell command reconstructed from a recording.
type Command struct {
	Recording string
	Command   string
	Output    string
	Cwd       string
	ExitCode  *int
	StartedAt time.Time
	Duration  time.Duration
	// Truncated is set when output filled the virtual screen, so earlier
	// lines scrolled away and Output is only the tail.
	Truncated bool
}

// ReplayOptions configures the virtual terminal.
type ReplayOptions struct {
	// Cols must match the real terminal width, or line-editor redraws and
	// wrapping are replayed at the wrong positions.
	Cols int
	// Rows bounds how much output per command is kept; vt10x has no scrollback.
	Rows int
}

type position struct{ x, y int }

type replayer struct {
	recording string
	opts      ReplayOptions
	term      vt10x.Terminal
	splitter  oscSplitter
	utf8Carry []byte

	cwd         string
	inCommand   bool
	promptEnd   position
	outputStart position
	current     Command
	commands    []Command
}

// Replay rebuilds the completed commands in a recording, oldest first.
// Only output records are replayed: the shell's echo already reflects
// line editing, history recall and completion, whereas raw keystrokes don't.
func Replay(name string, records []Record, opts ReplayOptions) []Command {
	if opts.Cols == 0 {
		opts.Cols = 120
	}
	if opts.Rows == 0 {
		opts.Rows = 2000
	}
	r := &replayer{
		recording: name,
		opts: opts,
		term: vt10x.New(vt10x.WithSize(opts.Cols, opts.Rows)),
	}
	for _, rec := range records {
		if rec.Direction != DirOutput {
			continue
		}
		r.splitter.feed(rec.Data, r.write, func(payload string) {
			r.handleOSC(payload, rec.Time)
		})
	}
	log.Printf("returning %d commands", len(r.commands))
	return r.commands
}

// write feeds the emulator, holding back a multi-byte rune split across
// records: vt10x reports it as unwritten rather than buffering it.
func (r *replayer) write(b []byte) {
	if len(b) == 0 {
		return
	}
	buf := append(r.utf8Carry, b...)
	n, _ := r.term.Write(buf)
	r.utf8Carry = append([]byte(nil), buf[n:]...)
}

func (r *replayer) cursor() position {
	c := r.term.Cursor()
	return position{c.X, c.Y}
}

func (r *replayer) handleOSC(payload string, at time.Time) {
	if rest, ok := strings.CutPrefix(payload, "7;"); ok {
		if u, err := url.Parse(rest); err == nil {
			r.cwd = u.Path
		}
		return
	}

	parts := strings.Split(strings.TrimPrefix(payload, "133;"), ";")
	switch parts[0] {
	case "A": // prompt start
		r.scrollCursorRowToTop()
	case "B": // prompt end, command input starts
		r.promptEnd = r.cursor()
	case "C": // command submitted, output starts
		r.current = Command{
			Recording: r.recording,
			Command:   r.text(r.promptEnd, r.cursor()),
			Cwd:       r.cwd,
			StartedAt: at,
		}
		r.outputStart = r.cursor()
		r.inCommand = true
	case "D": // command finished; bash also emits this before the very first prompt
		if !r.inCommand {
			return
		}
		r.inCommand = false
		end := r.cursor()
		r.current.Output = r.text(r.outputStart, end)
		r.current.Duration = at.Sub(r.current.StartedAt)
		r.current.Truncated = end.y >= r.opts.Rows-1
		if len(parts) > 1 {
			if code, err := strconv.Atoi(parts[1]); err == nil {
				r.current.ExitCode = &code
			}
		}
		if r.current.Command != "" {
			r.commands = append(r.commands, r.current)
		}
	}
}

// scrollCursorRowToTop discards everything above the cursor's row and below
// it, so each command gets the whole screen for its output. The cursor's row
// is kept because the shell may already have drawn on it before the A marker,
// and readline's later redraws position themselves relative to that text.
func (r *replayer) scrollCursorRowToTop() {
	c := r.cursor()
	if c.y > 0 {
		r.write(fmt.Appendf(nil, "\x1b[1;1H\x1b[%dM", c.y)) // CUP home, then DL
	}
	r.write(fmt.Appendf(nil, "\x1b[2;1H\x1b[J\x1b[1;%dH", c.x+1)) // ED below, restore column
}

// text renders the screen between two cursor positions, trimming the
// padding spaces vt10x uses for empty cells.
func (r *replayer) text(from, to position) string {
	var lines []string
	for y := from.y; y <= to.y && y < r.opts.Rows; y++ {
		startX, endX := 0, r.opts.Cols
		if y == from.y {
			startX = from.x
		}
		if y == to.y {
			endX = to.x
		}
		var line strings.Builder
		for x := startX; x < endX; x++ {
			line.WriteRune(r.term.Cell(x, y).Char)
		}
		lines = append(lines, strings.TrimRight(line.String(), " "))
	}
	return strings.Trim(strings.Join(lines, "\n"), "\n")
}

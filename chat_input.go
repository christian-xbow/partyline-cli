package main

// The room's input line.
//
// The first version cleared only the screen row the cursor was on before redrawing. Input longer
// than the window wraps onto more rows, so every keystroke left the previous first row behind and
// filled the screen with copies of what was being typed. This editor remembers how many rows the
// input area takes, moves back to its top, and clears from there down before drawing.
//
// Keys: Enter sends, Alt-Enter adds a line break, ctrl-u clears, ctrl-w deletes a word, ctrl-c
// leaves. Pasted text arrives as one message with its line breaks, through the terminal's
// bracketed paste, instead of every line being sent as it lands.

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

const (
	chatPrompt        = "you › "
	pasteOn, pasteOff = "\x1b[?2004h", "\x1b[?2004l"
	pasteStart        = "\x1b[200~"
	pasteEnd          = "\x1b[201~"
)

type chatInput struct {
	buf     []rune
	status  []string // one line per session that has something going on
	rows    int      // screen rows the input area took when last drawn
	pasting bool     // between bracketed-paste start and end
	pending []byte   // an escape sequence split across reads
}

// clear returns the codes that move to the top of the input area and erase it.
func (in *chatInput) clear() string {
	s := ""
	if in.rows > 1 {
		s = fmt.Sprintf("\x1b[%dA", in.rows-1)
	}
	in.rows = 0
	return s + "\r\x1b[J"
}

// draw returns the codes that redraw the status line and input area for a window cols wide.
func (in *chatInput) draw(cols int) string {
	if cols < 10 {
		cols = 80
	}
	var b strings.Builder
	b.WriteString(in.clear())
	rows := 0
	for _, st := range in.status {
		b.WriteString("\x1b[2m" + runewidth.Truncate(st, cols-1, "…") + "\x1b[0m\r\n")
		rows++
	}
	pad := strings.Repeat(" ", runewidth.StringWidth(chatPrompt))
	for i, seg := range strings.Split(string(in.buf), "\n") {
		if i == 0 {
			b.WriteString("\x1b[1;33m" + chatPrompt + "\x1b[0m")
		} else {
			b.WriteString("\r\n" + pad)
		}
		b.WriteString(seg)
		w := runewidth.StringWidth(pad) + runewidth.StringWidth(seg)
		rows += max(1, (w+cols-1)/cols)
	}
	in.rows = rows
	return b.String()
}

// handle applies bytes read from the terminal. It returns the messages to send, and whether to
// leave the room.
func (in *chatInput) handle(b []byte) (send []string, quit bool) {
	b = append(in.pending, b...)
	in.pending = nil
	for len(b) > 0 {
		if in.pasting {
			end := strings.Index(string(b), pasteEnd)
			chunk := b
			if end >= 0 {
				chunk = b[:end]
			} else if k := partialSuffix(b, pasteEnd); k > 0 {
				// The end marker may be split across two reads; hold its start for the next one,
				// or paste mode never ends and Enter and ctrl-c stop working.
				in.pending = append([]byte(nil), b[len(b)-k:]...)
				chunk = b[:len(b)-k]
			}
			for _, r := range strings.ReplaceAll(strings.ReplaceAll(string(chunk), "\r\n", "\n"), "\r", "\n") {
				if r == '\n' || r == '\t' || r >= 32 {
					in.buf = append(in.buf, r)
				}
			}
			if end < 0 {
				return send, false
			}
			in.pasting, b = false, b[end+len(pasteEnd):]
			continue
		}
		if b[0] == 27 {
			n, complete := escapeLen(b)
			if !complete {
				in.pending = append([]byte(nil), b...)
				return send, false
			}
			seq := string(b[:n])
			b = b[n:]
			switch {
			case seq == pasteStart:
				in.pasting = true
			case seq == "\x1b\r" || seq == "\x1b\n":
				in.buf = append(in.buf, '\n') // Alt-Enter
			}
			continue // arrows and other keys are ignored
		}
		r, size := utf8.DecodeRune(b)
		b = b[size:]
		switch {
		case r == 3: // ctrl-c
			return send, true
		case r == 4 && len(in.buf) == 0: // ctrl-d on an empty line
			return send, true
		case r == 127 || r == 8:
			if len(in.buf) > 0 {
				in.buf = in.buf[:len(in.buf)-1]
			}
		case r == 21: // ctrl-u
			in.buf = in.buf[:0]
		case r == 23: // ctrl-w
			s := strings.TrimRight(string(in.buf), " ")
			if i := strings.LastIndexAny(s, " \n"); i >= 0 {
				in.buf = []rune(s[:i+1])
			} else {
				in.buf = in.buf[:0]
			}
		case r == '\r' || r == '\n':
			if text := strings.TrimSpace(string(in.buf)); text != "" {
				send = append(send, text)
			}
			in.buf = in.buf[:0]
		case r == '\t' || r >= 32:
			in.buf = append(in.buf, r)
		}
	}
	return send, false
}

// partialSuffix is the length of the longest proper prefix of marker that b ends with.
func partialSuffix(b []byte, marker string) int {
	for k := min(len(b), len(marker)-1); k > 0; k-- {
		if string(b[len(b)-k:]) == marker[:k] {
			return k
		}
	}
	return 0
}

// escapeLen is the length of the escape sequence at the start of b, and whether it is complete.
func escapeLen(b []byte) (int, bool) {
	if len(b) < 2 {
		return 0, false
	}
	switch b[1] {
	case '[': // CSI: parameters, then a final byte in 0x40-0x7e
		for i := 2; i < len(b); i++ {
			if b[i] >= 0x40 && b[i] <= 0x7e {
				return i + 1, true
			}
		}
		return 0, false
	case 'O': // SS3: one more byte
		if len(b) < 3 {
			return 0, false
		}
		return 3, true
	default: // Alt plus a key
		return 2, true
	}
}

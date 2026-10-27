package main

// How the room shows a message: a header line with who and when, then the text wrapped to the
// terminal and indented under it, with a blank line between messages. The first version printed
// each message as one line and left wrapping to the terminal, which turned a long reply into a
// wall of text with nothing separating one speaker from the next.

import (
	"fmt"
	"os"
	"strings"

	"github.com/mattn/go-runewidth"
	"golang.org/x/term"
)

const chatIndent = "  "

// chatSafe removes control characters other than line breaks and tabs. Message text comes from
// agents, which repeat web pages and files; an escape sequence in it would otherwise reach the
// terminal and could set the clipboard (OSC 52) or the window title.
func chatSafe(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\t':
			return r
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0):
			return -1
		}
		return r
	}, s)
}

func chatWidth() int {
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 20 {
		return w
	}
	return 100
}

// chatFormat renders one message for a terminal of the given width.
func chatFormat(m chatMessage, width int) string {
	m.Name, m.Text = chatSafe(m.Name), chatSafe(m.Text)
	ts := m.At.Local().Format("15:04")
	if m.Note {
		return fmt.Sprintf("\x1b[2m%s · %s\x1b[0m", m.Text, ts)
	}
	color := "36"
	if m.From == "you" {
		color = "33"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\x1b[1;%sm%s\x1b[0m \x1b[2m· %s\x1b[0m\n", color, m.Name, ts)
	b.WriteString(chatBody(m.Text, width))
	return strings.TrimRight(b.String(), "\n")
}

// chatBody renders a message body as Markdown (chat_markdown.go), wrapped to width and indented.
func chatBody(text string, width int) string {
	if width < 20 {
		width = 20
	}
	return renderMarkdown(text, chatIndent, width)
}

// wrapWords breaks text into lines no wider than width, on spaces.
func wrapWords(text string, width int) []string {
	if width < 10 {
		width = 10
	}
	var lines []string
	cur := ""
	for _, w := range strings.Fields(text) {
		switch {
		case cur == "":
			cur = w
		case runewidth.StringWidth(cur)+1+runewidth.StringWidth(w) <= width:
			cur += " " + w
		default:
			lines = append(lines, cur)
			cur = w
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

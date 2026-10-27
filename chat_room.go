package main

// The room's screen: messages above, the input area below (chat_input.go), and a background loop
// that prints new messages, refreshes the status lines and wakes idle members.

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

const chatEscWait = 40 * time.Millisecond

func chatDraftPath() string { return filepath.Join(chatDir(), "draft.txt") }

func chatRoomUI() {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		fatal(fmt.Errorf("ptln chat needs a terminal; use `ptln chat send` from scripts"))
	}
	_ = os.MkdirAll(chatDir(), 0o700)
	_ = withChatLock(func(*chatRoom) error { return nil }) // create the room so hooks see it
	old, err := term.MakeRaw(fd)
	if err != nil {
		fatal(err)
	}
	modes := func(on bool) {
		if on {
			fmt.Print(pasteOn + modifyKeysOn)
		} else {
			fmt.Print(modifyKeysOff + pasteOff)
		}
	}
	// Restore the terminal however the room ends: normally, on a signal, or on a panic in the
	// feed goroutine. Without this a crash leaves the terminal in raw mode with bracketed paste on.
	restore := func() {
		modes(false)
		_ = term.Restore(fd, old)
	}
	defer restore()
	modes(true)
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT)
	saveDraft := func() {}
	go func() {
		<-sigs
		restore()
		saveDraft()
		os.Exit(1)
	}()

	var mu sync.Mutex
	msgs := readChat()
	in := newChatInput(chatHistory(msgs))
	in.complete = chatComplete
	if b, err := os.ReadFile(chatDraftPath()); err == nil {
		in.ed.Set(string(b))
		in.ed.undo = nil
	}
	saveDraft = func() { // keep an unsent draft for next time
		if d := in.ed.String(); strings.TrimSpace(d) != "" {
			_ = os.WriteFile(chatDraftPath(), []byte(d), 0o600)
		} else {
			_ = os.Remove(chatDraftPath())
		}
	}
	defer func() { saveDraft() }()
	// printAbove puts text above the input area, then redraws the input under it.
	printAbove := func(s string) {
		fmt.Print(in.clear() + strings.ReplaceAll(s, "\n", "\r\n") + "\r\n" + in.draw(chatWidth()))
	}
	seen := 0
	showHistory := func() {
		fmt.Print("\x1b[2mpartyline chat · /add <tab> brings a session in · /help lists commands and keys · ctrl-c leaves\x1b[0m\r\n")
		all := readChat()
		for _, m := range all[max(0, len(all)-40):] {
			fmt.Print("\r\n" + strings.ReplaceAll(chatFormat(m, chatWidth()), "\n", "\r\n") + "\r\n")
		}
		if len(all) > 0 {
			seen = all[len(all)-1].Seq
		}
	}
	showHistory()
	fmt.Print(in.draw(chatWidth()))

	done, stopped := make(chan struct{}), make(chan struct{})
	go func() { defer close(stopped); chatFeed(done, &mu, in, &seen, printAbove, restore) }()
	defer func() { close(done); <-stopped }() // no message may print after the terminal is restored

	keys, pauseReader, resumeReader := chatKeyReader(fd)
	var escTimer <-chan time.Time
	for {
		var res chatInputResult
		select {
		case b, ok := <-keys:
			if !ok {
				return
			}
			mu.Lock()
			res = in.handle(b)
			mu.Unlock()
		case <-escTimer:
			mu.Lock()
			res = in.flushEsc()
			mu.Unlock()
		}
		escTimer = nil
		mu.Lock()
		if in.escPending() {
			escTimer = time.After(chatEscWait) // the Esc key, unless the rest of a sequence follows
		}
		for _, text := range res.send {
			chatSubmit(text, printAbove, func() { fmt.Print("\x1b[2J\x1b[H"); showHistory() })
		}
		if res.clear {
			fmt.Print(in.clear() + "\x1b[2J\x1b[H")
		}
		if res.editor {
			pauseReader() // the editor reads the terminal now; the room must not take its keys
			// Clear the input area first: an editor that switches screens restores this one on
			// exit, and would bring the old input rows back with it.
			fmt.Print(in.clear())
			restore()
			// The terminal is in normal mode while the editor runs, so ctrl-c raises SIGINT; it is
			// meant for the editor, not the room.
			signal.Ignore(syscall.SIGINT)
			edited, err := chatEditExternally(in.ed.String())
			signal.Notify(sigs, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT)
			resumeReader()
			_, _ = term.MakeRaw(fd)
			modes(true)
			in.rows, in.curRow = 0, 0
			if err != nil {
				in.hint = "editor: " + err.Error()
			} else {
				in.ed.Set(strings.TrimRight(edited, "\n"))
			}
		}
		fmt.Print(in.draw(chatWidth()))
		mu.Unlock()
		if res.quit {
			fmt.Print("\r\n")
			return
		}
	}
}

// chatFeed prints new messages, refreshes the status lines, and wakes idle members.
func chatFeed(done chan struct{}, mu *sync.Mutex, in *chatInput, seen *int, printAbove func(string), restore func()) {
	defer func() {
		if p := recover(); p != nil {
			mu.TryLock() // keep other writers off the screen while it is restored
			restore()
			fmt.Fprintf(os.Stderr, "\nptln chat: %v\n", p)
			os.Exit(1)
		}
	}()
	tries, wake := map[string]time.Time{}, map[string]string{}
	var wakeMu sync.Mutex // guards wake only, and only ever briefly
	var waking atomic.Bool
	setWake := func(key, why string, drop bool) {
		wakeMu.Lock()
		defer wakeMu.Unlock()
		if drop {
			delete(wake, key)
		} else {
			wake[key] = why
		}
	}
	act := map[string]memberActivity{}  // guarded by wakeMu
	busySince := map[string]time.Time{} // when each member was first seen working; probe goroutine only
	var freeNotes []string              // guarded by wakeMu, printed by the feed
	probe := func() {                   // what each member is doing in its tab, and who just became free
		room := loadChatRoom()
		panes := chatPanes()
		now := time.Now()
		next := map[string]memberActivity{}
		var notes []string
		for _, m := range room.Members {
			p, ok := chatPaneFor(panes, m.Key)
			if !ok {
				continue
			}
			a := probeActivity(p)
			next[m.Key] = a
			if a.State == "working" {
				if busySince[m.Key].IsZero() {
					busySince[m.Key] = now
				}
			} else if since := busySince[m.Key]; !since.IsZero() {
				if worked := now.Sub(since); worked >= 30*time.Second && a.State == "idle" {
					notes = append(notes, chatFreeNote(m.Name, worked))
				}
				delete(busySince, m.Key)
			}
		}
		wakeMu.Lock()
		act = next
		freeNotes = append(freeNotes, notes...)
		wakeMu.Unlock()
	}
	statusNow := func() ([]string, bool, []string) {
		msgs := readChat()
		room := loadChatRoom()
		wakeMu.Lock()
		defer wakeMu.Unlock()
		notes := freeNotes
		freeNotes = nil
		lines, spin := chatStatusLines(&room, msgs, wake, act, time.Now())
		return lines, spin, notes
	}
	spinning := false
	for tick := 0; ; tick++ {
		select {
		case <-done:
			return
		case <-time.After(100 * time.Millisecond):
		}
		if tick%5 == 0 || (spinning && tick%2 == 0) { // new messages twice a second; spinners five times

			msgs := readChat()
			lines, spin, notes := statusNow()
			mu.Lock()
			for _, m := range msgs {
				if m.Seq > *seen {
					*seen = m.Seq
					printAbove("\n" + chatFormat(m, chatWidth()))
				}
			}
			for _, n := range notes {
				printAbove(n)
			}
			changed := strings.Join(lines, "\n") != strings.Join(in.status, "\n")
			in.status, spinning = lines, spin
			if changed || spin {
				fmt.Print(in.draw(chatWidth()))
			}
			mu.Unlock()
		}
		if tick%20 == 0 && waking.CompareAndSwap(false, true) { // every two seconds, one pass at a time
			go func() {
				defer waking.Store(false)
				probe()
				chatWakeIdle(tries, setWake)
			}()
		}
	}
}

// chatEditExternally opens $VISUAL or $EDITOR (vi if neither) on text and returns what was saved.
func chatEditExternally(text string) (string, error) {
	f, err := os.CreateTemp("", "ptln-chat-*.md")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(text); err != nil {
		f.Close()
		return "", err
	}
	f.Close()
	editor := strings.TrimSpace(os.Getenv("VISUAL"))
	if editor == "" {
		editor = strings.TrimSpace(os.Getenv("EDITOR"))
	}
	if editor == "" {
		editor = "vi"
	}
	args := strings.Fields(editor)
	cmd := exec.Command(args[0], append(args[1:], f.Name())...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return "", err
	}
	b, err := os.ReadFile(f.Name())
	return string(b), err
}

// chatHistory is what you sent in this room, oldest first, for ↑ and ctrl-r.
func chatHistory(msgs []chatMessage) []string {
	var h []string
	for _, m := range msgs {
		if m.From == "you" && (len(h) == 0 || h[len(h)-1] != m.Text) {
			h = append(h, m.Text)
		}
	}
	return h
}

var chatCommands = []string{"/add", "/remove", "/who", "/stop", "/go", "/clear", "/help"}

// chatComplete returns completions for a token: room commands at the start of the line, tab
// numbers after /add, member numbers after /remove /stop /go, and @mentions anywhere.
func chatComplete(token, before string) []string {
	var out []string
	add := func(c string) {
		if strings.HasPrefix(c, token) && c != token {
			out = append(out, c)
		}
	}
	first := strings.Fields(before)
	switch {
	case strings.TrimSpace(before) == "" && strings.HasPrefix(token, "/"):
		for _, c := range chatCommands {
			add(c)
		}
	case len(first) > 0 && first[0] == "/add":
		room := loadChatRoom()
		for _, p := range chatPanes() {
			if room.member(p.Key) == nil {
				add(strings.TrimPrefix(strings.Fields(p.Name)[0], "#"))
			}
		}
	case len(first) > 0 && (first[0] == "/remove" || first[0] == "/stop" || first[0] == "/go"):
		for _, m := range loadChatRoom().Members {
			add(strings.TrimPrefix(strings.Fields(m.Name)[0], "#"))
		}
	case strings.HasPrefix(token, "@"):
		room := loadChatRoom()
		add("@all")
		seen := map[string]bool{}
		for _, m := range room.Members {
			for _, c := range []string{"@" + strings.TrimPrefix(strings.Fields(m.Name)[0], "#"), "@" + m.Engine} {
				if !seen[c] {
					seen[c] = true
					add(c)
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, aerr := strconv.ParseFloat(strings.TrimLeft(out[i], "@/#"), 64)
		b, berr := strconv.ParseFloat(strings.TrimLeft(out[j], "@/#"), 64)
		if aerr == nil && berr == nil {
			return a < b
		}
		return out[i] < out[j]
	})
	return out
}

const chatRoomHelp = `commands: /add <tab>... · /remove <tab>... · /who · /stop [tab...] · /go [tab...] · /clear · /help
send: ⏎ · new line: Shift-⏎, Alt-⏎ or \ then ⏎ · address one session: @10 or @codex · Tab completes
edit: ←/→ · Option-←/→ or Alt-b/f by word · Home/End or ctrl-a/ctrl-e · ↑/↓ between lines, then history
delete: Backspace · Delete or ctrl-d · Option-Backspace or ctrl-w a word · ctrl-u to line start · ctrl-k to line end
also: ctrl-z or ctrl-_ undo · ctrl-r search what you sent · ctrl-g open $EDITOR · Esc Esc clear · ctrl-l clear screen · ctrl-c leave`

func chatSubmit(line string, say func(string), clearScreen func()) {
	f := strings.Fields(line)
	switch f[0] {
	case "/add":
		say("\x1b[2m" + chatAdd(f[1:]) + "\x1b[0m")
		return
	case "/remove", "/rm":
		say("\x1b[2m" + chatRemove(f[1:]) + "\x1b[0m")
		return
	case "/who":
		say("\x1b[2m" + strings.TrimRight(chatWho(), "\n") + "\x1b[0m")
		return
	case "/stop":
		say("\x1b[2m" + chatStop(f[1:], true) + "\x1b[0m")
		return
	case "/go":
		say("\x1b[2m" + chatStop(f[1:], false) + "\x1b[0m")
		return
	case "/clear":
		clearScreen()
		return
	case "/help":
		say("\x1b[2m" + chatRoomHelp + "\x1b[0m")
		return
	}
	unmatched := ""
	_ = withChatLock(func(r *chatRoom) error {
		to := chatAddressees(r, line)
		if to == nil && chatHasMention(line) {
			unmatched = "no session in the room matches the @ in that message; it was not sent (use @all for everyone)"
			return nil
		}
		_, err := postLocked(r, chatMessage{From: "you", Name: "you", To: to, Text: line})
		return err
	})
	if unmatched != "" {
		say("\x1b[2m" + unmatched + "\x1b[0m")
	}
}

// chatKeyReader reads the terminal on its own goroutine and sends what it reads on keys.
//
// It waits for input with select(2) in short steps and only calls read when input is ready, so it
// can be paused without being stuck inside a read. pause returns once the reader is idle; while
// paused it reads nothing. That is what lets ctrl-g hand the terminal to $EDITOR: a reader blocked
// in read would take some of the editor's keystrokes. (A read deadline would be simpler, but Go
// cannot poll a terminal on macOS, so deadlines never fire on one.)
func chatKeyReader(fd int) (keys chan []byte, pause, resume func()) {
	keys = make(chan []byte)
	var paused atomic.Bool
	idle := make(chan struct{}, 1)
	go func() {
		for {
			if paused.Load() {
				select {
				case idle <- struct{}{}:
				default:
				}
				time.Sleep(20 * time.Millisecond)
				continue
			}
			ready, err := ttyReadable(fd, 50*time.Millisecond)
			if err != nil {
				close(keys)
				return
			}
			if !ready || paused.Load() {
				continue
			}
			b := make([]byte, 4096)
			n, err := syscall.Read(fd, b)
			if err != nil || n == 0 {
				if err == syscall.EINTR || err == syscall.EAGAIN {
					continue
				}
				close(keys)
				return
			}
			keys <- b[:n]
		}
	}()
	pause = func() {
		select { // drop a stale idle signal from an earlier pause
		case <-idle:
		default:
		}
		paused.Store(true)
		<-idle
	}
	resume = func() { paused.Store(false) }
	return keys, pause, resume
}

// ttyReadable waits up to d for fd to have input.
func ttyReadable(fd int, d time.Duration) (bool, error) {
	var set unix.FdSet
	set.Set(fd)
	tv := unix.NsecToTimeval(d.Nanoseconds())
	n, err := unix.Select(fd+1, &set, nil, nil, &tv)
	if err == unix.EINTR {
		return false, nil
	}
	return n > 0, err
}

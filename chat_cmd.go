package main

// `ptln chat` — the room. The person types here; agents are added by their tab number and answer
// through their own hooks and `ptln chat send` (chat_hook.go). While the room is open it also
// wakes idle members that have messages waiting and no listener (chat_tmux.go).

import (
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/term"
)

const chatUsage = `usage:
  ptln chat                     open the room: type to talk, /add <tab> to bring a session in
  ptln chat add <tab>...        add sessions by their tab number (11, or 11.2 in a split)
  ptln chat remove <tab>...     take them out
  ptln chat who                 who is in the room
  ptln chat send "..."          post as this session (what agents run to reply)
  ptln chat read                print what is waiting for this session
  ptln chat listen              wait for a message, print it, exit (Claude runs it in the background)`

func chatMain(args []string) {
	if len(args) == 0 {
		chatRoomUI()
		return
	}
	switch args[0] {
	case "add":
		fmt.Println(chatAdd(args[1:]))
	case "remove", "rm":
		fmt.Println(chatRemove(args[1:]))
	case "who":
		fmt.Print(chatWho())
	case "send":
		chatSendSelf(strings.Join(args[1:], " "))
	case "read":
		chatReadSelf()
	case "listen":
		chatListen()
	case "-h", "--help", "help":
		fmt.Println(chatUsage)
	default:
		fatal(fmt.Errorf("ptln chat: unknown %q\n%s", args[0], chatUsage))
	}
}

// chatAdd brings sessions in by tab number. A new member is handed the last few messages so it
// joins the conversation rather than its next line.
func chatAdd(refs []string) string {
	if len(refs) == 0 {
		return "add which session? give its tab number, e.g. /add 11"
	}
	panes := chatPanes()
	var said []string
	_ = withChatLock(func(r *chatRoom) error {
		last := 0
		if msgs := readChat(); len(msgs) > 0 {
			last = msgs[len(msgs)-1].Seq
		}
		for _, ref := range refs {
			p, ok := chatPaneFor(panes, ref)
			if !ok {
				said = append(said, fmt.Sprintf("no claude or codex session in tab %s", ref))
				continue
			}
			if r.member(p.Key) != nil {
				said = append(said, p.Name+" is already here")
				continue
			}
			r.Members = append(r.Members, chatMember{Key: p.Key, Name: p.Name, Engine: p.Engine, Read: max(0, last-20), Joined: time.Now()})
			_, _ = postLocked(r, chatMessage{From: "partyline", Name: "partyline", Text: p.Name + " joined", Note: true})
			said = append(said, p.Name+" added")
		}
		return nil
	})
	return strings.Join(said, "\n")
}

func chatRemove(refs []string) string {
	var said []string
	_ = withChatLock(func(r *chatRoom) error {
		for _, ref := range refs {
			ref = strings.TrimPrefix(ref, "#")
			kept := r.Members[:0]
			for _, m := range r.Members {
				num := strings.TrimPrefix(strings.Fields(m.Name)[0], "#")
				if num == ref || m.Key == ref {
					_, _ = postLocked(r, chatMessage{From: "partyline", Name: "partyline", Text: m.Name + " left", Note: true})
					said = append(said, m.Name+" removed")
					continue
				}
				kept = append(kept, m)
			}
			r.Members = kept
		}
		return nil
	})
	if len(said) == 0 {
		return "nobody matched"
	}
	return strings.Join(said, "\n")
}

func chatWho() string {
	var b strings.Builder
	_ = withChatLock(func(r *chatRoom) error {
		if len(r.Members) == 0 {
			b.WriteString("nobody yet — /add <tab> brings a session in\n")
		}
		for _, m := range r.Members {
			state := "idle, no listener"
			if chatListening(m.Key) {
				state = "listening"
			}
			if m.Engine == "codex" {
				state = "woken when a message arrives"
			}
			fmt.Fprintf(&b, "%-14s %s\n", m.Name, state)
		}
		if r.paused() {
			fmt.Fprintf(&b, "agents have posted %d messages in a row; they wait for you to say something\n", r.AgentRun)
		}
		return nil
	})
	return b.String()
}

// chatSendSelf posts as the calling session. Run by agents.
func chatSendSelf(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		fatal(fmt.Errorf("usage: ptln chat send \"message\""))
	}
	keys := chatSelfKeys("")
	err := withChatLock(func(r *chatRoom) error {
		me := r.self(keys)
		if me == nil {
			return fmt.Errorf("this session is not in the chat")
		}
		me.Sent, me.Active = true, time.Now()
		_, err := postLocked(r, chatMessage{From: me.Key, Name: me.Name, To: chatAddressees(r, text), Text: text})
		return err
	})
	if err != nil {
		fatal(err)
	}
	fmt.Println("sent")
}

func chatReadSelf() {
	keys := chatSelfKeys("")
	text := ""
	_ = withChatLock(func(r *chatRoom) error {
		me := r.self(keys)
		if me == nil {
			text = "this session is not in the chat\n"
			return nil
		}
		if msgs := unreadFor(readChat(), *me); len(msgs) > 0 {
			me.Read, me.Answering, me.Sent, me.Active, me.Delivered = msgs[len(msgs)-1].Seq, true, false, time.Now(), time.Now()
			text = chatDeliveryText(*me, msgs, r)
		} else {
			text = "nothing new in the chat\n"
		}
		return nil
	})
	fmt.Print(text)
}

// chatWakeQuiet is how long after a member last received, sent or started listening before the
// room will type a wake line into it. In the first interactive test a message landed in the second
// between a listener finishing and the next one starting, and the wake was typed into a session
// that was busy replying.
const chatWakeQuiet = time.Minute

// chatWakeLine is all that is ever typed into a session: a pointer to `ptln chat read`.
const chatWakeLine = "[partyline chat] You have new messages in the partyline chat. Run `ptln chat read` and follow it."

// chatWakeIdle wakes members that have messages waiting and nothing listening. wake records, per
// member, why the last wake was held back ("" once it went in), for the room's status lines; a
// member with nothing waiting is dropped from it.
func chatWakeIdle(lastTry map[string]time.Time, setWake func(key, why string, drop bool)) {
	type want struct{ key, name string }
	var due []want
	_ = withChatLock(func(r *chatRoom) error {
		msgs := readChat()
		for _, m := range r.Members {
			if len(unreadFor(msgs, m)) == 0 {
				setWake(m.Key, "", true)
				continue
			}
			if r.paused() || (m.Engine == "claude" && chatListening(m.Key)) {
				continue
			}
			quiet := chatWakeQuiet
			if m.Engine == "codex" {
				quiet = 10 * time.Second // no listener gap to cover; its "Working (" line shows it is busy
			}
			if time.Since(m.Active) < quiet {
				continue // mid-conversation: its listener or its turn will pick this up
			}
			due = append(due, want{m.Key, m.Name})
		}
		return nil
	})
	if len(due) == 0 {
		return
	}
	panes := chatPanes()
	for _, d := range due {
		if time.Since(lastTry[d.key]) < 20*time.Second {
			continue
		}
		lastTry[d.key] = time.Now()
		p, ok := chatPaneFor(panes, d.key)
		switch {
		case !ok:
			setWake(d.key, "its tab is gone", false)
		default:
			if err := wakePane(p.ID, chatWakeLine); err != nil {
				setWake(d.key, err.Error(), false)
			} else {
				setWake(d.key, "", false)
			}
		}
	}
}

// --- the room's screen (message layout is in chat_format.go) ------------------------------------

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
	// Restore the terminal however the room ends: normally, on a signal, or on a panic in the
	// feed goroutine. Without this a crash leaves the terminal in raw mode with bracketed paste on.
	restore := func() {
		fmt.Print(pasteOff)
		_ = term.Restore(fd, old)
	}
	defer restore()
	fmt.Print(pasteOn)
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGINT)
	go func() {
		<-sigs
		restore()
		os.Exit(1)
	}()

	var mu sync.Mutex
	in := &chatInput{}
	// printAbove puts text above the input area, then redraws the input under it.
	printAbove := func(s string) {
		fmt.Print(in.clear() + strings.ReplaceAll(s, "\n", "\r\n") + "\r\n" + in.draw(chatWidth()))
	}

	msgs := readChat()
	seen := 0
	fmt.Print("\x1b[2mpartyline chat · /add <tab> brings a session in · /who · /remove <tab> · Alt-Enter for a new line · ctrl-c leaves\x1b[0m\r\n")
	for _, m := range msgs[max(0, len(msgs)-40):] {
		fmt.Print("\r\n" + strings.ReplaceAll(chatFormat(m, chatWidth()), "\n", "\r\n") + "\r\n")
	}
	if len(msgs) > 0 {
		seen = msgs[len(msgs)-1].Seq
	}
	fmt.Print(in.draw(chatWidth()))

	done := make(chan struct{})
	go func() { // the feed, the status lines, and waking idle members
		defer func() {
			if p := recover(); p != nil {
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
		statusNow := func() ([]string, bool) {
			msgs := readChat()
			room := loadChatRoom()
			wakeMu.Lock()
			defer wakeMu.Unlock()
			return chatStatusLines(&room, msgs, wake, time.Now())
		}
		spinning := false
		for tick := 0; ; tick++ {
			select {
			case <-done:
				return
			case <-time.After(100 * time.Millisecond):
			}
			if tick%5 == 0 { // twice a second: new messages and member state
				msgs := readChat()
				lines, spin := statusNow()
				mu.Lock()
				for _, m := range msgs {
					if m.Seq > seen {
						seen = m.Seq
						printAbove("\n" + chatFormat(m, chatWidth()))
					}
				}
				changed := strings.Join(lines, "\n") != strings.Join(in.status, "\n")
				in.status, spinning = lines, spin
				if changed {
					fmt.Print(in.draw(chatWidth()))
				}
				mu.Unlock()
			} else if spinning { // animate the spinners in between
				lines, spin := statusNow()
				mu.Lock()
				in.status, spinning = lines, spin
				fmt.Print(in.draw(chatWidth()))
				mu.Unlock()
			}
			if tick%20 == 0 && waking.CompareAndSwap(false, true) { // every two seconds, one pass at a time
				go func() {
					defer waking.Store(false)
					chatWakeIdle(tries, setWake)
				}()
			}
		}
	}()
	defer close(done)

	buf := make([]byte, 4096)
	for {
		n, err := os.Stdin.Read(buf)
		if err != nil {
			return
		}
		mu.Lock()
		send, quit := in.handle(buf[:n])
		for _, text := range send {
			chatSubmit(text, printAbove)
		}
		fmt.Print(in.draw(chatWidth()))
		mu.Unlock()
		if quit {
			fmt.Print("\r\n")
			return
		}
	}
}

func chatSubmit(line string, say func(string)) {
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
	case "/help":
		say("\x1b[2m" + chatUsage + "\x1b[0m")
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

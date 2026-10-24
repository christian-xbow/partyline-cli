package main

// The live line per session above the input: where your message is and what each session is
// doing with it. Without it, nothing on screen changed between sending a message and a reply
// arriving, which could be a minute.

import (
	"fmt"
	"hash/fnv"
	"time"
)

var chatVerbs = []string{
	"thinking", "pondering", "mulling", "tinkering", "noodling", "chewing on it", "puzzling",
	"working it out", "sketching", "weighing it", "digging in", "untangling", "cooking",
	"brewing", "reasoning", "considering", "drafting", "checking", "piecing it together",
	"turning it over", "crunching", "sifting", "musing", "scheming", "assembling",
}

const chatSpinner = "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"

// chatVerb picks a verb for one turn: the same for the whole turn, different from turn to turn.
func chatVerb(key string, turn int) string {
	h := fnv.New32a()
	fmt.Fprintf(h, "%s/%d", key, turn)
	return chatVerbs[int(h.Sum32())%len(chatVerbs)]
}

func chatSpinFrame(now time.Time) string {
	frames := []rune(chatSpinner)
	return string(frames[int(now.UnixMilli()/100)%len(frames)])
}

// chatStatusLines describes every member that has something going on. wake holds the last reason
// a wake was held back for a member ("" once woken). The second result is true while any line is
// animated, so the room knows to keep redrawing.
func chatStatusLines(r *chatRoom, msgs []chatMessage, wake map[string]string, now time.Time) ([]string, bool) {
	var lines []string
	spinning := false
	spin := chatSpinFrame(now)
	for _, m := range r.Members {
		waiting := len(unreadFor(msgs, m)) > 0
		switch {
		case m.Answering && !m.Sent:
			spinning = true
			lines = append(lines, fmt.Sprintf("%s %s · %s… %s", spin, m.Name, chatVerb(m.Key, m.Read), chatElapsed(now.Sub(m.Delivered))))
		case waiting && r.paused():
			// said once below
		case waiting && chatListening(m.Key):
			spinning = true
			lines = append(lines, fmt.Sprintf("%s %s · receiving", spin, m.Name))
		case waiting:
			why, tried := wake[m.Key]
			switch {
			case tried && why != "":
				lines = append(lines, fmt.Sprintf("· %s · waiting: %s", m.Name, why))
			case tried:
				spinning = true
				lines = append(lines, fmt.Sprintf("%s %s · waking up", spin, m.Name))
			default:
				spinning = true
				lines = append(lines, fmt.Sprintf("%s %s · gets it when its current turn ends", spin, m.Name))
			}
		case m.Sent && now.Sub(m.Active) < 4*time.Second:
			lines = append(lines, fmt.Sprintf("✓ %s · replied", m.Name))
		}
	}
	if r.paused() {
		lines = append(lines, fmt.Sprintf("· the sessions posted %d messages in a row; they wait for you to say something", r.AgentRun))
	}
	return lines, spinning
}

func chatElapsed(d time.Duration) string {
	if d < 0 || d > 24*time.Hour {
		return ""
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

package main

// helpMain prints `ptln help` — the index, not the manual.
//
// It used to be a 2,600-word hand-written blob that restated each command's flags, the mux keys,
// the session-manager keys, the daemon's subcommands and the MCP tool list. Every one of those
// facts already exists in a declaration somewhere, so the blob's only real function was to
// disagree with them: it advertised deleted and never-built commands while never mentioning the
// ones that existed. The prose that remains below is covered by TestHelpNamesNoPhantomCommand.
//
// So it renders the declarations instead, and points at the places that document themselves —
// `ptln <command> --help`, the ctrl-\ menu, and `?` in the session manager.

import (
	"fmt"
	"os"

	"partyline.sh/partyline/internal/clispec"
)

func helpMain() {
	w := os.Stdout
	fmt.Fprint(w, `partyline — a session manager for AI coding agents, repository memory, and a chat between sessions

USAGE
  ptln                   open the session manager
  ptln new <engine>      start a session: claude, codex, gemini, opencode, goose or antigravity
  ptln --resume          reopen the sessions you had open last time
  ptln memory build      set up repository memory in this repo
  ptln chat              open the chat room
  ptln <command> --help  usage, subcommands and flags for one command

  The binary is "partyline"; "ptln" is a symlink to it.
  There is no server and no account. partyline.sh serves the documentation and the installer.

COMMANDS
`)
	clispec.WriteIndex(w, "  ")
	fmt.Fprint(w, `
KEYS
  ctrl-\        in a session: open the menu, which lists every command and its key.
                ←/→ or 1-9 switch sessions. ctrl-\ twice sends a literal ctrl-\.
  ?             in the session manager: list every key.

DOCS
  ptln man                             the manual
  https://partyline.sh/docs            the documentation
  https://partyline.sh/llms-full.txt   the documentation as one plain-text file, for an AI assistant
`)
}

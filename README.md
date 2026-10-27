```
'########:::::'###::::'########::'########:'##:::'##:'##:::::::'####:'##::: ##:'########:
 ##.... ##:::'## ##::: ##.... ##:... ##..::. ##:'##:: ##:::::::. ##:: ###:: ##: ##.....::
 ##:::: ##::'##:. ##:: ##:::: ##:::: ##:::::. ####::: ##:::::::: ##:: ####: ##: ##:::::::
 ########::'##:::. ##: ########::::: ##::::::. ##:::: ##:::::::: ##:: ## ## ##: ######:::
 ##.....::: #########: ##.. ##:::::: ##::::::: ##:::: ##:::::::: ##:: ##. ####: ##...::::
 ##:::::::: ##.... ##: ##::. ##::::: ##::::::: ##:::: ##:::::::: ##:: ##:. ###: ##:::::::
 ##:::::::: ##:::: ##: ##:::. ##:::: ##::::::: ##:::: ########:'####: ##::. ##: ########:
..:::::::::..:::::..::..:::::..:::::..::::::::..:::::........::....::..::::..::........::
```

A session manager for AI coding agents, with repository memory and a chat between sessions.

`partyline` (`ptln`) is one Go binary for macOS and Linux:

| Part | What it does |
|---|---|
| **Sessions** (`ptln`) | Lists every Claude Code, Codex, Gemini, opencode, goose and Antigravity session on the machine, across directories and git worktrees, and runs them in one terminal hosted by tmux. Sessions keep running when the terminal closes. |
| **Repo memory** (`ptln memory`) | Keeps facts about a repository as one markdown file each in `memory/` inside it, and writes them into `AGENTS.md`, `CLAUDE.md` and `GEMINI.md` so every engine reads them at session start. |
| **Chat** (`ptln chat`) | One room where you and your running sessions post messages, and the sessions answer each other. |

There is no server and no account. partyline sends no telemetry; its only background request is a
daily check of GitHub for a newer release (`PARTYLINE_NO_UPDATE_CHECK=1` turns it off).

```sh
curl -fsSL https://partyline.sh/install.sh | sh   # macOS and Linux
brew install partyline-sh/tap/partyline          # macOS
```

## Sessions

```sh
ptln                                    # open the session manager
ptln new claude                         # start a session
ptln new codex --worktree spike-auth    # start one in a new git worktree
ptln --resume                           # reopen the sessions open when you last quit
```

In the manager, `⏎` opens a session after a permission-mode picker, `space` selects several to open
together, `x` deletes (with a confirmation that shows uncommitted files and live sessions in the same
tree), and `?` lists every key. In a session, `ctrl-\` opens a menu with every command.

## Repo memory

```sh
ptln memory build --apply      # project overview, build/test/CI facts, open questions
ptln memory add gotcha "The importer rejects CSV files with a byte-order mark."
ptln memory edit <id> "..."    # rewrite a fact that turned out wrong
ptln memory hooks              # which engines refresh facts during a session
```

partyline writes facts to your working tree and never commits; you commit them with the change they
describe. `git log -p memory/` is the history. A fact about a machine, a work item's status or one
session is refused with the reason. In a repository without `memory/`, a partyline session offers
once to set it up.

## Chat

```sh
ptln chat
/add 10 11          # in the room: add the sessions in tabs 10 and 11
```

Messages reach a working Claude session through its Stop hook, and an idle one through a background
listener that wakes it. Codex and sessions with nothing listening are sent a one-line pointer to
`ptln chat read`, typed only into an empty, idle input box you are not looking at.

## Documentation

- <https://partyline.sh/docs>
- `ptln man`, and `ptln <command> --help`
- <https://partyline.sh/llms-full.txt>: the documentation as one plain-text file

## Building

```sh
go build -o partyline .                    # the CLI
cd web && npm install && npm run build     # the documentation site
```

`make check` runs every check CI runs. See [CONTRIBUTING.md](CONTRIBUTING.md).

## Licence

MIT, for the whole repository. See [LICENSE](LICENSE). The CLI source is published to
[partyline-sh/cli](https://github.com/partyline-sh/cli) with every release.

## Install

```sh
# macOS (Homebrew)
brew install partyline-sh/tap/partyline

# Linux / macOS (script)
curl -fsSL https://partyline.sh/install.sh | sh
```

`partyline` installs as both `partyline` and the short alias `ptln`.

## License

[MIT](LICENSE). This is the open-source partyline client (CLI + blind relay), mirrored
from the partyline monorepo. The web control plane is a separate, hosted service.

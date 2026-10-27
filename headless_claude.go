package main

import (
	"context"
	"os/exec"
)

// headlessClaude is a non-interactive claude run that gets only what partyline gives it. Each flag
// closes a hole found by running claude 2.1.311 on 2026-10-27:
//
//   - --restricted: without it, `claude -p` loads the repository's .claude/settings.json and runs
//     that repository's hooks with no trust prompt. Restricted mode ignores user, project and
//     local settings files, confines the file tools to the working directory (a Read of a file
//     outside it is refused), and refuses bypassPermissions.
//   - --strict-mcp-config: --tools limits only the built-in tools. Every MCP server in the user's
//     settings stayed available, including ones that write (ticket and note creation).
//   - --tools: the complete built-in tool list, "" for none.
//
// Stdin stays nil, which exec connects to /dev/null: claude otherwise waits for piped input.
func headlessClaude(ctx context.Context, dir, prompt, tools string, args ...string) *exec.Cmd {
	full := append([]string{"-p", prompt, "--restricted", "--strict-mcp-config", "--tools", tools}, args...)
	cmd := exec.CommandContext(ctx, "claude", full...)
	cmd.Dir = dir
	return cmd
}

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEngineSessionNames(t *testing.T) {
	dir := t.TempDir()
	cl := filepath.Join(dir, "c.jsonl")
	os.WriteFile(cl, []byte(`{"type":"user","cwd":"/x","message":{"role":"user","content":"fix the login bug please"}}
{"type":"custom-title","customTitle":"Auth work","sessionId":"s"}
{"type":"user","message":{"role":"user","content":"now add tests"}}
`), 0o600)
	if _, title, _ := scanClaude(cl); title != "Auth work" {
		t.Fatalf("claude title = %q, want the /rename name", title)
	}
	renameClaudeSession(aiSession{ID: "s", storePath: cl}, "Renamed in ptln")
	if _, title, _ := scanClaude(cl); title != "Renamed in ptln" {
		t.Fatalf("after ptln rename, claude title = %q", title)
	}
	_, title, named := scanClaude(cl)
	meta := map[string]sessMeta{"s": {Name: "older ptln name"}}
	if n := sessionName(aiSession{ID: "s", Title: title, Named: named}, meta); n != "Renamed in ptln" {
		t.Fatalf("engine name must beat the sidecar, got %q", n)
	}

	os.MkdirAll(filepath.Join(dir, ".codex"), 0o700)
	os.WriteFile(filepath.Join(dir, ".codex", "session_index.jsonl"), []byte(`{"id":"a","thread_name":"old"}
{"id":"a","thread_name":"\"New name\""}
`), 0o600)
	if n := codexThreadNames(dir)["a"]; n != "New name" {
		t.Fatalf("codex name = %q", n)
	}
	if !renameCodexSession(aiSession{Tool: "codex", ID: "a", root: dir}, "it's ptln's") || codexThreadNames(dir)["a"] != "it's ptln's" {
		t.Fatalf("after ptln rename, codex name = %q", codexThreadNames(dir)["a"])
	}

}

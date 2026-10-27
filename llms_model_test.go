package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSessionModel(t *testing.T) {
	dir := t.TempDir()
	cl := filepath.Join(dir, "c.jsonl")
	os.WriteFile(cl, []byte(`{"type":"assistant","message":{"role":"assistant","model":"claude-opus-5","content":"hi"}}
{"type":"assistant","message":{"role":"assistant","model":"<synthetic>","content":"API error"}}
`), 0o600)
	var d aiDetail
	readJSONLDetail(aiSession{storePath: cl}, &d, claudeLineMsg)
	if d.Model != "claude-opus-5" {
		t.Fatalf("claude model = %q", d.Model)
	}

	cx := filepath.Join(dir, "x.jsonl")
	os.WriteFile(cx, []byte(`{"type":"turn_context","payload":{"model":"gpt-a"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}}
{"type":"turn_context","payload":{"model":"gpt-b"}}
`), 0o600)
	d = aiDetail{}
	readJSONLDetail(aiSession{storePath: cx}, &d, codexLineMsg)
	if d.Model != "gpt-b" {
		t.Fatalf("codex model = %q, want the latest turn's", d.Model)
	}
}

package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func body(t *testing.T, r seenRequest) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(r.Body, &m); err != nil {
		t.Fatalf("request body is not JSON: %s", r.Body)
	}
	return m
}

func dig(v any, path ...string) any {
	for _, p := range path {
		switch x := v.(type) {
		case map[string]any:
			v = x[p]
		case []any:
			if len(x) == 0 {
				return nil
			}
			v = x[0].(map[string]any)[p]
		}
	}
	return v
}

func writeSite(t *testing.T) *fakeSite {
	s := newSite(t)
	s.json("GET /rest/api/content/42", `{"id":"42","type":"page","title":"Runbook","space":{"key":"OPS"},"version":{"number":7},
		"ancestors":[{"id":"1","type":"page"}],
		"body":{"storage":{"value":"<p>old</p>","representation":"storage"}},"_links":{"webui":"/pages/viewpage.action?pageId=42"}}`)
	s.json("GET /rest/api/content/50", `{"id":"50","type":"page","title":"Lossy","space":{"key":"OPS"},"version":{"number":2},
		"body":{"storage":{"value":"<ac:layout><ac:layout-section><ac:layout-cell><p>x</p></ac:layout-cell></ac:layout-section></ac:layout>"}}}`)
	s.json("GET /rest/api/content/9", `{"id":"9","type":"page","title":"New Parent","space":{"key":"OPS"}}`)
	s.json("GET /rest/api/content/8", `{"id":"8","type":"page","title":"Elsewhere","space":{"key":"DEV"}}`)
	s.json("POST /rest/api/content", `{"id":"100","type":"page","title":"Created","space":{"key":"OPS"},"version":{"number":1},"_links":{"webui":"/pages/viewpage.action?pageId=100"}}`)
	s.json("PUT /rest/api/content/42", `{"id":"42","type":"page","title":"Runbook","space":{"key":"OPS"},"version":{"number":8},"_links":{"webui":"/pages/viewpage.action?pageId=42"}}`)
	s.json("PUT /rest/api/content/50", `{"id":"50","type":"page","title":"Lossy","version":{"number":3}}`)
	return s
}

func TestReadOnlyBlocksWrites(t *testing.T) {
	s := writeSite(t)
	env := s.env(t, "CONFLUENCE_READ_ONLY", "true")
	cases := [][]string{
		{"create", "T", "OPS", "-c", "<p>x</p>"},
		{"update", "42", "-c", "<p>x</p>"},
		{"move", "42", "9"},
		{"comment", "42", "-c", "<p>x</p>"},
		{"property-set", "42", "k", "-v", "1"},
	}
	for _, args := range cases {
		r := run(t, env, "", append(args, "--json")...)
		if r.code == 0 || !strings.Contains(r.stderr, `"read_only"`) {
			t.Errorf("%v in read-only mode: %+v", args, r)
		}
	}
	if n := len(s.requests(http.MethodPost, "")) + len(s.requests(http.MethodPut, "")); n != 0 {
		t.Errorf("%d write requests reached the server in read-only mode", n)
	}
	mustSucceed(t, run(t, env, "", "read", "42", "-f", "storage"))
	mustSucceed(t, run(t, env, "", "edit", "42"))
}

func TestCreateFromMarkdown(t *testing.T) {
	s := writeSite(t)
	r := mustSucceed(t, run(t, s.env(t), "# Hello\n\n> **INFO**\n> Heads up", "create", "Created", "OPS", "-f", "-", "--format", "markdown"))
	assertContains(t, r.stdout, "100", s.srv.URL+"/pages/viewpage.action?pageId=100")
	b := body(t, s.requests(http.MethodPost, "/rest/api/content")[0])
	storage, _ := dig(b, "body", "storage", "value").(string)
	if !strings.Contains(storage, "<h1>Hello</h1>") || !strings.Contains(storage, `ac:name="info"`) || dig(b, "space", "key") != "OPS" {
		t.Errorf("create body = %v", b)
	}
}

func TestCreateChildUsesParentSpace(t *testing.T) {
	s := writeSite(t)
	mustSucceed(t, run(t, s.env(t), "", "create-child", "Kid", "9", "-c", "<p>x</p>"))
	b := body(t, s.requests(http.MethodPost, "/rest/api/content")[0])
	if dig(b, "ancestors", "id") != "9" || dig(b, "space", "key") != "OPS" || dig(b, "title") != "Kid" {
		t.Errorf("create-child body = %v", b)
	}
}

func TestCreateRejectsInvalidStorage(t *testing.T) {
	s := writeSite(t)
	r := run(t, s.env(t), "", "create", "T", "OPS", "-c", "<p>unclosed", "--json")
	if r.code == 0 || !strings.Contains(r.stderr, "invalid_input") {
		t.Errorf("invalid storage should be refused locally: %+v", r)
	}
	if len(s.requests(http.MethodPost, "")) != 0 {
		t.Error("invalid storage was sent")
	}
	if r := run(t, s.env(t), "", "create", "T", "OPS"); r.code == 0 {
		t.Error("create without content should fail")
	}
}

func TestUpdate(t *testing.T) {
	s := writeSite(t)
	file := filepath.Join(t.TempDir(), "page.xml")
	if err := os.WriteFile(file, []byte("<p>new</p>"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := mustSucceed(t, run(t, s.env(t), "", "update", "42", "-f", file))
	assertContains(t, r.stdout, "8")
	b := body(t, s.requests(http.MethodPut, "/rest/api/content/42")[0])
	if dig(b, "version", "number") != float64(8) || dig(b, "body", "storage", "value") != "<p>new</p>" || dig(b, "title") != "Runbook" {
		t.Errorf("update body = %v", b)
	}

	mustSucceed(t, run(t, s.env(t), "", "update", "42", "-t", "Renamed"))
	b = body(t, s.requests(http.MethodPut, "/rest/api/content/42")[1])
	if dig(b, "title") != "Renamed" || dig(b, "body", "storage", "value") != "<p>old</p>" {
		t.Errorf("title-only update body = %v", b)
	}
	if r := run(t, s.env(t), "", "update", "42"); r.code == 0 {
		t.Error("update with nothing to change should fail")
	}
}

func TestUpdateDryRunSendsNothing(t *testing.T) {
	s := writeSite(t)
	r := mustSucceed(t, run(t, s.env(t), "", "update", "42", "-c", "<p>new</p>", "--dry-run"))
	assertContains(t, r.stdout, "dry run", "Runbook", "7", "8")
	if len(s.requests(http.MethodPut, "")) != 0 {
		t.Error("dry run sent a PUT")
	}
}

func TestUpdateFromMarkdownGuardsLossyPages(t *testing.T) {
	s := writeSite(t)
	r := run(t, s.env(t), "new text", "update", "50", "-f", "-", "--format", "markdown", "--json")
	if r.code == 0 || !strings.Contains(r.stderr, "lossy_update") || !strings.Contains(r.stderr, "page layout") {
		t.Fatalf("lossy markdown update should be refused: %+v", r)
	}
	if len(s.requests(http.MethodPut, "")) != 0 {
		t.Fatal("refused update still sent a PUT")
	}
	mustSucceed(t, run(t, s.env(t), "new text", "update", "50", "-f", "-", "--format", "markdown", "--allow-lossy"))
	if len(s.requests(http.MethodPut, "/rest/api/content/50")) != 1 {
		t.Error("--allow-lossy did not update")
	}
	mustSucceed(t, run(t, s.env(t), "new text", "update", "42", "-f", "-", "--format", "markdown"))
}

func TestUpdateConflictReportsCode(t *testing.T) {
	s := writeSite(t)
	s.handle("PUT /rest/api/content/42", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusConflict) })
	r := run(t, s.env(t), "", "update", "42", "-c", "<p>x</p>", "--json")
	if r.code == 0 || !strings.Contains(r.stderr, "version_conflict") {
		t.Errorf("conflict = %+v", r)
	}
}

func TestEditWritesStorageOnly(t *testing.T) {
	s := writeSite(t)
	out := filepath.Join(t.TempDir(), "page.xml")
	r := mustSucceed(t, run(t, s.env(t), "", "edit", "42", "-o", out))
	assertFile(t, out, "<p>old</p>")
	assertContains(t, r.stderr, "Runbook", "7")
	stdout := mustSucceed(t, run(t, s.env(t), "", "edit", "42")).stdout
	if strings.TrimSpace(stdout) != "<p>old</p>" {
		t.Errorf("edit stdout should be the storage body only, got %q", stdout)
	}
}

func TestMove(t *testing.T) {
	s := writeSite(t)
	if r := run(t, s.env(t), "", "move", "42", "8"); r.code == 0 || !strings.Contains(r.stderr, "across spaces") {
		t.Errorf("cross-space move = %+v", r)
	}
	mustSucceed(t, run(t, s.env(t), "", "move", "42", "9"))
	b := body(t, s.requests(http.MethodPut, "/rest/api/content/42")[0])
	if dig(b, "ancestors", "id") != "9" || dig(b, "body", "storage", "value") != "<p>old</p>" {
		t.Errorf("move body = %v", b)
	}
}

func TestCommentReply(t *testing.T) {
	s := writeSite(t)
	mustSucceed(t, run(t, s.env(t), "", "comment", "42", "-c", "Thanks!", "--format", "markdown", "--parent", "c1"))
	b := body(t, s.requests(http.MethodPost, "/rest/api/content")[0])
	if dig(b, "type") != "comment" || dig(b, "ancestors", "id") != "c1" || dig(b, "body", "storage", "value") != "<p>Thanks!</p>" {
		t.Errorf("comment body = %v", b)
	}
}

func TestAttachmentUpload(t *testing.T) {
	s := writeSite(t)
	s.json("POST /rest/api/content/42/child/attachment", `{"results":[{"id":"att1","title":"a.txt"}]}`)
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.txt"), filepath.Join(dir, "b.txt")
	for _, f := range []string{a, b} {
		if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mustSucceed(t, run(t, s.env(t), "", "attachment-upload", "42", "-f", a, "-f", b, "--dry-run"))
	if len(s.requests(http.MethodPost, "")) != 0 {
		t.Fatal("dry run uploaded")
	}
	mustSucceed(t, run(t, s.env(t), "", "attachment-upload", "42", "-f", a, "-f", b, "--comment", "v2"))
	if n := len(s.requests(http.MethodPost, "/rest/api/content/42/child/attachment")); n != 2 {
		t.Errorf("uploads = %d, want 2", n)
	}
	if r := run(t, s.env(t), "", "attachment-upload", "42", "-f", filepath.Join(dir, "missing.txt")); r.code == 0 {
		t.Error("missing file should fail")
	}
}

func TestPropertySet(t *testing.T) {
	s := writeSite(t)
	s.handle("GET /rest/api/content/42/property/status", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
	s.json("PUT /rest/api/content/42/property/status", `{"key":"status","value":{"state":"done"},"version":{"number":1}}`)
	if r := run(t, s.env(t), "", "property-set", "42", "status", "-v", "{not json"); r.code == 0 {
		t.Error("invalid JSON should be refused")
	}
	mustSucceed(t, run(t, s.env(t), "", "property-set", "42", "status", "-v", `{"state":"done"}`))
	b := body(t, s.requests(http.MethodPut, "/rest/api/content/42/property/status")[0])
	if dig(b, "value", "state") != "done" {
		t.Errorf("property body = %v", b)
	}
}

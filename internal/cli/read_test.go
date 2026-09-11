package cli

import (
	"net/http"
	"strings"
	"testing"
)

const page42 = `{"id":"42","type":"page","title":"Runbook","space":{"key":"OPS","name":"Operations"},
	"version":{"number":7,"when":"2026-09-01T10:00:00.000Z","by":{"displayName":"Chris"}},
	"history":{"createdBy":{"displayName":"Alice"},"createdDate":"2025-01-01T00:00:00.000Z"},
	"ancestors":[{"id":"1","type":"page","title":"Home"}],
	"body":{"storage":{"value":"<p>Ask <ac:link><ri:user ri:userkey=\"k1\"/></ac:link>.</p><ac:structured-macro ac:name=\"children\"/><ac:structured-macro ac:name=\"jira\"/>","representation":"storage"},
	        "view":{"value":"<p>rendered</p>","representation":"view"}},
	"_links":{"webui":"/pages/viewpage.action?pageId=42"}}`

func siteWithPage(t *testing.T) *fakeSite {
	s := newSite(t)
	s.json("GET /rest/api/content/42", page42)
	s.json("GET /rest/api/user", `{"userKey":"k1","displayName":"Alice Wang"}`)
	s.json("GET /rest/api/content/42/child/page", `{"results":[{"id":"43","title":"Child","_links":{"webui":"/pages/viewpage.action?pageId=43"}}],"_links":{}}`)
	return s
}

func TestReadFormats(t *testing.T) {
	s := siteWithPage(t)
	env := s.env(t)

	md := mustSucceed(t, run(t, env, "", "read", "42"))
	assertContains(t, md.stdout, "Ask @Alice Wang.", "- [Child]("+s.srv.URL+"/pages/viewpage.action?pageId=43)", "<!-- macro: jira -->")
	assertContains(t, md.stderr, "macro:jira", "storage")

	st := mustSucceed(t, run(t, env, "", "read", "42", "-f", "storage"))
	if !strings.HasPrefix(st.stdout, "<p>Ask <ac:link>") {
		t.Errorf("storage output = %q", st.stdout)
	}
	html := mustSucceed(t, run(t, env, "", "read", "42", "--format", "html"))
	if strings.TrimSpace(html.stdout) != "<p>rendered</p>" {
		t.Errorf("html output = %q", html.stdout)
	}
	if r := run(t, env, "", "read", "42", "-f", "text"); r.code == 0 {
		t.Error("unsupported format should fail")
	}

	js := decode[map[string]any](t, mustSucceed(t, run(t, env, "", "read", "42", "--json")).stdout)
	if js["id"] != "42" || js["format"] != "markdown" || js["space"] != "OPS" || js["version"] != float64(7) {
		t.Errorf("json = %v", js)
	}
	if lossy, _ := js["lossy"].([]any); len(lossy) == 0 {
		t.Errorf("json should list lossy features: %v", js)
	}
}

func TestReadAcceptsURL(t *testing.T) {
	s := siteWithPage(t)
	r := mustSucceed(t, run(t, s.env(t), "", "read", s.srv.URL+"/pages/viewpage.action?pageId=42", "-f", "storage"))
	assertContains(t, r.stdout, "<p>Ask")
}

func TestInfo(t *testing.T) {
	s := siteWithPage(t)
	r := mustSucceed(t, run(t, s.env(t), "", "info", "42"))
	assertContains(t, r.stdout, "Runbook", "OPS", "7", "Chris", "Alice", s.srv.URL+"/pages/viewpage.action?pageId=42")
	v := decode[map[string]any](t, mustSucceed(t, run(t, s.env(t), "", "info", "42", "--json")).stdout)
	if v["parentId"] != "1" || v["updatedBy"] != "Chris" || v["url"] != s.srv.URL+"/pages/viewpage.action?pageId=42" {
		t.Errorf("info json = %v", v)
	}
}

func TestSearchAndFind(t *testing.T) {
	s := newSite(t)
	s.json("GET /rest/api/search", `{"totalSize":1,"results":[{"content":{"id":"42","type":"page","title":"Runbook","space":{"key":"OPS"},
		"_links":{"webui":"/pages/viewpage.action?pageId=42"}},"title":"Runbook","excerpt":"restart the @@@hl@@@service@@@endhl@@@"}]}`)
	env := s.env(t)

	r := mustSucceed(t, run(t, env, "", "search", "service", "-l", "5"))
	assertContains(t, r.stdout, "42", "Runbook", "OPS", "restart the service")
	q := s.requests("GET", "/rest/api/search")[0].Query
	if q.Get("cql") != `text ~ "service"` || q.Get("limit") != "5" {
		t.Errorf("search query = %v", q)
	}

	mustSucceed(t, run(t, env, "", "search", `space = OPS AND type = page`, "--cql"))
	if got := s.requests("GET", "/rest/api/search")[1].Query.Get("cql"); got != `space = OPS AND type = page` {
		t.Errorf("raw cql = %q", got)
	}

	f := decode[map[string]any](t, mustSucceed(t, run(t, env, "", "find", "Runbook", "-s", "OPS", "--json")).stdout)
	if f["id"] != "42" {
		t.Errorf("find json = %v", f)
	}
}

func TestSpaces(t *testing.T) {
	s := newSite(t)
	s.json("GET /rest/api/space", `{"results":[{"key":"OPS","name":"Operations","type":"global"},{"key":"~cy","name":"Chris","type":"personal"}],"_links":{}}`)
	s.json("GET /rest/api/space/OPS", `{"key":"OPS","name":"Operations","type":"global","homepage":{"id":"10","title":"Ops Home"},"_links":{"webui":"/display/OPS"}}`)
	env := s.env(t)
	r := mustSucceed(t, run(t, env, "", "spaces"))
	assertContains(t, r.stdout, "OPS", "Operations", "~cy")
	if got := s.requests("GET", "/rest/api/space")[0].Query.Get("limit"); got != "100" {
		t.Errorf("page size = %s", got)
	}
	l := mustSucceed(t, run(t, env, "", "space-lookup", "OPS"))
	assertContains(t, l.stdout, "Ops Home", "10")
}

func TestChildrenRecursive(t *testing.T) {
	s := newSite(t)
	s.json("GET /rest/api/content/1/child/page", `{"results":[{"id":"2","title":"A"},{"id":"3","title":"B"}],"_links":{}}`)
	s.json("GET /rest/api/content/2/child/page", `{"results":[{"id":"4","title":"A1"}],"_links":{}}`)
	s.json("GET /rest/api/content/3/child/page", `{"results":[],"_links":{}}`)
	s.json("GET /rest/api/content/4/child/page", `{"results":[],"_links":{}}`)
	env := s.env(t)

	flat := mustSucceed(t, run(t, env, "", "children", "1"))
	if strings.Contains(flat.stdout, "A1") {
		t.Errorf("non-recursive listing included grandchildren:\n%s", flat.stdout)
	}
	tree := mustSucceed(t, run(t, env, "", "children", "1", "-r", "--format", "tree", "--show-id"))
	assertContains(t, tree.stdout, "- A [2]", "  - A1 [4]", "- B [3]")
	shallow := decode[[]map[string]any](t, mustSucceed(t, run(t, env, "", "children", "1", "-r", "--max-depth", "1", "--json")).stdout)
	if len(shallow) != 2 {
		t.Errorf("max-depth 1 should stop at direct children, got %v", shallow)
	}
}

func TestVersionsAndProperties(t *testing.T) {
	s := newSite(t)
	s.json("GET /rest/api/content/42/version", `{"results":[{"number":1,"when":"2025-01-01","by":{"displayName":"Alice"},"message":"first"}],"_links":{}}`)
	s.json("GET /rest/api/content/42/property", `{"results":[{"key":"status","value":{"state":"done"}}],"_links":{}}`)
	s.json("GET /rest/api/content/42/property/status", `{"key":"status","value":{"state":"done"},"version":{"number":2}}`)
	env := s.env(t)
	assertContains(t, mustSucceed(t, run(t, env, "", "versions", "42")).stdout, "1", "Alice", "first")
	assertContains(t, mustSucceed(t, run(t, env, "", "property-list", "42")).stdout, "status", `{"state":"done"}`)
	assertContains(t, mustSucceed(t, run(t, env, "", "property-get", "42", "status")).stdout, `"state": "done"`)
}

func TestComments(t *testing.T) {
	s := newSite(t)
	s.json("GET /rest/api/content/42/child/comment", `{"results":[
		{"id":"c1","type":"comment","body":{"storage":{"value":"<p>Looks <strong>good</strong></p>"}},"history":{"createdBy":{"displayName":"Alice"},"createdDate":"2026-09-01"},"extensions":{"location":"footer"}},
		{"id":"c2","type":"comment","body":{"storage":{"value":"<p>thanks</p>"}},"history":{"createdBy":{"displayName":"Bob"}},"ancestors":[{"id":"c1","type":"comment"}],"extensions":{"location":"footer"}}],"_links":{}}`)
	s.json("GET /rest/api/content/c1", `{"id":"c1","type":"comment","container":{"id":"42","title":"Runbook"},"body":{"storage":{"value":"<p>Looks good</p>"}}}`)
	env := s.env(t)
	r := mustSucceed(t, run(t, env, "", "comments", "42", "--location", "footer"))
	assertContains(t, r.stdout, "Alice", "Looks **good**", "Bob", "reply to c1")
	if got := s.requests("GET", "/rest/api/content/42/child/comment")[0].Query["location"]; len(got) != 1 || got[0] != "footer" {
		t.Errorf("location = %v", got)
	}
	assertContains(t, mustSucceed(t, run(t, env, "", "comment-lookup", "c1")).stdout, "c1", "42", "Looks good")
}

func TestAttachmentsListAndDownload(t *testing.T) {
	s := newSite(t)
	s.json("GET /rest/api/content/42/child/attachment", `{"results":[
		{"id":"a1","type":"attachment","title":"diagram.png","metadata":{"mediaType":"image/png"},"extensions":{"fileSize":3},"_links":{"download":"/download/attachments/42/diagram.png"}},
		{"id":"a2","type":"attachment","title":"notes.txt","metadata":{"mediaType":"text/plain"},"extensions":{"fileSize":2},"_links":{"download":"/download/attachments/42/notes.txt"}}],"_links":{}}`)
	s.json("GET /download/attachments/42/diagram.png", "PNG")
	s.json("GET /rest/api/content/a1", `{"id":"a1","type":"attachment","title":"diagram.png","container":{"id":"42"},"metadata":{"mediaType":"image/png"}}`)
	env := s.env(t)
	dest := t.TempDir()

	r := mustSucceed(t, run(t, env, "", "attachments", "42", "-p", "*.png", "-d", "--dest", dest))
	assertContains(t, r.stdout, "diagram.png")
	if strings.Contains(r.stdout, "notes.txt") {
		t.Error("pattern did not filter")
	}
	assertFile(t, dest+"/diagram.png", "PNG")
	assertContains(t, mustSucceed(t, run(t, env, "", "attachment-lookup", "a1")).stdout, "diagram.png", "image/png", "42")
}

func TestConvertIsLocal(t *testing.T) {
	env := map[string]string{"HOME": t.TempDir()}
	r := mustSucceed(t, run(t, env, "<p>Hi <strong>there</strong></p>", "convert", "--input-format", "storage", "--output-format", "markdown"))
	if strings.TrimSpace(r.stdout) != "Hi **there**" {
		t.Errorf("storage→markdown = %q", r.stdout)
	}
	r = mustSucceed(t, run(t, env, "# Title", "convert", "--input-format", "markdown", "--output-format", "storage"))
	if strings.TrimSpace(r.stdout) != "<h1>Title</h1>" {
		t.Errorf("markdown→storage = %q", r.stdout)
	}
	if r := run(t, env, "x", "convert", "--input-format", "markdown", "--output-format", "markdown"); r.code == 0 {
		t.Error("same input and output format should fail")
	}
}

func TestAPIIsGetOnly(t *testing.T) {
	s := newSite(t)
	s.json("GET /rest/api/space/OPS", `{"key":"OPS"}`)
	env := s.env(t)
	r := mustSucceed(t, run(t, env, "", "api", "space/OPS"))
	assertContains(t, r.stdout, `"key": "OPS"`)
	r = mustSucceed(t, run(t, env, "", "api", "/rest/api/space/OPS", "-i"))
	assertContains(t, r.stdout, "HTTP 200", `"key": "OPS"`)
	if r := run(t, env, "", "api", "https://evil.example.com/x"); r.code == 0 {
		t.Error("foreign URL must be refused")
	}
	if r := run(t, env, "", "api", "space/OPS", "-X", "DELETE"); r.code == 0 {
		t.Error("api must not accept other methods")
	}
	if n := len(s.requests(http.MethodDelete, "")); n != 0 {
		t.Errorf("a DELETE reached the server")
	}
}

func TestErrorsAsJSON(t *testing.T) {
	s := newSite(t)
	r := run(t, s.env(t), "", "read", "999", "--json")
	if r.code != 1 {
		t.Fatalf("exit code = %d", r.code)
	}
	e := decode[map[string]map[string]string](t, r.stderr[strings.Index(r.stderr, "{"):])
	if e["error"]["code"] != "not_found" {
		t.Errorf("error json = %v", e)
	}
	plain := run(t, s.env(t), "", "read", "999")
	assertContains(t, plain.stderr, "Error: ")
}

func TestHTTPWarningAndMissingConfig(t *testing.T) {
	s := siteWithPage(t)
	assertContains(t, mustSucceed(t, run(t, s.env(t), "", "info", "42")).stderr, "unencrypted")
	r := run(t, map[string]string{"HOME": t.TempDir()}, "", "info", "42")
	if r.code == 0 || !strings.Contains(r.stderr, "cfl init") {
		t.Errorf("missing config should point at cfl init: %+v", r)
	}
}

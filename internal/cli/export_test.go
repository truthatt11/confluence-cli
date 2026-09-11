package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// exportSite serves a small tree:
//
//	1 Root (attachments: used.png referenced, unused.bin not)
//	├── 2 "A/B: Design?" (child 4 "Deep")
//	└── 3 Archive
func exportSite(t *testing.T) *fakeSite {
	s := newSite(t)
	page := func(id, title, body string) string {
		return `{"id":"` + id + `","type":"page","title":"` + title + `","space":{"key":"OPS"},
			"version":{"number":3,"when":"2026-09-01T10:00:00.000Z"},
			"body":{"storage":{"value":"` + body + `","representation":"storage"}},
			"_links":{"webui":"/pages/viewpage.action?pageId=` + id + `"}}`
	}
	s.json("GET /rest/api/content/1", page("1", "Root", `<p>Root body <ac:image><ri:attachment ri:filename=\"used.png\"/></ac:image></p>`))
	s.json("GET /rest/api/content/2", page("2", "A/B: Design?", "<p>A body</p>"))
	s.json("GET /rest/api/content/3", page("3", "Archive", "<p>old</p>"))
	s.json("GET /rest/api/content/4", page("4", "Deep", "<p>deep</p>"))
	s.json("GET /rest/api/content/1/child/page", `{"results":[{"id":"2","title":"A/B: Design?"},{"id":"3","title":"Archive"}],"_links":{}}`)
	s.json("GET /rest/api/content/2/child/page", `{"results":[{"id":"4","title":"Deep"}],"_links":{}}`)
	s.json("GET /rest/api/content/3/child/page", `{"results":[],"_links":{}}`)
	s.json("GET /rest/api/content/4/child/page", `{"results":[],"_links":{}}`)
	s.json("GET /rest/api/content/1/child/attachment", `{"results":[
		{"id":"a1","type":"attachment","title":"used.png","_links":{"download":"/download/attachments/1/used.png"}},
		{"id":"a2","type":"attachment","title":"unused.bin","_links":{"download":"/download/attachments/1/unused.bin"}}],"_links":{}}`)
	for _, id := range []string{"2", "3", "4"} {
		s.json("GET /rest/api/content/"+id+"/child/attachment", `{"results":[],"_links":{}}`)
	}
	s.json("GET /download/attachments/1/used.png", "PNG")
	s.json("GET /download/attachments/1/unused.bin", "BIN")
	return s
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestExportSinglePage(t *testing.T) {
	s := exportSite(t)
	dest := t.TempDir()
	mustSucceed(t, run(t, s.env(t), "", "export", "1", "--dest", dest, "--referenced-only", "--delay-ms", "0"))

	md := readFile(t, filepath.Join(dest, "Root", "page.md"))
	assertContains(t, md, "---\nid: \"1\"\n", `title: "Root"`, `space: "OPS"`, "version: 3",
		`url: "`+s.srv.URL+`/pages/viewpage.action?pageId=1"`, `updated: "2026-09-01T10:00:00.000Z"`,
		"Root body ![used.png](attachments/used.png)")
	assertFile(t, filepath.Join(dest, "Root", "attachments", "used.png"), "PNG")
	if _, err := os.Stat(filepath.Join(dest, "Root", "attachments", "unused.bin")); err == nil {
		t.Error("--referenced-only downloaded an unreferenced attachment")
	}
	if _, err := os.Stat(filepath.Join(dest, "Root", "A_B_ Design_")); err == nil {
		t.Error("children exported without -r")
	}
	if _, err := os.Stat(filepath.Join(dest, "Root", exportMarker)); err != nil {
		t.Errorf("export marker missing: %v", err)
	}
}

func TestExportRecursive(t *testing.T) {
	s := exportSite(t)
	dest := t.TempDir()
	r := mustSucceed(t, run(t, s.env(t), "", "export", "1", "--dest", dest, "-r", "--exclude", "arch*", "--skip-attachments", "--delay-ms", "0", "--json"))
	assertContains(t, readFile(t, filepath.Join(dest, "Root", "A_B_ Design_", "page.md")), "A body")
	assertContains(t, readFile(t, filepath.Join(dest, "Root", "A_B_ Design_", "Deep", "page.md")), "deep")
	if _, err := os.Stat(filepath.Join(dest, "Root", "Archive")); err == nil {
		t.Error("--exclude did not skip Archive")
	}
	if _, err := os.Stat(filepath.Join(dest, "Root", "attachments")); err == nil {
		t.Error("--skip-attachments still downloaded")
	}
	pages := decode[[]map[string]any](t, r.stdout)
	if len(pages) != 3 {
		t.Errorf("json listed %d pages, want 3: %v", len(pages), pages)
	}

	shallow := t.TempDir()
	mustSucceed(t, run(t, s.env(t), "", "export", "1", "--dest", shallow, "-r", "--max-depth", "1", "--skip-attachments", "--delay-ms", "0"))
	if _, err := os.Stat(filepath.Join(shallow, "Root", "A_B_ Design_", "Deep")); err == nil {
		t.Error("--max-depth 1 exported grandchildren")
	}
}

func TestExportOverwriteSafety(t *testing.T) {
	s := exportSite(t)
	env := s.env(t)
	dest := t.TempDir()
	args := []string{"export", "1", "--dest", dest, "--skip-attachments", "--delay-ms", "0"}

	mustSucceed(t, run(t, env, "", args...))
	stale := filepath.Join(dest, "Root", "stale.md")
	if err := os.WriteFile(stale, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if r := run(t, env, "", args...); r.code == 0 || !strings.Contains(r.stderr, "--overwrite") {
		t.Errorf("second export without --overwrite should fail: %+v", r)
	}
	mustSucceed(t, run(t, env, "", append(args, "--overwrite")...))
	if _, err := os.Stat(stale); err == nil {
		t.Error("--overwrite kept a stale file")
	}

	foreign := t.TempDir()
	mine := filepath.Join(foreign, "Root")
	if err := os.MkdirAll(mine, 0o755); err != nil {
		t.Fatal(err)
	}
	precious := filepath.Join(mine, "precious.txt")
	if err := os.WriteFile(precious, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := run(t, env, "", "export", "1", "--dest", foreign, "--overwrite", "--skip-attachments", "--delay-ms", "0")
	if r.code == 0 {
		t.Error("--overwrite must refuse a directory cfl did not create")
	}
	assertFile(t, precious, "keep")
}

func TestExportDryRunWritesNothing(t *testing.T) {
	s := exportSite(t)
	dest := t.TempDir()
	r := mustSucceed(t, run(t, s.env(t), "", "export", "1", "--dest", dest, "-r", "--dry-run", "--delay-ms", "0"))
	assertContains(t, r.stdout, filepath.Join(dest, "Root", "page.md"), "Deep", "used.png")
	entries, _ := os.ReadDir(dest)
	if len(entries) != 0 {
		t.Errorf("dry run wrote %v", entries)
	}
	if n := len(s.requests("GET", "/download/attachments/1/used.png")); n != 0 {
		t.Error("dry run downloaded an attachment")
	}
}

func TestSafeName(t *testing.T) {
	tests := map[string]string{
		"A/B: Design?":          "A_B_ Design_",
		"..":                    "page-7",
		"  部署手冊 ":               "部署手冊",
		"con<>|*\"\\x":          "con______x",
		strings.Repeat("長", 60): strings.Repeat("長", 33),
	}
	for in, want := range tests {
		if got := safeName(in, "7"); got != want {
			t.Errorf("safeName(%q) = %q, want %q", in, got, want)
		}
	}
}

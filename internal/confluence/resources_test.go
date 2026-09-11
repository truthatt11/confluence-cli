package confluence

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSearch(t *testing.T) {
	f, c := newFakeDC(t)
	f.json("GET /rest/api/search", `{"totalSize":12,"results":[{"content":{"id":"1","type":"page","title":"Deploy"},
		"title":"@@@hl@@@Deploy@@@endhl@@@ guide","excerpt":"how to @@@hl@@@deploy@@@endhl@@@ ","url":"/display/OPS/Deploy"}]}`)
	res, err := c.Search(ctx, TextQuery(`blue "green"`), 5, 10)
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalSize != 12 || res.Results[0].Title != "Deploy guide" || res.Results[0].Excerpt != "how to deploy" {
		t.Errorf("result = %+v", res)
	}
	q, _ := url.ParseQuery(f.requests("GET")[0].Query)
	if q.Get("cql") != `text ~ "blue \"green\""` || q.Get("limit") != "5" || q.Get("start") != "10" {
		t.Errorf("query = %v", q)
	}
}

func TestSpaces(t *testing.T) {
	f, c := newFakeDC(t)
	f.json("GET /rest/api/space", `{"results":[{"key":"OPS","name":"Operations","type":"global"}],"_links":{}}`)
	f.json("GET /rest/api/space/OPS", `{"key":"OPS","name":"Operations","homepage":{"id":"10","title":"Home"}}`)
	spaces, err := c.ListSpaces(ctx, 0)
	if err != nil || len(spaces) != 1 || spaces[0].Key != "OPS" {
		t.Fatalf("spaces = %v, %v", spaces, err)
	}
	s, err := c.GetSpace(ctx, "OPS")
	if err != nil || s.Homepage == nil || s.Homepage.ID != "10" {
		t.Fatalf("space = %+v, %v", s, err)
	}
}

func TestUsers(t *testing.T) {
	f, c := newFakeDC(t)
	f.json("GET /rest/api/user/current", `{"type":"known","username":"cyang","displayName":"Chris"}`)
	f.routes["GET /rest/api/user"] = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("key") == "k1" {
			_, _ = io.WriteString(w, `{"userKey":"k1","displayName":"Alice"}`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}
	me, err := c.CurrentUser(ctx)
	if err != nil || me.Username != "cyang" {
		t.Fatalf("current = %+v, %v", me, err)
	}
	names := c.DisplayNames(ctx, []string{"k1", "gone"})
	if names["k1"] != "Alice" || names["gone"] != "gone" {
		t.Errorf("names = %v", names)
	}
	before := len(f.requests("GET"))
	_ = c.DisplayNames(ctx, []string{"k1", "gone"})
	if len(f.requests("GET")) != before {
		t.Error("display names were not cached")
	}
}

func TestAttachments(t *testing.T) {
	f, c := newFakeDC(t)
	f.json("GET /rest/api/content/42/child/attachment", `{"results":[{"id":"att1","type":"attachment","title":"a.png",
		"metadata":{"mediaType":"image/png"},"extensions":{"fileSize":3},"_links":{"download":"/download/attachments/42/a.png"}}],"_links":{}}`)
	f.routes["GET /download/attachments/42/a.png"] = func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "PNG") }

	atts, err := c.Attachments(ctx, "42", 0)
	if err != nil || len(atts) != 1 || atts[0].MediaType() != "image/png" || atts[0].FileSize() != 3 {
		t.Fatalf("attachments = %+v, %v", atts, err)
	}
	var buf bytes.Buffer
	n, err := c.Download(ctx, atts[0], &buf)
	if err != nil || n != 3 || buf.String() != "PNG" {
		t.Fatalf("download = %d %q %v", n, buf.String(), err)
	}
	if got := f.requests("GET")[1].Header.Get("Authorization"); got == "" {
		t.Error("download was sent without credentials")
	}
	foreign := Content{Links: Links{Download: "https://evil.example.com/steal"}}
	if _, err := c.Download(ctx, foreign, io.Discard); err == nil {
		t.Error("download from another origin must be refused")
	}
}

func TestUploadAttachment(t *testing.T) {
	for _, replace := range []bool{false, true} {
		f, c := newFakeDC(t)
		f.json("POST /rest/api/content/42/child/attachment", `{"results":[{"id":"att9","title":"report.txt"}]}`)
		f.json("PUT /rest/api/content/42/child/attachment", `{"results":[{"id":"att9","title":"report.txt"}]}`)
		path := filepath.Join(t.TempDir(), "report.txt")
		if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
			t.Fatal(err)
		}
		att, err := c.UploadAttachment(ctx, "42", path, Upload{Comment: "v2", MinorEdit: true, Replace: replace})
		if err != nil || att.ID != "att9" {
			t.Fatalf("upload = %+v, %v", att, err)
		}
		method := http.MethodPost
		if replace {
			method = http.MethodPut
		}
		reqs := f.requests(method)
		if len(reqs) != 1 {
			t.Fatalf("want one %s, got %d", method, len(reqs))
		}
		r := reqs[0]
		if r.Header.Get("X-Atlassian-Token") != "nocheck" {
			t.Error("missing X-Atlassian-Token: nocheck")
		}
		fields := multipartFields(t, r)
		if fields["file"] != "report.txt=hello" || fields["comment"] != "v2" || fields["minorEdit"] != "true" {
			t.Errorf("multipart fields = %v", fields)
		}
	}
}

func multipartFields(t *testing.T, r recorded) map[string]string {
	t.Helper()
	_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	mr := multipart.NewReader(bytes.NewReader(r.Body), params["boundary"])
	out := map[string]string{}
	for {
		p, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(p)
		if p.FileName() != "" {
			out[p.FormName()] = p.FileName() + "=" + string(b)
		} else {
			out[p.FormName()] = string(b)
		}
	}
}

func TestComments(t *testing.T) {
	f, c := newFakeDC(t)
	f.json("GET /rest/api/content/42/child/comment", `{"results":[{"id":"c1","type":"comment","extensions":{"location":"inline","resolution":{"status":"open"}}}],"_links":{}}`)
	f.json("POST /rest/api/content", `{"id":"c2","type":"comment"}`)
	got, err := c.Comments(ctx, "42", CommentQuery{Location: []string{"inline", "footer"}, Limit: 25})
	if err != nil || len(got) != 1 || got[0].Extension("location") != "inline" || got[0].Extension("resolution") != "open" {
		t.Fatalf("comments = %+v, %v", got, err)
	}
	q, _ := url.ParseQuery(f.requests("GET")[0].Query)
	if strings.Join(q["location"], ",") != "inline,footer" || q.Get("depth") != "all" || !strings.Contains(q.Get("expand"), "body.storage") {
		t.Errorf("comment query = %v", q)
	}
	if _, err := c.CreateComment(ctx, "42", "c1", "<p>reply</p>"); err != nil {
		t.Fatal(err)
	}
	body := decodeJSON(t, f.requests("POST")[0].Body)
	if at(body, "type") != "comment" || at(body, "container", "id") != "42" || at(body, "ancestors", 0, "id") != "c1" ||
		at(body, "body", "storage", "value") != "<p>reply</p>" {
		t.Errorf("comment body = %v", body)
	}
}

func TestProperties(t *testing.T) {
	f, c := newFakeDC(t)
	f.json("GET /rest/api/content/42/property/existing", `{"key":"existing","value":{"a":1},"version":{"number":4}}`)
	f.status("GET /rest/api/content/42/property/fresh", http.StatusNotFound)
	f.json("PUT /rest/api/content/42/property/existing", `{"key":"existing","value":{"a":2},"version":{"number":5}}`)
	f.json("PUT /rest/api/content/42/property/fresh", `{"key":"fresh","value":true,"version":{"number":1}}`)
	f.json("GET /rest/api/content/42/property", `{"results":[{"key":"existing","value":{"a":1}}],"_links":{}}`)

	if _, err := c.SetProperty(ctx, "42", "existing", json.RawMessage(`{"a":2}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SetProperty(ctx, "42", "fresh", json.RawMessage(`true`)); err != nil {
		t.Fatal(err)
	}
	puts := f.requests("PUT")
	if v := at(decodeJSON(t, puts[0].Body), "version", "number"); v != float64(5) {
		t.Errorf("existing property version = %v, want 5", v)
	}
	if v := at(decodeJSON(t, puts[1].Body), "version", "number"); v != float64(1) {
		t.Errorf("new property version = %v, want 1", v)
	}
	props, err := c.Properties(ctx, "42", 0, 0)
	if err != nil || len(props) != 1 || string(props[0].Value) != `{"a":1}` {
		t.Fatalf("properties = %+v, %v", props, err)
	}
	if _, err := c.GetProperty(ctx, "42", "existing"); err != nil {
		t.Fatal(err)
	}
}

func TestVersionsFallsBackToExperimental(t *testing.T) {
	f, c := newFakeDC(t)
	f.status("GET /rest/api/content/42/version", http.StatusNotFound)
	f.json("GET /rest/experimental/content/42/version", `{"results":[{"number":2,"by":{"displayName":"B"}},{"number":1}],"_links":{}}`)
	vs, err := c.Versions(ctx, "42")
	if err != nil || len(vs) != 2 || vs[0].Number != 1 || vs[1].By.DisplayName != "B" {
		t.Fatalf("versions = %+v, %v", vs, err)
	}
}

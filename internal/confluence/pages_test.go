package confluence

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

var ctx = context.Background()

func TestResolvePageID(t *testing.T) {
	f, c := newFakeDC(t)
	f.json("GET /rest/api/search", `{"results":[{"content":{"id":"555","type":"page","title":"My Page"}}]}`)
	f.routes["GET /x/AbC1"] = func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/pages/viewpage.action?pageId=77", http.StatusFound)
	}
	base := c.Origin()
	tests := map[string]string{
		"12345": "12345",
		base + "/pages/viewpage.action?pageId=42": "42",
		base + "/spaces/OPS/pages/9001/Runbook":   "9001",
		"/pages/viewpage.action?pageId=8":         "8",
		base + "/display/OPS/My+Page":             "555",
		base + "/display/OPS/Parent/My%20Page":    "555",
		base + "/x/AbC1":                          "77",
	}
	for ref, want := range tests {
		got, err := c.ResolvePageID(ctx, ref)
		if err != nil || got != want {
			t.Errorf("ResolvePageID(%q) = %q, %v; want %q", ref, got, err, want)
		}
	}
	search := f.requests("GET")
	var cql string
	for _, r := range search {
		if r.Path == "/rest/api/search" {
			q, _ := url.ParseQuery(r.Query)
			cql = q.Get("cql")
		}
	}
	if cql != `type = page AND title = "My Page" AND space = "OPS"` {
		t.Errorf("display URL lookup used CQL %q", cql)
	}
	for _, bad := range []string{"https://evil.example.com/pages/viewpage.action?pageId=1", "hello", base + "/spaces/OPS/overview"} {
		if _, err := c.ResolvePageID(ctx, bad); err == nil {
			t.Errorf("ResolvePageID(%q) should fail", bad)
		}
	}
}

func TestGetContentAndFindPage(t *testing.T) {
	f, c := newFakeDC(t)
	f.json("GET /rest/api/content/42", `{"id":"42","type":"page","title":"T","space":{"key":"OPS"},"version":{"number":7},
		"body":{"storage":{"value":"<p>x</p>","representation":"storage"}},"ancestors":[{"id":"1","type":"page"},{"id":"2","type":"page"}],
		"_links":{"webui":"/pages/viewpage.action?pageId=42"}}`)
	p, err := c.GetContent(ctx, "42", "body.storage", "version")
	if err != nil {
		t.Fatal(err)
	}
	if p.Storage() != "<p>x</p>" || p.VersionNumber() != 7 || p.SpaceKey() != "OPS" || p.ParentID("page") != "2" {
		t.Errorf("parsed %+v", p)
	}
	if got := f.requests("GET")[0].Query; got != "expand=body.storage%2Cversion" {
		t.Errorf("query = %s", got)
	}
	if got := c.PageURL(p); got != c.Origin()+"/pages/viewpage.action?pageId=42" {
		t.Errorf("PageURL = %s", got)
	}

	f.json("GET /rest/api/search", `{"results":[]}`)
	_, err = c.FindPage(ctx, `Say "hi"`, "")
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.Code != CodeNotFound {
		t.Errorf("want not_found, got %v", err)
	}
	q, _ := url.ParseQuery(f.requests("GET")[1].Query)
	if q.Get("cql") != `type = page AND title = "Say \"hi\""` {
		t.Errorf("cql = %s", q.Get("cql"))
	}
}

func TestCreateAndUpdatePage(t *testing.T) {
	f, c := newFakeDC(t)
	f.json("POST /rest/api/content", `{"id":"100","type":"page","title":"New"}`)
	f.json("PUT /rest/api/content/42", `{"id":"42","type":"page","title":"T","version":{"number":8}}`)

	if _, err := c.CreatePage(ctx, NewPage{Title: "New", SpaceKey: "OPS", ParentID: "7", Storage: "<p>hi</p>"}); err != nil {
		t.Fatal(err)
	}
	body := decodeJSON(t, f.requests("POST")[0].Body)
	if at(body, "type") != "page" || at(body, "space", "key") != "OPS" || at(body, "ancestors", 0, "id") != "7" ||
		at(body, "body", "storage", "value") != "<p>hi</p>" || at(body, "body", "storage", "representation") != "storage" {
		t.Errorf("create body = %v", body)
	}

	current := Content{ID: "42", Title: "T", Space: &Space{Key: "OPS"}, Version: &Version{Number: 7}}
	updated, err := c.UpdatePage(ctx, current, "", "<p>new</p>")
	if err != nil || updated.VersionNumber() != 8 {
		t.Fatalf("update = %+v, %v", updated, err)
	}
	body = decodeJSON(t, f.requests("PUT")[0].Body)
	if at(body, "version", "number") != float64(8) || at(body, "title") != "T" || at(body, "body", "storage", "value") != "<p>new</p>" {
		t.Errorf("update body = %v", body)
	}
	if _, err := c.UpdatePage(ctx, Content{ID: "1"}, "", "x"); err == nil {
		t.Error("update without version/space must fail before calling the API")
	}
}

func TestUpdateConflictIsNotRetried(t *testing.T) {
	f, c := newFakeDC(t)
	f.status("PUT /rest/api/content/42", http.StatusConflict)
	_, err := c.UpdatePage(ctx, Content{ID: "42", Space: &Space{Key: "S"}, Version: &Version{Number: 1}}, "t", "x")
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.Code != CodeVersionConflict {
		t.Fatalf("want version_conflict, got %v", err)
	}
	if n := len(f.requests("PUT")); n != 1 {
		t.Errorf("conflict was retried: %d PUTs", n)
	}
}

func TestMovePage(t *testing.T) {
	f, c := newFakeDC(t)
	f.json("PUT /rest/api/content/42", `{"id":"42","type":"page","title":"T"}`)
	page := Content{ID: "42", Title: "T", Space: &Space{Key: "OPS"}, Version: &Version{Number: 3}, Body: &Body{Storage: &Representation{Value: "<p>keep me</p>"}}}

	if _, err := c.MovePage(ctx, page, Content{ID: "9", Space: &Space{Key: "DEV"}}, ""); err == nil || !strings.Contains(err.Error(), "across spaces") {
		t.Errorf("cross-space move should be refused, got %v", err)
	}
	if len(f.requests("PUT")) != 0 {
		t.Fatal("refused move still sent a PUT")
	}
	if _, err := c.MovePage(ctx, page, Content{ID: "9", Space: &Space{Key: "OPS"}}, "Renamed"); err != nil {
		t.Fatal(err)
	}
	body := decodeJSON(t, f.requests("PUT")[0].Body)
	if at(body, "ancestors", 0, "id") != "9" || at(body, "version", "number") != float64(4) ||
		at(body, "body", "storage", "value") != "<p>keep me</p>" || at(body, "title") != "Renamed" {
		t.Errorf("move body = %v", body)
	}
}

func TestChildren(t *testing.T) {
	f, c := newFakeDC(t)
	f.json("GET /rest/api/content/42/child/page", `{"results":[{"id":"1","title":"A"},{"id":"2","title":"B"}],"_links":{}}`)
	kids, err := c.Children(ctx, "42", 0)
	if err != nil || len(kids) != 2 || kids[1].Title != "B" {
		t.Fatalf("children = %v, %v", kids, err)
	}
	q, _ := url.ParseQuery(f.requests("GET")[0].Query)
	if q.Get("expand") != "space,version" {
		t.Errorf("expand = %s", q.Get("expand"))
	}
}

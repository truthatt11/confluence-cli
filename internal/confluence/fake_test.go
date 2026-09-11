package confluence

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// recorded is one request the fake server saw.
type recorded struct {
	Method, Path, Query string
	Header              http.Header
	Body                []byte
}

// fakeDC is a tiny router standing in for a Data Center server.
type fakeDC struct {
	mu     sync.Mutex
	routes map[string]http.HandlerFunc // "GET /rest/api/content/1"
	seen   []recorded
}

func newFakeDC(t *testing.T) (*fakeDC, *Client) {
	t.Helper()
	f := &fakeDC{routes: map[string]http.HandlerFunc{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	c, _ := newTestClient(t, srv, Options{})
	return f, c
}

func (f *fakeDC) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	f.mu.Lock()
	f.seen = append(f.seen, recorded{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Clone(), body})
	h, ok := f.routes[r.Method+" "+r.URL.Path]
	f.mu.Unlock()
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"message":"no route `+r.Method+" "+r.URL.Path+`"}`)
		return
	}
	r.Body = io.NopCloser(strings.NewReader(string(body)))
	h(w, r)
}

// json registers a route answering with a fixed JSON body.
func (f *fakeDC) json(route, body string) {
	f.routes[route] = func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }
}

func (f *fakeDC) status(route string, code int) {
	f.routes[route] = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }
}

func (f *fakeDC) requests(method string) []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []recorded
	for _, r := range f.seen {
		if r.Method == method {
			out = append(out, r)
		}
	}
	return out
}

func decodeJSON(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("request body is not JSON: %v\n%s", err, b)
	}
	return m
}

// at walks a decoded JSON value by keys and indexes, e.g. at(m, "version", "number").
func at(v any, path ...any) any {
	for _, p := range path {
		switch k := p.(type) {
		case string:
			m, _ := v.(map[string]any)
			v = m[k]
		case int:
			s, _ := v.([]any)
			if k >= len(s) {
				return nil
			}
			v = s[k]
		}
	}
	return v
}

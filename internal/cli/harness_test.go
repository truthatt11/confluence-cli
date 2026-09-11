package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
)

type seenRequest struct {
	Method, Path string
	Query        url.Values
	Header       http.Header
	Body         []byte
}

// fakeSite is a stand-in Data Center server with scripted routes.
type fakeSite struct {
	mu     sync.Mutex
	routes map[string]http.HandlerFunc
	seen   []seenRequest
	srv    *httptest.Server
}

func newSite(t *testing.T) *fakeSite {
	t.Helper()
	s := &fakeSite{routes: map[string]http.HandlerFunc{}}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		s.mu.Lock()
		s.seen = append(s.seen, seenRequest{r.Method, r.URL.Path, r.URL.Query(), r.Header.Clone(), body})
		h, ok := s.routes[r.Method+" "+r.URL.Path]
		s.mu.Unlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"message":"no route for `+r.Method+" "+r.URL.Path+`"}`)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		h(w, r)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *fakeSite) json(route, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routes[route] = func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }
}

func (s *fakeSite) handle(route string, h http.HandlerFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.routes[route] = h
}

func (s *fakeSite) requests(method, path string) []seenRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []seenRequest
	for _, r := range s.seen {
		if r.Method == method && (path == "" || r.Path == path) {
			out = append(out, r)
		}
	}
	return out
}

func (s *fakeSite) host() string {
	u, _ := url.Parse(s.srv.URL)
	return u.Host
}

// env configures cfl purely from environment variables against the fake site.
func (s *fakeSite) env(t *testing.T, extra ...string) map[string]string {
	t.Helper()
	m := map[string]string{
		"HOME":                 t.TempDir(),
		"CONFLUENCE_DOMAIN":    s.host(),
		"CONFLUENCE_PROTOCOL":  "http",
		"CONFLUENCE_API_TOKEN": "test-token",
	}
	for i := 0; i+1 < len(extra); i += 2 {
		m[extra[i]] = extra[i+1]
	}
	return m
}

type result struct {
	stdout, stderr string
	code           int
}

func run(t *testing.T, env map[string]string, stdin string, args ...string) result {
	t.Helper()
	var out, errb bytes.Buffer
	a := &app{
		stdout: &out, stderr: &errb, stdin: strings.NewReader(stdin),
		env: func(k string) string { return env[k] }, version: "test",
	}
	code := a.run(context.Background(), args)
	return result{out.String(), errb.String(), code}
}

func mustSucceed(t *testing.T, r result) result {
	t.Helper()
	if r.code != 0 {
		t.Fatalf("exit %d\nstdout: %s\nstderr: %s", r.code, r.stdout, r.stderr)
	}
	return r
}

func decode[T any](t *testing.T, s string) T {
	t.Helper()
	var v T
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("output is not the expected JSON: %v\n%s", err, s)
	}
	return v
}

func assertContains(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("output lacks %q:\n%s", w, got)
		}
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != want {
		t.Errorf("%s = %q, want %q", path, b, want)
	}
}

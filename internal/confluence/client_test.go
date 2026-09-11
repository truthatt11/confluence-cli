package confluence

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// newTestClient points a client at srv and records retry waits instead of sleeping.
func newTestClient(t *testing.T, srv *httptest.Server, o Options) (*Client, *[]time.Duration) {
	t.Helper()
	u, _ := url.Parse(srv.URL)
	o.Protocol, o.Domain = "http", u.Host
	if o.Token == "" {
		o.Token = "tok"
	}
	c, err := New(o)
	if err != nil {
		t.Fatal(err)
	}
	var waits []time.Duration
	c.sleep = func(_ context.Context, d time.Duration) error { waits = append(waits, d); return nil }
	return c, &waits
}

func TestAuthHeaders(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		want string
	}{
		{"bearer", Options{Token: "pat123"}, "Bearer pat123"},
		{"basic", Options{AuthType: "basic", Email: "cyang", Token: "pw"}, "Basic " + base64.StdEncoding.EncodeToString([]byte("cyang:pw"))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got, ua string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got, ua = r.Header.Get("Authorization"), r.Header.Get("User-Agent")
				fmt.Fprint(w, `{}`)
			}))
			defer srv.Close()
			tt.opts.UserAgent = "cfl/test"
			c, _ := newTestClient(t, srv, tt.opts)
			if err := c.getJSON(context.Background(), "/space", nil, nil); err != nil {
				t.Fatal(err)
			}
			if got != tt.want || ua != "cfl/test" {
				t.Errorf("Authorization=%q User-Agent=%q", got, ua)
			}
		})
	}
}

func TestErrorMapping(t *testing.T) {
	tests := []struct {
		status   int
		body     string
		wantCode string
		wantMsg  string
	}{
		{401, ``, CodeAuthFailed, "cfl init"},
		{403, ``, CodeForbidden, "permission"},
		{404, `{"statusCode":404,"message":"No content found with id: 42"}`, CodeNotFound, "No content found with id: 42"},
		{409, ``, CodeVersionConflict, "changed"},
		{400, `{"message":"Error parsing xhtml: Unexpected close tag"}`, CodeBadRequest, "Error parsing xhtml"},
		{500, `oops`, CodeServerError, "500"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprint(tt.status), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				fmt.Fprint(w, tt.body)
			}))
			defer srv.Close()
			c, _ := newTestClient(t, srv, Options{Token: "super-secret-token"})
			err := c.getJSON(context.Background(), "/content/42", nil, nil)
			var apiErr *Error
			if !errors.As(err, &apiErr) {
				t.Fatalf("want *Error, got %T %v", err, err)
			}
			if apiErr.Code != tt.wantCode || apiErr.Status != tt.status || !strings.Contains(apiErr.Error(), tt.wantMsg) {
				t.Errorf("got code=%s status=%d msg=%q", apiErr.Code, apiErr.Status, apiErr.Error())
			}
			if strings.Contains(apiErr.Error(), "super-secret-token") {
				t.Error("error message leaked the token")
			}
		})
	}
}

func TestRetryPolicy(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		statuses   []int
		retryAfter string
		wantCalls  int32
		wantErr    string
		wantWaits  []time.Duration
	}{
		{"429 retried for POST, honours Retry-After", http.MethodPost, []int{429, 429, 200}, "2", 3, "", []time.Duration{2 * time.Second, 2 * time.Second}},
		{"429 backoff without Retry-After", http.MethodGet, []int{429, 429, 200}, "", 3, "", []time.Duration{time.Second, 2 * time.Second}},
		{"503 retried for GET", http.MethodGet, []int{503, 200}, "", 2, "", []time.Duration{time.Second}},
		{"503 not retried for POST", http.MethodPost, []int{503}, "", 1, CodeServerError, nil},
		{"gives up after three retries", http.MethodGet, []int{429, 429, 429, 429, 200}, "", 4, CodeRateLimited, nil},
		{"Retry-After capped at 60s", http.MethodGet, []int{429, 200}, "3600", 2, "", []time.Duration{60 * time.Second}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				i := calls.Add(1) - 1
				if tt.retryAfter != "" {
					w.Header().Set("Retry-After", tt.retryAfter)
				}
				w.WriteHeader(tt.statuses[i])
				fmt.Fprint(w, `{}`)
			}))
			defer srv.Close()
			c, waits := newTestClient(t, srv, Options{})
			err := c.send(context.Background(), tt.method, "/content", nil, map[string]string{"a": "b"}, nil)
			if calls.Load() != tt.wantCalls {
				t.Errorf("calls = %d, want %d", calls.Load(), tt.wantCalls)
			}
			var apiErr *Error
			if tt.wantErr == "" && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.wantErr != "" && (!errors.As(err, &apiErr) || apiErr.Code != tt.wantErr) {
				t.Fatalf("want error code %s, got %v", tt.wantErr, err)
			}
			if tt.wantWaits != nil && fmt.Sprint(*waits) != fmt.Sprint(tt.wantWaits) {
				t.Errorf("waits = %v, want %v", *waits, tt.wantWaits)
			}
		})
	}
}

func TestRetryResendsBody(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b := new(strings.Builder)
		_, _ = fmt.Fprint(b, readAll(r))
		bodies = append(bodies, b.String())
		if len(bodies) == 1 {
			w.WriteHeader(429)
			return
		}
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()
	c, _ := newTestClient(t, srv, Options{})
	if err := c.send(context.Background(), http.MethodPost, "/content", nil, map[string]string{"k": "v"}, nil); err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 2 || bodies[0] != bodies[1] || !strings.Contains(bodies[1], `"k":"v"`) {
		t.Errorf("retry did not resend the same body: %q", bodies)
	}
}

func TestRedirectToOtherOriginIsRefused(t *testing.T) {
	var leaked atomic.Bool
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Store(true) }))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/steal", http.StatusFound)
	}))
	defer srv.Close()
	c, _ := newTestClient(t, srv, Options{})
	err := c.getJSON(context.Background(), "/space", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "refusing redirect") {
		t.Fatalf("want refused redirect, got %v", err)
	}
	if leaked.Load() {
		t.Error("request reached the other origin")
	}
}

func TestSameOrigin(t *testing.T) {
	c, err := New(Options{Protocol: "https", Domain: "wiki.example.com", Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	for raw, want := range map[string]bool{
		"https://wiki.example.com/x":      true,
		"https://WIKI.example.com/x":      true,
		"http://wiki.example.com/x":       false, // downgrade would send the token in clear text
		"https://wiki.example.com:8443/x": false,
		"https://evil.example.com/x":      false,
	} {
		u, _ := url.Parse(raw)
		if got := c.sameOrigin(u); got != want {
			t.Errorf("sameOrigin(%s) = %v, want %v", raw, got, want)
		}
	}
}

func TestResolveRefusesForeignAbsoluteURL(t *testing.T) {
	c, _ := New(Options{Protocol: "https", Domain: "wiki.example.com", APIPath: "/confluence/rest/api", Token: "t"})
	if _, err := c.resolveRef("https://evil.example.com/rest/api/space"); err == nil {
		t.Error("foreign absolute URL must be refused")
	}
	tests := map[string]string{
		"https://wiki.example.com/confluence/rest/api/space": "https://wiki.example.com/confluence/rest/api/space",
		"/rest/api/space?limit=1":                            "https://wiki.example.com/confluence/rest/api/space?limit=1",
		"space/ABC":                                          "https://wiki.example.com/confluence/rest/api/space/ABC",
	}
	for in, want := range tests {
		got, err := c.resolveRef(in)
		if err != nil || got.String() != want {
			t.Errorf("resolveRef(%q) = %v, %v; want %s", in, got, err, want)
		}
	}
}

func TestContextPath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		fmt.Fprint(w, `{}`)
	}))
	defer srv.Close()
	c, _ := newTestClient(t, srv, Options{APIPath: "/confluence/rest/api"})
	if err := c.getJSON(context.Background(), "/space", nil, nil); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/confluence/rest/api/space" {
		t.Errorf("request path = %s", gotPath)
	}
	if got := c.WebURL("/pages/viewpage.action?pageId=1"); got != srv.URL+"/confluence/pages/viewpage.action?pageId=1" {
		t.Errorf("WebURL = %s", got)
	}
	if got := c.WebBase(); got != srv.URL+"/confluence" {
		t.Errorf("WebBase = %s", got)
	}
}

func TestCollectPaginates(t *testing.T) {
	var starts []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := r.URL.Query().Get("start")
		starts = append(starts, start+"/"+r.URL.Query().Get("limit"))
		switch start {
		case "0":
			fmt.Fprint(w, `{"results":[{"id":"1"},{"id":"2"}],"_links":{"next":"/rest/api/space?limit=2&start=2"}}`)
		case "2":
			fmt.Fprint(w, `{"results":[{"id":"3"},{"id":"4"}],"_links":{"next":"/rest/api/space?limit=2&start=4"}}`)
		default:
			fmt.Fprint(w, `{"results":[{"id":"5"}],"_links":{}}`)
		}
	}))
	defer srv.Close()
	c, _ := newTestClient(t, srv, Options{})
	type item struct{ ID string }

	all, err := collect[item](context.Background(), c, "/space", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 5 || all[4].ID != "5" {
		t.Errorf("collected %v", all)
	}

	starts = nil
	three, err := collect[item](context.Background(), c, "/space", nil, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(three) != 3 || fmt.Sprint(starts) != "[0/3 2/1]" {
		t.Errorf("max=3 collected %v with requests %v", three, starts)
	}
}

func TestNewValidates(t *testing.T) {
	if _, err := New(Options{Protocol: "https", Domain: "", Token: "t"}); err == nil {
		t.Error("empty domain must be rejected")
	}
	if _, err := New(Options{Protocol: "https", Domain: "bad host/x", Token: "t"}); err == nil {
		t.Error("invalid domain must be rejected")
	}
}

func readAll(r *http.Request) string {
	b, _ := io.ReadAll(r.Body)
	return string(b)
}

// Package confluence is a client for the Confluence Data Center / Server REST API.
package confluence

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	apiTimeout    = 60 * time.Second
	headerTimeout = 60 * time.Second
	maxRetries    = 3
	maxRetryDelay = 60 * time.Second
	maxJSONBody   = 64 << 20
)

// Options configures a Client. Values are expected to be normalized already
// (see config.Profile.Normalized).
type Options struct {
	Protocol  string
	Domain    string // host[:port]
	APIPath   string // e.g. /rest/api or /confluence/rest/api
	AuthType  string // bearer or basic
	Email     string // username for basic auth
	Token     string
	UserAgent string
}

// Client talks to one Confluence site. Credentials are only ever sent to its origin.
type Client struct {
	origin  *url.URL
	apiPath string
	context string // context path, e.g. /confluence
	auth    string
	ua      string
	http    *http.Client
	sleep   func(context.Context, time.Duration) error

	mu    sync.Mutex
	names map[string]string // userkey → display name cache
}

// New builds a client.
func New(o Options) (*Client, error) {
	if o.Domain == "" {
		return nil, errors.New("confluence domain is empty")
	}
	origin, err := url.Parse(o.Protocol + "://" + o.Domain)
	if err != nil || origin.Host == "" || origin.Path != "" {
		return nil, fmt.Errorf("invalid confluence domain %q", o.Domain)
	}
	apiPath := "/" + strings.Trim(o.APIPath, "/")
	if apiPath == "/" {
		apiPath = "/rest/api"
	}
	c := &Client{
		origin:  origin,
		apiPath: apiPath,
		context: strings.TrimSuffix(apiPath, "/rest/api"),
		auth:    authHeader(o),
		ua:      o.UserAgent,
		sleep:   sleepCtx,
		names:   map[string]string{},
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = headerTimeout
	c.http = &http.Client{Transport: transport, CheckRedirect: c.checkRedirect}
	return c, nil
}

func authHeader(o Options) string {
	if strings.EqualFold(o.AuthType, "basic") {
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(o.Email+":"+o.Token))
	}
	return "Bearer " + o.Token
}

// checkRedirect refuses redirects that leave the origin. Go's own protection
// compares hosts only, so an https→http redirect on the same host would
// otherwise resend the token in clear text.
func (c *Client) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	if !c.sameOrigin(req.URL) {
		return fmt.Errorf("refusing redirect to %s: credentials are only sent to %s", req.URL.Redacted(), c.origin)
	}
	return nil
}

func (c *Client) sameOrigin(u *url.URL) bool {
	return strings.EqualFold(u.Scheme, c.origin.Scheme) && strings.EqualFold(u.Host, c.origin.Host)
}

// WebBase is the site URL including its context path.
func (c *Client) WebBase() string { return c.origin.String() + c.context }

// WebURL turns a path from an API response (_links.webui, _links.download) into a URL.
func (c *Client) WebURL(path string) string {
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		return path
	}
	return c.WebBase() + path
}

// Origin is scheme://host[:port].
func (c *Client) Origin() string { return c.origin.String() }

// resolveRef turns a caller-supplied reference into a URL on this site:
// an absolute URL (same origin only), a site path ("/rest/api/space"), or a
// path relative to the REST API ("space/ABC").
func (c *Client) resolveRef(ref string) (*url.URL, error) {
	var raw string
	switch {
	case strings.Contains(ref, "://"):
		raw = ref
	case strings.HasPrefix(ref, "/"):
		raw = c.WebBase() + ref
	default:
		raw = c.origin.String() + c.apiPath + "/" + ref
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid URL %q: %w", ref, err)
	}
	if !c.sameOrigin(u) {
		return nil, fmt.Errorf("refusing to send credentials to %s: only %s is allowed", u.Redacted(), c.origin)
	}
	return u, nil
}

func (c *Client) apiURL(prefix, path string, q url.Values) string {
	u := c.origin.String() + prefix + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

// request is everything needed to (re)send a call; body is reopened per attempt.
type request struct {
	method      string
	url         string
	body        func() (io.Reader, error)
	contentType string
	header      map[string]string
}

// do sends req, retrying 429 for any method (the server refused before doing
// anything) and 503 only for GET, since a 503 from a proxy can arrive after
// the backend already applied a write.
func (c *Client) do(ctx context.Context, req request) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		httpReq, err := c.newRequest(ctx, req)
		if err != nil {
			return nil, err
		}
		resp, err := c.http.Do(httpReq)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", req.method, redact(req.url), err)
		}
		retry := resp.StatusCode == http.StatusTooManyRequests ||
			(resp.StatusCode == http.StatusServiceUnavailable && req.method == http.MethodGet)
		if !retry || attempt >= maxRetries {
			return resp, nil
		}
		wait := retryDelay(resp.Header.Get("Retry-After"), attempt)
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		if err := c.sleep(ctx, wait); err != nil {
			return nil, err
		}
	}
}

func (c *Client) newRequest(ctx context.Context, req request) (*http.Request, error) {
	var body io.Reader
	if req.body != nil {
		b, err := req.body()
		if err != nil {
			return nil, err
		}
		body = b
	}
	httpReq, err := http.NewRequestWithContext(ctx, req.method, req.url, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Authorization", c.auth)
	httpReq.Header.Set("Accept", "application/json")
	if c.ua != "" {
		httpReq.Header.Set("User-Agent", c.ua)
	}
	if req.contentType != "" {
		httpReq.Header.Set("Content-Type", req.contentType)
	}
	for k, v := range req.header {
		httpReq.Header.Set(k, v)
	}
	return httpReq, nil
}

// retryDelay honours Retry-After (seconds or HTTP date), else backs off 1s, 2s, 4s.
func retryDelay(retryAfter string, attempt int) time.Duration {
	d := time.Second << attempt
	if secs, err := strconv.Atoi(retryAfter); err == nil && secs >= 0 {
		d = time.Duration(secs) * time.Second
	} else if t, err := http.ParseTime(retryAfter); err == nil {
		d = max(time.Until(t), 0)
	}
	return min(d, maxRetryDelay)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func redact(raw string) string {
	if u, err := url.Parse(raw); err == nil {
		return u.Redacted()
	}
	return raw
}

// send issues a JSON API call under apiPath and decodes the response into out.
func (c *Client) send(ctx context.Context, method, path string, q url.Values, body, out any) error {
	return c.sendURL(ctx, method, c.apiURL(c.apiPath, path, q), body, out)
}

func (c *Client) getJSON(ctx context.Context, path string, q url.Values, out any) error {
	return c.send(ctx, http.MethodGet, path, q, nil, out)
}

func (c *Client) sendURL(ctx context.Context, method, rawURL string, body, out any) error {
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	req := request{method: method, url: rawURL}
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		req.body = func() (io.Reader, error) { return bytes.NewReader(payload), nil }
		req.contentType = "application/json"
	}
	resp, err := c.do(ctx, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return decode(resp, out)
}

func decode(resp *http.Response, out any) error {
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return errorFrom(resp)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxJSONBody)).Decode(out); err != nil {
		return fmt.Errorf("decode response from %s: %w", redact(resp.Request.URL.String()), err)
	}
	return nil
}

// listPage is the envelope Data Center uses for paginated results.
type listPage[T any] struct {
	Results []T `json:"results"`
	Links   struct {
		Next string `json:"next"`
	} `json:"_links"`
}

const pageSize = 100

// collect gathers results across pages until max (0 = everything) or the
// last page. It reads only `start` from _links.next and rebuilds the request
// itself, so context paths in the link cannot be doubled or dropped.
func collect[T any](ctx context.Context, c *Client, path string, q url.Values, max int) ([]T, error) {
	return collectAt[T](ctx, c, c.apiPath, path, q, max)
}

// collectAt is collect under a prefix other than the REST API path.
func collectAt[T any](ctx context.Context, c *Client, prefix, path string, q url.Values, max int) ([]T, error) {
	var all []T
	start := 0
	if q != nil {
		start, _ = strconv.Atoi(q.Get("start"))
	}
	for {
		limit := pageSize
		if max > 0 {
			limit = min(limit, max-len(all))
		}
		query := cloneValues(q)
		query.Set("start", strconv.Itoa(start))
		query.Set("limit", strconv.Itoa(limit))
		var p listPage[T]
		if err := c.sendURL(ctx, http.MethodGet, c.apiURL(prefix, path, query), nil, &p); err != nil {
			return nil, err
		}
		all = append(all, p.Results...)
		next, ok := nextStart(p.Links.Next)
		if !ok || len(p.Results) == 0 || (max > 0 && len(all) >= max) {
			break
		}
		start = next
	}
	if max > 0 && len(all) > max {
		all = all[:max]
	}
	return all, nil
}

func nextStart(link string) (int, bool) {
	if link == "" {
		return 0, false
	}
	u, err := url.Parse(link)
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(u.Query().Get("start"))
	return n, err == nil
}

func cloneValues(q url.Values) url.Values {
	out := url.Values{}
	for k, v := range q {
		out[k] = append([]string(nil), v...)
	}
	return out
}

// Get fetches ref (a same-origin URL, a site path, or a path under the REST
// API) and returns the raw response. Non-2xx responses return the body too,
// along with an *Error.
func (c *Client) Get(ctx context.Context, ref string) (int, http.Header, []byte, error) {
	u, err := c.resolveRef(ref)
	if err != nil {
		return 0, nil, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	resp, err := c.do(ctx, request{method: http.MethodGet, url: u.String()})
	if err != nil {
		return 0, nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxJSONBody))
	if err != nil {
		return resp.StatusCode, resp.Header, nil, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return resp.StatusCode, resp.Header, body, newError(resp.StatusCode, body)
	}
	return resp.StatusCode, resp.Header, body, nil
}

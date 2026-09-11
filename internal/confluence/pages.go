package confluence

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

var (
	digitsOnly  = regexp.MustCompile(`^\d+$`)
	prettyPath  = regexp.MustCompile(`/pages/(\d+)(?:/|$)`)
	displayPath = regexp.MustCompile(`/display/([^/]+)/(.+)$`)
	tinyPath    = regexp.MustCompile(`/x/([A-Za-z0-9_-]+)$`)
)

// ResolvePageID accepts a page ID or a URL on this site: ?pageId=,
// /pages/<id>, /display/SPACE/Title or a tiny link /x/<code>.
func (c *Client) ResolvePageID(ctx context.Context, ref string) (string, error) {
	return c.resolvePageID(ctx, strings.TrimSpace(ref), 0)
}

func (c *Client) resolvePageID(ctx context.Context, ref string, hops int) (string, error) {
	if digitsOnly.MatchString(ref) {
		return ref, nil
	}
	u, err := url.Parse(ref)
	if err != nil || (u.Host == "" && !strings.HasPrefix(ref, "/")) {
		return "", fmt.Errorf("%q is neither a page ID nor a URL", ref)
	}
	if u.Host != "" && !strings.EqualFold(u.Host, c.origin.Host) {
		return "", fmt.Errorf("%q is not on %s", ref, c.origin.Host)
	}
	if id := u.Query().Get("pageId"); digitsOnly.MatchString(id) {
		return id, nil
	}
	if m := prettyPath.FindStringSubmatch(u.Path); m != nil {
		return m[1], nil
	}
	if m := displayPath.FindStringSubmatch(u.EscapedPath()); m != nil {
		return c.pageIDFromDisplay(ctx, m[1], m[2])
	}
	if tinyPath.MatchString(u.Path) && hops == 0 {
		target, err := c.redirectTarget(ctx, ref)
		if err != nil {
			return "", err
		}
		return c.resolvePageID(ctx, target, hops+1)
	}
	return "", fmt.Errorf("cannot find a page ID in %q", ref)
}

// pageIDFromDisplay resolves /display/SPACE/Title; nested paths name the page last.
func (c *Client) pageIDFromDisplay(ctx context.Context, space, titlePath string) (string, error) {
	segs := strings.Split(titlePath, "/")
	title, err := url.PathUnescape(strings.ReplaceAll(segs[len(segs)-1], "+", "%20"))
	if err != nil {
		return "", fmt.Errorf("bad title in URL: %w", err)
	}
	spaceKey, _ := url.PathUnescape(space)
	page, err := c.FindPage(ctx, title, spaceKey)
	if err != nil {
		return "", err
	}
	return page.ID, nil
}

// redirectTarget follows a tiny link one hop and returns where it points.
func (c *Client) redirectTarget(ctx context.Context, ref string) (string, error) {
	u, err := c.resolveRef(ref)
	if err != nil {
		return "", err
	}
	noFollow := *c.http
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := c.newRequest(ctx, request{method: http.MethodGet, url: u.String()})
	if err != nil {
		return "", err
	}
	resp, err := noFollow.Do(req)
	if err != nil {
		return "", fmt.Errorf("resolve tiny link: %w", err)
	}
	defer resp.Body.Close()
	loc, err := resp.Location()
	if err != nil {
		return "", fmt.Errorf("tiny link %s did not redirect to a page (HTTP %d)", ref, resp.StatusCode)
	}
	return loc.String(), nil
}

// GetContent fetches any content by ID with the given expansions.
func (c *Client) GetContent(ctx context.Context, id string, expand ...string) (Content, error) {
	var out Content
	q := url.Values{}
	if len(expand) > 0 {
		q.Set("expand", strings.Join(expand, ","))
	}
	err := c.getJSON(ctx, "/content/"+url.PathEscape(id), q, &out)
	return out, err
}

// FindPage finds a page by exact title, optionally within a space.
func (c *Client) FindPage(ctx context.Context, title, spaceKey string) (Content, error) {
	cql := fmt.Sprintf(`type = page AND title = "%s"`, EscapeCQL(title))
	if spaceKey != "" {
		cql += fmt.Sprintf(` AND space = "%s"`, EscapeCQL(spaceKey))
	}
	res, err := c.Search(ctx, cql, 1, 0)
	if err != nil {
		return Content{}, err
	}
	if len(res.Results) == 0 {
		return Content{}, &Error{Code: CodeNotFound, Status: http.StatusNotFound, Message: fmt.Sprintf("no page titled %q", title)}
	}
	return res.Results[0].Content, nil
}

// Children lists a page's direct child pages.
func (c *Client) Children(ctx context.Context, id string, max int) ([]Content, error) {
	q := url.Values{"expand": {"space,version"}}
	return collect[Content](ctx, c, "/content/"+url.PathEscape(id)+"/child/page", q, max)
}

// PageURL is the browser URL of content.
func (c *Client) PageURL(p Content) string {
	if p.Links.WebUI != "" {
		return c.WebURL(p.Links.WebUI)
	}
	return c.WebBase() + "/pages/viewpage.action?pageId=" + url.QueryEscape(p.ID)
}

// NewPage describes a page to create.
type NewPage struct {
	Title    string
	SpaceKey string
	ParentID string // optional
	Storage  string
}

type pagePayload struct {
	ID        string   `json:"id,omitempty"`
	Type      string   `json:"type"`
	Title     string   `json:"title"`
	Space     Space    `json:"space"`
	Ancestors []idRef  `json:"ancestors,omitempty"`
	Body      Body     `json:"body"`
	Version   *Version `json:"version,omitempty"`
}

type idRef struct {
	ID string `json:"id"`
}

func storageBody(value string) Body {
	return Body{Storage: &Representation{Value: value, Representation: "storage"}}
}

// CreatePage creates a page, as a child of ParentID when set.
func (c *Client) CreatePage(ctx context.Context, p NewPage) (Content, error) {
	payload := pagePayload{Type: "page", Title: p.Title, Space: Space{Key: p.SpaceKey}, Body: storageBody(p.Storage)}
	if p.ParentID != "" {
		payload.Ancestors = []idRef{{ID: p.ParentID}}
	}
	var out Content
	err := c.send(ctx, http.MethodPost, "/content", nil, payload, &out)
	return out, err
}

// UpdatePage replaces the body (and optionally the title) of current, which
// must have been fetched with version and space. A concurrent edit makes the
// server answer 409, which is returned as-is rather than retried: retrying
// would silently overwrite someone else's change.
func (c *Client) UpdatePage(ctx context.Context, current Content, title, storage string) (Content, error) {
	if current.Version == nil || current.Space == nil {
		return Content{}, errors.New("update needs the current page with version and space expanded")
	}
	if title == "" {
		title = current.Title
	}
	payload := pagePayload{
		ID: current.ID, Type: "page", Title: title, Space: Space{Key: current.Space.Key},
		Body: storageBody(storage), Version: &Version{Number: current.Version.Number + 1},
	}
	var out Content
	err := c.send(ctx, http.MethodPut, "/content/"+url.PathEscape(current.ID), nil, payload, &out)
	return out, err
}

// MovePage re-parents page under parent within the same space. The body is
// resent unchanged because Data Center replaces whatever the PUT omits.
func (c *Client) MovePage(ctx context.Context, page, parent Content, title string) (Content, error) {
	if page.SpaceKey() == "" || parent.SpaceKey() == "" || page.Version == nil || page.Body == nil {
		return Content{}, errors.New("move needs both pages with space, and the page with body and version")
	}
	if page.SpaceKey() != parent.SpaceKey() {
		return Content{}, fmt.Errorf("cannot move across spaces: page is in %s, new parent is in %s", page.SpaceKey(), parent.SpaceKey())
	}
	if title == "" {
		title = page.Title
	}
	payload := pagePayload{
		ID: page.ID, Type: "page", Title: title, Space: Space{Key: page.SpaceKey()},
		Ancestors: []idRef{{ID: parent.ID}}, Body: storageBody(page.Storage()),
		Version: &Version{Number: page.Version.Number + 1},
	}
	var out Content
	err := c.send(ctx, http.MethodPut, "/content/"+url.PathEscape(page.ID), nil, payload, &out)
	return out, err
}

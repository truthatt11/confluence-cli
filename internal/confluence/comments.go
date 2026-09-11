package confluence

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
)

// CommentQuery selects a page's comments.
type CommentQuery struct {
	Location []string // inline, footer, resolved
	Start    int
	Limit    int
	All      bool // ignore Limit and fetch everything
}

// Comments lists a page's comments including replies (depth=all).
func (c *Client) Comments(ctx context.Context, pageID string, cq CommentQuery) ([]Content, error) {
	q := url.Values{
		"expand": {"body.storage,history,version,extensions.inlineProperties,extensions.resolution,ancestors"},
		"depth":  {"all"},
	}
	for _, l := range cq.Location {
		q.Add("location", l)
	}
	if cq.Start > 0 {
		q.Set("start", strconv.Itoa(cq.Start))
	}
	max := cq.Limit
	if cq.All {
		max = 0
	}
	return collect[Content](ctx, c, "/content/"+url.PathEscape(pageID)+"/child/comment", q, max)
}

type commentPayload struct {
	Type      string  `json:"type"`
	Container idType  `json:"container"`
	Body      Body    `json:"body"`
	Ancestors []idRef `json:"ancestors,omitempty"`
}

type idType struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

// CreateComment adds a footer comment, as a reply when parentID is set.
func (c *Client) CreateComment(ctx context.Context, pageID, parentID, storage string) (Content, error) {
	payload := commentPayload{Type: "comment", Container: idType{ID: pageID, Type: "page"}, Body: storageBody(storage)}
	if parentID != "" {
		payload.Ancestors = []idRef{{ID: parentID}}
	}
	var out Content
	err := c.send(ctx, http.MethodPost, "/content", nil, payload, &out)
	return out, err
}

// Properties lists a page's content properties.
func (c *Client) Properties(ctx context.Context, pageID string, max, start int) ([]Property, error) {
	q := url.Values{}
	if start > 0 {
		q.Set("start", strconv.Itoa(start))
	}
	return collect[Property](ctx, c, "/content/"+url.PathEscape(pageID)+"/property", q, max)
}

// GetProperty fetches one content property.
func (c *Client) GetProperty(ctx context.Context, pageID, key string) (Property, error) {
	var p Property
	err := c.getJSON(ctx, "/content/"+url.PathEscape(pageID)+"/property/"+url.PathEscape(key), nil, &p)
	return p, err
}

// SetProperty creates or updates a content property.
func (c *Client) SetProperty(ctx context.Context, pageID, key string, value json.RawMessage) (Property, error) {
	version := 1
	existing, err := c.GetProperty(ctx, pageID, key)
	var apiErr *Error
	switch {
	case err == nil && existing.Version != nil:
		version = existing.Version.Number + 1
	case err == nil, errors.As(err, &apiErr) && apiErr.Code == CodeNotFound:
	default:
		return Property{}, err
	}
	payload := Property{Key: key, Value: value, Version: &Version{Number: version}}
	var out Property
	err = c.send(ctx, http.MethodPut, "/content/"+url.PathEscape(pageID)+"/property/"+url.PathEscape(key), nil, payload, &out)
	return out, err
}

// Versions lists a page's versions, oldest first. Older Data Center releases
// only serve this under /rest/experimental.
func (c *Client) Versions(ctx context.Context, pageID string) ([]Version, error) {
	path := "/content/" + url.PathEscape(pageID) + "/version"
	vs, err := collectAt[Version](ctx, c, c.apiPath, path, nil, 0)
	var apiErr *Error
	if errors.As(err, &apiErr) && (apiErr.Status == http.StatusNotFound || apiErr.Status == http.StatusMethodNotAllowed) {
		vs, err = collectAt[Version](ctx, c, c.context+"/rest/experimental", path, nil, 0)
	}
	if err != nil {
		return nil, err
	}
	sorted := slices.Clone(vs)
	slices.SortFunc(sorted, func(a, b Version) int { return a.Number - b.Number })
	return sorted, nil
}

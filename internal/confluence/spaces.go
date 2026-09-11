package confluence

import (
	"context"
	"net/url"
)

// ListSpaces lists spaces visible to the user, up to max (0 = all).
func (c *Client) ListSpaces(ctx context.Context, max int) ([]Space, error) {
	return collect[Space](ctx, c, "/space", nil, max)
}

// GetSpace fetches one space, including its homepage.
func (c *Client) GetSpace(ctx context.Context, key string) (Space, error) {
	var s Space
	err := c.getJSON(ctx, "/space/"+url.PathEscape(key), url.Values{"expand": {"homepage"}}, &s)
	return s, err
}

// CurrentUser returns the authenticated user; Type is "anonymous" when the
// credentials were not accepted but the server allows anonymous access.
func (c *Client) CurrentUser(ctx context.Context) (User, error) {
	var u User
	err := c.getJSON(ctx, "/user/current", nil, &u)
	return u, err
}

// DisplayNames maps user keys to display names, falling back to the key.
// Lookups are cached for the life of the client, which matters when
// exporting many pages that mention the same people.
func (c *Client) DisplayNames(ctx context.Context, keys []string) map[string]string {
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		c.mu.Lock()
		name, ok := c.names[k]
		c.mu.Unlock()
		if !ok {
			name = c.lookupName(ctx, k)
			c.mu.Lock()
			c.names[k] = name
			c.mu.Unlock()
		}
		out[k] = name
	}
	return out
}

func (c *Client) lookupName(ctx context.Context, key string) string {
	var u User
	if err := c.getJSON(ctx, "/user", url.Values{"key": {key}}, &u); err != nil {
		return key
	}
	for _, n := range []string{u.DisplayName, u.Username} {
		if n != "" {
			return n
		}
	}
	return key
}

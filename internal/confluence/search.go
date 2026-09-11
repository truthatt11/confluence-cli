package confluence

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

// SearchPage is one page of CQL search results.
type SearchPage struct {
	Results   []SearchResult `json:"results"`
	TotalSize int            `json:"totalSize"`
	Start     int            `json:"start"`
	Limit     int            `json:"limit"`
}

// Search runs a CQL query and returns one page of results.
func (c *Client) Search(ctx context.Context, cql string, limit, start int) (SearchPage, error) {
	q := url.Values{
		"cql":    {cql},
		"limit":  {strconv.Itoa(limit)},
		"start":  {strconv.Itoa(start)},
		"expand": {"content.space,content.version"},
	}
	var page SearchPage
	if err := c.getJSON(ctx, "/search", q, &page); err != nil {
		return SearchPage{}, err
	}
	cleaned := make([]SearchResult, len(page.Results))
	for i, r := range page.Results {
		cleaned[i] = r.clean()
	}
	page.Results = cleaned
	return page, nil
}

// TextQuery builds a full-text CQL query for plain words.
func TextQuery(text string) string { return `text ~ "` + EscapeCQL(text) + `"` }

// EscapeCQL escapes a value for use inside a double-quoted CQL string.
func EscapeCQL(s string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s)
}

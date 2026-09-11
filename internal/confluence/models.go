package confluence

import (
	"encoding/json"
	"strings"
)

// Links are the _links of a Data Center entity; paths are relative to Base.
type Links struct {
	WebUI    string `json:"webui,omitempty"`
	TinyUI   string `json:"tinyui,omitempty"`
	Download string `json:"download,omitempty"`
	Base     string `json:"base,omitempty"`
}

// User is a Confluence user. Type is "anonymous" when not logged in.
type User struct {
	Type        string `json:"type,omitempty"`
	Username    string `json:"username,omitempty"`
	UserKey     string `json:"userKey,omitempty"`
	DisplayName string `json:"displayName,omitempty"`
}

// Space is a Confluence space.
type Space struct {
	ID       int64    `json:"id,omitempty"`
	Key      string   `json:"key"`
	Name     string   `json:"name,omitempty"`
	Type     string   `json:"type,omitempty"`
	Homepage *Content `json:"homepage,omitempty"`
	Links    Links    `json:"_links"`
}

// Version is one revision of a piece of content.
type Version struct {
	Number    int    `json:"number"`
	When      string `json:"when,omitempty"`
	Message   string `json:"message,omitempty"`
	MinorEdit bool   `json:"minorEdit,omitempty"`
	By        *User  `json:"by,omitempty"`
}

// Representation is a body in one format.
type Representation struct {
	Value          string `json:"value"`
	Representation string `json:"representation"`
}

// Body holds the formats requested through expand.
type Body struct {
	Storage *Representation `json:"storage,omitempty"`
	View    *Representation `json:"view,omitempty"`
}

// History is creation metadata.
type History struct {
	CreatedBy   *User  `json:"createdBy,omitempty"`
	CreatedDate string `json:"createdDate,omitempty"`
}

// Metadata carries the media type of attachments.
type Metadata struct {
	MediaType string `json:"mediaType,omitempty"`
}

// Content is a page, blog post, comment or attachment.
type Content struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Status     string         `json:"status,omitempty"`
	Title      string         `json:"title"`
	Space      *Space         `json:"space,omitempty"`
	Version    *Version       `json:"version,omitempty"`
	History    *History       `json:"history,omitempty"`
	Ancestors  []Content      `json:"ancestors,omitempty"`
	Container  *Content       `json:"container,omitempty"`
	Body       *Body          `json:"body,omitempty"`
	Metadata   *Metadata      `json:"metadata,omitempty"`
	Extensions map[string]any `json:"extensions,omitempty"`
	Links      Links          `json:"_links"`
}

// Storage returns the storage-format body, or "" when it was not expanded.
func (c Content) Storage() string {
	if c.Body == nil || c.Body.Storage == nil {
		return ""
	}
	return c.Body.Storage.Value
}

// VersionNumber returns the version, or 0 when it was not expanded.
func (c Content) VersionNumber() int {
	if c.Version == nil {
		return 0
	}
	return c.Version.Number
}

// SpaceKey returns the space key, or "" when it was not expanded.
func (c Content) SpaceKey() string {
	if c.Space == nil {
		return ""
	}
	return c.Space.Key
}

// ParentID is the closest ancestor of the given type ("" matches any).
func (c Content) ParentID(ofType string) string {
	for i := len(c.Ancestors) - 1; i >= 0; i-- {
		if ofType == "" || c.Ancestors[i].Type == ofType {
			return c.Ancestors[i].ID
		}
	}
	return ""
}

// Extension returns a string-ish extension such as a comment's location,
// looking inside objects shaped like {"value": ...} or {"status": ...}.
func (c Content) Extension(name string) string {
	switch v := c.Extensions[name].(type) {
	case string:
		return v
	case map[string]any:
		for _, k := range []string{"value", "status", "name"} {
			if s, ok := v[k].(string); ok {
				return s
			}
		}
	}
	return ""
}

// FileSize returns an attachment's size in bytes.
func (c Content) FileSize() int64 {
	if f, ok := c.Extensions["fileSize"].(float64); ok {
		return int64(f)
	}
	return 0
}

// MediaType returns an attachment's media type.
func (c Content) MediaType() string {
	if c.Metadata != nil && c.Metadata.MediaType != "" {
		return c.Metadata.MediaType
	}
	return c.Extension("mediaType")
}

// Property is a content property: JSON stored on a page.
type Property struct {
	ID      string          `json:"id,omitempty"`
	Key     string          `json:"key"`
	Value   json.RawMessage `json:"value"`
	Version *Version        `json:"version,omitempty"`
}

// SearchResult is one hit from CQL search.
type SearchResult struct {
	Content      Content `json:"content"`
	Title        string  `json:"title"`
	Excerpt      string  `json:"excerpt"`
	URL          string  `json:"url"`
	LastModified string  `json:"lastModified,omitempty"`
}

var highlight = strings.NewReplacer("@@@hl@@@", "", "@@@endhl@@@", "")

// clean strips the search highlight markers Data Center inserts.
func (r SearchResult) clean() SearchResult {
	r.Title = highlight.Replace(r.Title)
	r.Excerpt = strings.TrimSpace(highlight.Replace(r.Excerpt))
	return r
}

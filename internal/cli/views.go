package cli

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/truthatt11/confluence-cli/internal/confluence"
)

// pageView is the stable JSON shape for a page.
type pageView struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Type      string `json:"type,omitempty"`
	Space     string `json:"space,omitempty"`
	Version   int    `json:"version,omitempty"`
	ParentID  string `json:"parentId,omitempty"`
	URL       string `json:"url"`
	CreatedBy string `json:"createdBy,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
	UpdatedBy string `json:"updatedBy,omitempty"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

func viewOf(c *confluence.Client, p confluence.Content) pageView {
	v := pageView{
		ID: p.ID, Title: p.Title, Type: p.Type, Space: p.SpaceKey(), Version: p.VersionNumber(),
		ParentID: p.ParentID(""), URL: c.PageURL(p),
	}
	if p.History != nil {
		v.CreatedBy, v.CreatedAt = userName(p.History.CreatedBy), p.History.CreatedDate
	}
	if p.Version != nil {
		v.UpdatedBy, v.UpdatedAt = userName(p.Version.By), p.Version.When
	}
	return v
}

func userName(u *confluence.User) string {
	if u == nil {
		return ""
	}
	for _, n := range []string{u.DisplayName, u.Username, u.UserKey} {
		if n != "" {
			return n
		}
	}
	return ""
}

func printPage(w io.Writer, v pageView) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	row := func(label, value string) {
		if value != "" {
			fmt.Fprintf(tw, "%s:\t%s\n", label, value)
		}
	}
	row("Title", v.Title)
	row("ID", v.ID)
	row("Type", v.Type)
	row("Space", v.Space)
	if v.Version > 0 {
		row("Version", fmt.Sprintf("%d  %s  %s", v.Version, v.UpdatedAt, v.UpdatedBy))
	}
	row("Created", joinNonEmpty(v.CreatedAt, v.CreatedBy))
	row("Parent", v.ParentID)
	row("URL", v.URL)
	_ = tw.Flush()
}

func joinNonEmpty(parts ...string) string {
	out := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		if out != "" {
			out += "  "
		}
		out += p
	}
	return out
}

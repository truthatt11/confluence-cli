package cli

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/truthatt11/confluence-cli/internal/confluence"
)

type searchHit struct {
	ID           string `json:"id"`
	Title        string `json:"title"`
	Type         string `json:"type"`
	Space        string `json:"space,omitempty"`
	URL          string `json:"url"`
	Excerpt      string `json:"excerpt,omitempty"`
	LastModified string `json:"lastModified,omitempty"`
}

func (a *app) searchCmd() *cobra.Command {
	var limit, start int
	var rawCQL bool
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search content",
		Long: "Full-text search. With --cql the query is passed through as CQL, for example:\n" +
			`  cfl search --cql 'space = OPS AND type = page AND lastmodified > now("-7d")'`,
		Args:        cobra.ExactArgs(1),
		Annotations: readOnly,
		RunE: func(cmd *cobra.Command, args []string) error {
			if limit < 1 || start < 0 {
				return invalid("--limit must be at least 1 and --start not negative")
			}
			c, err := a.connect(cmd)
			if err != nil {
				return err
			}
			cql := confluence.TextQuery(args[0])
			if rawCQL {
				cql = args[0]
			}
			res, err := c.Search(cmd.Context(), cql, limit, start)
			if err != nil {
				return err
			}
			hits := make([]searchHit, len(res.Results))
			for i, r := range res.Results {
				hits[i] = searchHit{
					ID: r.Content.ID, Title: r.Title, Type: r.Content.Type, Space: r.Content.SpaceKey(),
					URL: c.PageURL(r.Content), Excerpt: r.Excerpt, LastModified: r.LastModified,
				}
			}
			out := map[string]any{"total": res.TotalSize, "start": start, "limit": limit, "results": hits}
			return a.emit(out, func(w io.Writer) { printHits(w, hits, res.TotalSize) })
		},
	}
	cmd.Flags().IntVarP(&limit, "limit", "l", 10, "maximum results")
	cmd.Flags().IntVar(&start, "start", 0, "index of the first result, for paging")
	cmd.Flags().BoolVar(&rawCQL, "cql", false, "treat the query as raw CQL")
	return cmd
}

func printHits(w io.Writer, hits []searchHit, total int) {
	for _, h := range hits {
		space := ""
		if h.Space != "" {
			space = "  [" + h.Space + "]"
		}
		fmt.Fprintf(w, "%s  %s%s\n", h.ID, h.Title, space)
		if h.Excerpt != "" {
			fmt.Fprintf(w, "    %s\n", h.Excerpt)
		}
	}
	fmt.Fprintf(w, "(%d of %d results)\n", len(hits), total)
}

func (a *app) spacesCmd() *cobra.Command {
	var limit int
	var all bool
	cmd := &cobra.Command{
		Use:         "spaces",
		Short:       "List spaces",
		Args:        cobra.NoArgs,
		Annotations: readOnly,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.connect(cmd)
			if err != nil {
				return err
			}
			max := limit
			if all {
				max = 0
			}
			spaces, err := c.ListSpaces(cmd.Context(), max)
			if err != nil {
				return err
			}
			return a.emit(spaces, func(w io.Writer) {
				tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
				fmt.Fprintln(tw, "KEY\tNAME\tTYPE")
				for _, s := range spaces {
					fmt.Fprintf(tw, "%s\t%s\t%s\n", s.Key, s.Name, s.Type)
				}
				_ = tw.Flush()
			})
		},
	}
	cmd.Flags().IntVarP(&limit, "limit", "l", 500, "maximum spaces")
	cmd.Flags().BoolVar(&all, "all", false, "list every space (ignores --limit)")
	return cmd
}

func (a *app) spaceLookupCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "space-lookup <key>",
		Short:       "Show a space and its homepage",
		Args:        cobra.ExactArgs(1),
		Annotations: readOnly,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.connect(cmd)
			if err != nil {
				return err
			}
			s, err := c.GetSpace(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			out := map[string]any{"key": s.Key, "name": s.Name, "type": s.Type, "url": c.WebURL(s.Links.WebUI)}
			if s.Homepage != nil {
				out["homepageId"], out["homepageTitle"] = s.Homepage.ID, s.Homepage.Title
			}
			return a.emit(out, func(w io.Writer) {
				fmt.Fprintf(w, "Key:       %s\nName:      %s\nType:      %s\n", s.Key, s.Name, s.Type)
				if s.Homepage != nil {
					fmt.Fprintf(w, "Homepage:  %s  %s\n", s.Homepage.ID, s.Homepage.Title)
				}
				fmt.Fprintf(w, "URL:       %s\n", out["url"])
			})
		},
	}
}

package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/truthatt11/confluence-cli/internal/confluence"
)

const defaultMaxDepth = 10

type treeEntry struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	ParentID string `json:"parentId"`
	Depth    int    `json:"depth"`
	URL      string `json:"url"`
}

// walkTree visits the descendants of rootID depth-first, down to maxDepth
// levels (direct children are depth 1). visit returns whether to descend
// into the page it was given.
func walkTree(ctx context.Context, c *confluence.Client, rootID string, maxDepth int,
	visit func(p confluence.Content, depth int, parentID string) (bool, error)) error {
	var walk func(id string, depth int) error
	walk = func(id string, depth int) error {
		if depth > maxDepth {
			return nil
		}
		kids, err := c.Children(ctx, id, 0)
		if err != nil {
			return err
		}
		for _, k := range kids {
			descend, err := visit(k, depth, id)
			if err != nil {
				return err
			}
			if descend {
				if err := walk(k.ID, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return walk(rootID, 1)
}

func (a *app) childrenCmd() *cobra.Command {
	var recursive, showURL, showID bool
	var maxDepth int
	var format string
	cmd := &cobra.Command{
		Use:         "children <page>",
		Short:       "List child pages",
		Long:        "List a page's child pages; with -r, all descendants.\n" + pageArgHelp,
		Args:        cobra.ExactArgs(1),
		Annotations: readOnly,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "list" && format != "tree" {
				return invalid("--format must be list or tree")
			}
			c, err := a.connect(cmd)
			if err != nil {
				return err
			}
			id, err := c.ResolvePageID(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			depth := 1
			if recursive {
				depth = maxDepth
			}
			entries := []treeEntry{}
			err = walkTree(cmd.Context(), c, id, depth, func(p confluence.Content, d int, parent string) (bool, error) {
				entries = append(entries, treeEntry{ID: p.ID, Title: p.Title, ParentID: parent, Depth: d, URL: c.PageURL(p)})
				return true, nil
			})
			if err != nil {
				return err
			}
			return a.emit(entries, func(w io.Writer) { printTree(w, entries, format == "tree", showID, showURL) })
		},
	}
	cmd.Flags().BoolVarP(&recursive, "recursive", "r", false, "include all descendants")
	cmd.Flags().IntVar(&maxDepth, "max-depth", defaultMaxDepth, "how deep -r goes")
	cmd.Flags().StringVar(&format, "format", "list", "list or tree")
	cmd.Flags().BoolVar(&showURL, "show-url", false, "print page URLs")
	cmd.Flags().BoolVar(&showID, "show-id", false, "print page IDs")
	return cmd
}

func printTree(w io.Writer, entries []treeEntry, tree, showID, showURL bool) {
	for _, e := range entries {
		indent := ""
		if tree {
			indent = strings.Repeat("  ", e.Depth-1)
		}
		line := indent + "- " + e.Title
		if showID {
			line += " [" + e.ID + "]"
		}
		if showURL {
			line += " " + e.URL
		}
		fmt.Fprintln(w, line)
	}
}

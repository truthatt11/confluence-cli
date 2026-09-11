package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/truthatt11/confluence-cli/internal/confluence"
	"github.com/truthatt11/confluence-cli/internal/convert"
)

type commentView struct {
	ID         string `json:"id"`
	PageID     string `json:"pageId,omitempty"`
	ParentID   string `json:"parentId,omitempty"`
	Author     string `json:"author"`
	CreatedAt  string `json:"createdAt,omitempty"`
	Location   string `json:"location,omitempty"`
	Resolution string `json:"resolution,omitempty"`
	Body       string `json:"body"`
}

func (a *app) commentView(ctx context.Context, c *confluence.Client, cm confluence.Content, format string) commentView {
	v := commentView{
		ID: cm.ID, ParentID: cm.ParentID("comment"), Location: cm.Extension("location"),
		Resolution: cm.Extension("resolution"), Body: cm.Storage(),
	}
	if cm.Container != nil {
		v.PageID = cm.Container.ID
	}
	if cm.History != nil {
		v.Author, v.CreatedAt = userName(cm.History.CreatedBy), cm.History.CreatedDate
	}
	if format == "markdown" {
		keys, _ := convert.UserKeys(v.Body)
		if res, err := convert.StorageToMarkdown(v.Body, convert.StorageOptions{Users: c.DisplayNames(ctx, keys), WebBase: c.WebBase()}); err == nil {
			v.Body = res.Markdown
		}
	}
	return v
}

func printComment(w io.Writer, v commentView) {
	meta := joinNonEmpty(v.Author, v.CreatedAt, v.Location, v.Resolution)
	if v.ParentID != "" {
		meta += "  (reply to " + v.ParentID + ")"
	}
	fmt.Fprintf(w, "[%s] %s\n%s\n\n", v.ID, meta, strings.TrimSpace(v.Body))
}

func (a *app) commentsCmd() *cobra.Command {
	var format string
	var limit, start int
	var all bool
	var location []string
	cmd := &cobra.Command{
		Use:         "comments <page>",
		Short:       "List a page's comments, including replies",
		Args:        cobra.ExactArgs(1),
		Annotations: readOnly,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "markdown" && format != "storage" {
				return invalid("--format must be markdown or storage")
			}
			c, err := a.connect(cmd)
			if err != nil {
				return err
			}
			id, err := c.ResolvePageID(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			list, err := c.Comments(cmd.Context(), id, confluence.CommentQuery{Location: location, Start: start, Limit: limit, All: all})
			if err != nil {
				return err
			}
			views := make([]commentView, len(list))
			for i, cm := range list {
				views[i] = a.commentView(cmd.Context(), c, cm, format)
			}
			return a.emit(views, func(w io.Writer) {
				for _, v := range views {
					printComment(w, v)
				}
			})
		},
	}
	cmd.Flags().StringVarP(&format, "format", "f", "markdown", "body format: markdown or storage")
	cmd.Flags().IntVarP(&limit, "limit", "l", 25, "maximum comments")
	cmd.Flags().IntVar(&start, "start", 0, "index of the first comment")
	cmd.Flags().BoolVar(&all, "all", false, "fetch every comment")
	cmd.Flags().StringSliceVar(&location, "location", nil, "filter by location: inline, footer, resolved")
	return cmd
}

func (a *app) commentLookupCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "comment-lookup <comment-id>",
		Short:       "Show one comment",
		Args:        cobra.ExactArgs(1),
		Annotations: readOnly,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.connect(cmd)
			if err != nil {
				return err
			}
			cm, err := c.GetContent(cmd.Context(), args[0], "container", "ancestors", "body.storage", "history", "extensions.resolution")
			if err != nil {
				return err
			}
			if cm.Type != "comment" {
				return invalid("%s is a %s, not a comment", args[0], cm.Type)
			}
			v := a.commentView(cmd.Context(), c, cm, "markdown")
			return a.emit(v, func(w io.Writer) {
				fmt.Fprintf(w, "Page: %s\n", v.PageID)
				printComment(w, v)
			})
		},
	}
}

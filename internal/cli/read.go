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

const pageArgHelp = "<page> is a page ID or a URL on your Confluence site."

func (a *app) readCmd() *cobra.Command {
	var format string
	cmd := &cobra.Command{
		Use:         "read <page>",
		Short:       "Print a page's content",
		Long:        "Print a page's content as Markdown (default), storage format, or rendered HTML.\n" + pageArgHelp,
		Args:        cobra.ExactArgs(1),
		Annotations: readOnly,
		RunE: func(cmd *cobra.Command, args []string) error {
			if format != "markdown" && format != "storage" && format != "html" {
				return invalid("--format must be markdown, storage or html")
			}
			c, err := a.connect(cmd)
			if err != nil {
				return err
			}
			expand := []string{"body.storage", "space", "version"}
			if format == "html" {
				expand = append(expand, "body.view")
			}
			page, err := a.getPage(cmd.Context(), c, args[0], expand...)
			if err != nil {
				return err
			}
			return a.printContent(cmd.Context(), c, page, format)
		},
	}
	cmd.Flags().StringVarP(&format, "format", "f", "markdown", "markdown, storage or html")
	return cmd
}

func (a *app) printContent(ctx context.Context, c *confluence.Client, page confluence.Content, format string) error {
	content, lossy := page.Storage(), []string(nil)
	switch format {
	case "html":
		if page.Body != nil && page.Body.View != nil {
			content = page.Body.View.Value
		}
	case "markdown":
		res, err := a.toMarkdown(ctx, c, page, "attachments")
		if err != nil {
			return err
		}
		content, lossy = res.Markdown, res.Lossy
		if len(lossy) > 0 && !a.json {
			a.warnf("note: Markdown cannot reproduce %s; to change this page use `cfl edit` and update it in storage format", strings.Join(lossy, ", "))
		}
	}
	v := viewOf(c, page)
	out := map[string]any{
		"id": v.ID, "title": v.Title, "space": v.Space, "version": v.Version, "url": v.URL,
		"format": format, "content": content, "lossy": emptyIfNil(lossy),
	}
	return a.emit(out, func(w io.Writer) { fmt.Fprintln(w, content) })
}

func emptyIfNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// toMarkdown converts a page body, resolving mentions and the children macro first.
func (a *app) toMarkdown(ctx context.Context, c *confluence.Client, page confluence.Content, attachmentsDir string) (convert.Result, error) {
	storage := page.Storage()
	keys, err := convert.UserKeys(storage)
	if err != nil {
		return convert.Result{}, fmt.Errorf("page %s: %w (use --format storage to see the raw body)", page.ID, err)
	}
	opts := convert.StorageOptions{
		AttachmentsDir: attachmentsDir, Users: c.DisplayNames(ctx, keys),
		WebBase: c.WebBase(), SpaceKey: page.SpaceKey(),
	}
	if has, _ := convert.HasMacro(storage, "children"); has && page.Type != "comment" {
		kids, err := c.Children(ctx, page.ID, 0)
		if err != nil {
			a.warnf("warning: could not list child pages for the children macro: %v", err)
		}
		for _, k := range kids {
			opts.Children = append(opts.Children, convert.Link{Title: k.Title, URL: c.PageURL(k)})
		}
	}
	return convert.StorageToMarkdown(storage, opts)
}

// getPage resolves a page reference and fetches it.
func (a *app) getPage(ctx context.Context, c *confluence.Client, ref string, expand ...string) (confluence.Content, error) {
	id, err := c.ResolvePageID(ctx, ref)
	if err != nil {
		return confluence.Content{}, err
	}
	return c.GetContent(ctx, id, expand...)
}

func (a *app) infoCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "info <page>",
		Short:       "Show a page's metadata",
		Long:        "Show a page's title, space, version, authors, parent and URL.\n" + pageArgHelp,
		Args:        cobra.ExactArgs(1),
		Annotations: readOnly,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.connect(cmd)
			if err != nil {
				return err
			}
			page, err := a.getPage(cmd.Context(), c, args[0], "space", "history", "version", "ancestors")
			if err != nil {
				return err
			}
			v := viewOf(c, page)
			return a.emit(v, func(w io.Writer) { printPage(w, v) })
		},
	}
}

func (a *app) findCmd() *cobra.Command {
	var space string
	cmd := &cobra.Command{
		Use:         "find <title>",
		Short:       "Find a page by its exact title",
		Args:        cobra.ExactArgs(1),
		Annotations: readOnly,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.connect(cmd)
			if err != nil {
				return err
			}
			page, err := c.FindPage(cmd.Context(), args[0], space)
			if err != nil {
				return err
			}
			v := viewOf(c, page)
			return a.emit(v, func(w io.Writer) { printPage(w, v) })
		},
	}
	cmd.Flags().StringVarP(&space, "space", "s", "", "limit the search to a space key")
	return cmd
}

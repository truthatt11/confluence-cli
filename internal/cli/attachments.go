package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/truthatt11/confluence-cli/internal/confluence"
)

type attachmentView struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	MediaType string `json:"mediaType"`
	Size      int64  `json:"size"`
	Version   int    `json:"version,omitempty"`
	PageID    string `json:"pageId,omitempty"`
	URL       string `json:"url,omitempty"`
	SavedTo   string `json:"savedTo,omitempty"`
}

func attachmentOf(c *confluence.Client, att confluence.Content) attachmentView {
	v := attachmentView{ID: att.ID, Title: att.Title, MediaType: att.MediaType(), Size: att.FileSize(), Version: att.VersionNumber()}
	if att.Container != nil {
		v.PageID = att.Container.ID
	}
	if att.Links.Download != "" {
		v.URL = c.WebURL(att.Links.Download)
	}
	return v
}

// matchAny reports whether name matches one of the glob patterns (none = all).
func matchAny(name string, patterns []string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, p := range patterns {
		if ok, _ := path.Match(p, name); ok {
			return true
		}
	}
	return false
}

// saveAttachment downloads att into dir under its base name only, so a
// crafted title like "../../.ssh/config" cannot escape dir.
func saveAttachment(ctx context.Context, c *confluence.Client, att confluence.Content, dir string) (string, error) {
	name := filepath.Base(filepath.Clean("/" + att.Title))
	if name == "/" || name == "." {
		return "", fmt.Errorf("attachment %s has no usable file name", att.ID)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dest := filepath.Join(dir, name)
	tmp, err := os.CreateTemp(dir, "."+name+".*")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name()) // no-op after rename
	if _, err := c.Download(ctx, att, tmp); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	return dest, os.Rename(tmp.Name(), dest)
}

func (a *app) attachmentsCmd() *cobra.Command {
	var limit int
	var pattern, dest string
	var download bool
	cmd := &cobra.Command{
		Use:         "attachments <page>",
		Short:       "List (and optionally download) a page's attachments",
		Args:        cobra.ExactArgs(1),
		Annotations: readOnly,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.connect(cmd)
			if err != nil {
				return err
			}
			id, err := c.ResolvePageID(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			atts, err := c.Attachments(cmd.Context(), id, limit)
			if err != nil {
				return err
			}
			views := []attachmentView{}
			for _, att := range atts {
				if pattern != "" && !matchAny(att.Title, []string{pattern}) {
					continue
				}
				v := attachmentOf(c, att)
				if download {
					if v.SavedTo, err = saveAttachment(cmd.Context(), c, att, dest); err != nil {
						return err
					}
				}
				views = append(views, v)
			}
			return a.emit(views, func(w io.Writer) { printAttachments(w, views) })
		},
	}
	cmd.Flags().IntVarP(&limit, "limit", "l", 0, "maximum attachments (default all)")
	cmd.Flags().StringVarP(&pattern, "pattern", "p", "", `only file names matching this glob, e.g. "*.png"`)
	cmd.Flags().BoolVarP(&download, "download", "d", false, "download the listed attachments")
	cmd.Flags().StringVar(&dest, "dest", ".", "directory for downloads")
	return cmd
}

func printAttachments(w io.Writer, views []attachmentView) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tFILE\tTYPE\tSIZE\tSAVED")
	for _, v := range views {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\n", v.ID, v.Title, v.MediaType, v.Size, v.SavedTo)
	}
	_ = tw.Flush()
}

func (a *app) attachmentLookupCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "attachment-lookup <attachment-id>",
		Short:       "Show one attachment",
		Args:        cobra.ExactArgs(1),
		Annotations: readOnly,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.connect(cmd)
			if err != nil {
				return err
			}
			att, err := c.GetContent(cmd.Context(), args[0], "container", "version")
			if err != nil {
				return err
			}
			if att.Type != "attachment" {
				return invalid("%s is a %s, not an attachment", args[0], att.Type)
			}
			v := attachmentOf(c, att)
			return a.emit(v, func(w io.Writer) {
				fmt.Fprintf(w, "ID:    %s\nFile:  %s\nType:  %s\nSize:  %d\nPage:  %s\nURL:   %s\n", v.ID, v.Title, v.MediaType, v.Size, v.PageID, v.URL)
			})
		},
	}
}

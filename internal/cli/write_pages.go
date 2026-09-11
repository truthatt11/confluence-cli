package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/truthatt11/confluence-cli/internal/confluence"
)

func (a *app) createCmd() *cobra.Command {
	var cf contentFlags
	var dry bool
	cmd := &cobra.Command{
		Use:   "create <title> <space-key>",
		Short: "Create a page at the top of a space",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.create(cmd, confluence.NewPage{Title: args[0], SpaceKey: args[1]}, cf, dry)
		},
	}
	cf.register(cmd)
	cmd.Flags().BoolVar(&dry, "dry-run", false, "validate and show the result without creating")
	return cmd
}

func (a *app) createChildCmd() *cobra.Command {
	var cf contentFlags
	var dry bool
	cmd := &cobra.Command{
		Use:   "create-child <title> <parent-page>",
		Short: "Create a page under another page",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.create(cmd, confluence.NewPage{Title: args[0], ParentID: args[1]}, cf, dry)
		},
	}
	cf.register(cmd)
	cmd.Flags().BoolVar(&dry, "dry-run", false, "validate and show the result without creating")
	return cmd
}

// create makes a page; when p.ParentID is set it is resolved and supplies the space.
func (a *app) create(cmd *cobra.Command, p confluence.NewPage, cf contentFlags, dry bool) error {
	storage, err := cf.storage(a.stdin)
	if err != nil {
		return err
	}
	c, err := a.connect(cmd)
	if err != nil {
		return err
	}
	var parent confluence.Content
	if p.ParentID != "" {
		if parent, err = a.getPage(cmd.Context(), c, p.ParentID, "space"); err != nil {
			return err
		}
		p.ParentID, p.SpaceKey = parent.ID, parent.SpaceKey()
	}
	p.Storage = storage
	if dry {
		preview := confluence.Content{Title: p.Title, Type: "page", Space: &confluence.Space{Key: p.SpaceKey}}
		details := map[string]any{"space": p.SpaceKey, "parentId": p.ParentID, "bytes": len(storage)}
		return a.dryRun(c, "create", preview, details, fmt.Sprintf("would create %q in space %s (%d bytes)", p.Title, p.SpaceKey, len(storage)))
	}
	created, err := c.CreatePage(cmd.Context(), p)
	if err != nil {
		return err
	}
	return a.done(c, "created", created)
}

func (a *app) updateCmd() *cobra.Command {
	var cf contentFlags
	var title string
	var dry, allowLossy bool
	cmd := &cobra.Command{
		Use:   "update <page>",
		Short: "Replace a page's content and/or title",
		Long: "Replace a page's content (--file/--content) and/or its title (--title).\n" +
			"Updating from Markdown is refused when the page holds content Markdown cannot\n" +
			"reproduce (macros, layouts, mentions…); edit the storage format instead, or pass --allow-lossy.\n" + pageArgHelp,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !cf.given() && title == "" {
				return invalid("nothing to update: give --file, --content or --title")
			}
			c, err := a.connect(cmd)
			if err != nil {
				return err
			}
			current, err := a.getPage(cmd.Context(), c, args[0], "body.storage", "space", "version")
			if err != nil {
				return err
			}
			storage := current.Storage()
			if cf.given() {
				if cf.format == "markdown" && !allowLossy {
					if err := ensureLossless(current); err != nil {
						return err
					}
				}
				if storage, err = cf.storage(a.stdin); err != nil {
					return err
				}
			}
			if dry {
				v := current.VersionNumber()
				details := map[string]any{"fromVersion": v, "toVersion": v + 1, "bytesBefore": len(current.Storage()), "bytesAfter": len(storage), "title": title}
				return a.dryRun(c, "update", current, details, fmt.Sprintf("would update %q (id %s) from version %d to %d, %d → %d bytes", current.Title, current.ID, v, v+1, len(current.Storage()), len(storage)))
			}
			updated, err := c.UpdatePage(cmd.Context(), current, title, storage)
			if err != nil {
				return err
			}
			return a.done(c, "updated", updated)
		},
	}
	cf.register(cmd)
	cmd.Flags().StringVarP(&title, "title", "t", "", "new title")
	cmd.Flags().BoolVar(&dry, "dry-run", false, "validate and show the change without updating")
	cmd.Flags().BoolVar(&allowLossy, "allow-lossy", false, "update from Markdown even if Confluence-only content will be lost")
	return cmd
}

func (a *app) editCmd() *cobra.Command {
	var output string
	cmd := &cobra.Command{
		Use:   "edit <page>",
		Short: "Fetch a page's storage format for editing",
		Long: "Print (or save with -o) a page's storage format. Change it, then apply it with\n" +
			"`cfl update <page> -f <file>`. This path keeps every macro and layout intact.\n" + pageArgHelp,
		Args:        cobra.ExactArgs(1),
		Annotations: readOnly,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.connect(cmd)
			if err != nil {
				return err
			}
			p, err := a.getPage(cmd.Context(), c, args[0], "body.storage", "space", "version")
			if err != nil {
				return err
			}
			a.warnf("%s (id %s, space %s, version %d)", p.Title, p.ID, p.SpaceKey(), p.VersionNumber())
			if output != "" {
				if err := os.WriteFile(output, []byte(p.Storage()), 0o644); err != nil {
					return err
				}
				a.warnf("saved to %s; apply changes with: cfl update %s -f %s", output, p.ID, output)
			}
			out := map[string]any{"id": p.ID, "title": p.Title, "space": p.SpaceKey(), "version": p.VersionNumber(), "content": p.Storage()}
			return a.emit(out, func(w io.Writer) {
				if output == "" {
					fmt.Fprintln(w, p.Storage())
				}
			})
		},
	}
	cmd.Flags().StringVarP(&output, "output", "o", "", "save the storage format to this file")
	return cmd
}

func (a *app) moveCmd() *cobra.Command {
	var title string
	var dry bool
	cmd := &cobra.Command{
		Use:   "move <page> <new-parent-page>",
		Short: "Move a page under another page in the same space",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.connect(cmd)
			if err != nil {
				return err
			}
			page, err := a.getPage(cmd.Context(), c, args[0], "body.storage", "space", "version", "ancestors")
			if err != nil {
				return err
			}
			parent, err := a.getPage(cmd.Context(), c, args[1], "space")
			if err != nil {
				return err
			}
			if page.SpaceKey() != parent.SpaceKey() {
				return invalid("cannot move across spaces: page is in %s, new parent is in %s", page.SpaceKey(), parent.SpaceKey())
			}
			if dry {
				details := map[string]any{"fromParentId": page.ParentID(""), "toParentId": parent.ID}
				return a.dryRun(c, "move", page, details, fmt.Sprintf("would move %q under %q", page.Title, parent.Title))
			}
			moved, err := c.MovePage(cmd.Context(), page, parent, title)
			if err != nil {
				return err
			}
			return a.done(c, "moved", moved)
		},
	}
	cmd.Flags().StringVarP(&title, "title", "t", "", "also rename the page")
	cmd.Flags().BoolVar(&dry, "dry-run", false, "check and show the move without doing it")
	return cmd
}

package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/truthatt11/confluence-cli/internal/confluence"
)

func (a *app) commentCmd() *cobra.Command {
	var cf contentFlags
	var parent string
	var dry bool
	cmd := &cobra.Command{
		Use:   "comment <page>",
		Short: "Add a footer comment, or a reply with --parent",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			storage, err := cf.storage(a.stdin)
			if err != nil {
				return err
			}
			c, err := a.connect(cmd)
			if err != nil {
				return err
			}
			page, err := a.getPage(cmd.Context(), c, args[0], "space")
			if err != nil {
				return err
			}
			if dry {
				return a.dryRun(c, "comment", page, map[string]any{"parentId": parent, "bytes": len(storage)},
					fmt.Sprintf("would comment on %q (%d bytes)", page.Title, len(storage)))
			}
			cm, err := c.CreateComment(cmd.Context(), page.ID, parent, storage)
			if err != nil {
				return err
			}
			line := fmt.Sprintf("commented on %q (comment id %s)\n%s", page.Title, cm.ID, c.PageURL(page))
			v := viewOf(c, page)
			return a.report(writeReport{Action: "commented", Page: &v, Details: map[string]any{"commentId": cm.ID}}, line)
		},
	}
	cf.register(cmd)
	cmd.Flags().StringVar(&parent, "parent", "", "comment ID to reply to")
	cmd.Flags().BoolVar(&dry, "dry-run", false, "validate without posting")
	return cmd
}

func (a *app) attachmentUploadCmd() *cobra.Command {
	var files []string
	var o confluence.Upload
	var dry bool
	cmd := &cobra.Command{
		Use:   "attachment-upload <page> -f <file> [-f <file>...]",
		Short: "Attach files to a page",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(files) == 0 {
				return invalid("give at least one --file")
			}
			sizes := map[string]any{}
			for _, f := range files { // check every file before uploading any
				info, err := os.Stat(f)
				if err != nil || info.IsDir() {
					return invalid("cannot upload %s: not a readable file", f)
				}
				sizes[f] = info.Size()
			}
			c, err := a.connect(cmd)
			if err != nil {
				return err
			}
			page, err := a.getPage(cmd.Context(), c, args[0], "space")
			if err != nil {
				return err
			}
			if dry {
				return a.dryRun(c, "upload", page, map[string]any{"files": sizes, "replace": o.Replace},
					fmt.Sprintf("would attach %s to %q", strings.Join(files, ", "), page.Title))
			}
			var ids []string
			for _, f := range files {
				att, err := c.UploadAttachment(cmd.Context(), page.ID, f, o)
				if err != nil {
					return fmt.Errorf("upload %s: %w", f, err)
				}
				ids = append(ids, att.ID)
			}
			v := viewOf(c, page)
			line := fmt.Sprintf("attached %s to %q\n%s", strings.Join(files, ", "), page.Title, v.URL)
			return a.report(writeReport{Action: "uploaded", Page: &v, Details: map[string]any{"attachmentIds": ids}}, line)
		},
	}
	cmd.Flags().StringArrayVarP(&files, "file", "f", nil, "file to upload (repeatable)")
	cmd.Flags().StringVar(&o.Comment, "comment", "", "comment stored with the attachment")
	cmd.Flags().BoolVar(&o.Replace, "replace", false, "update an attachment with the same file name")
	cmd.Flags().BoolVar(&o.MinorEdit, "minor-edit", false, "mark as a minor edit (no notifications)")
	cmd.Flags().BoolVar(&dry, "dry-run", false, "check the files without uploading")
	return cmd
}

func (a *app) propertySetCmd() *cobra.Command {
	var value, file string
	var dry bool
	cmd := &cobra.Command{
		Use:   "property-set <page> <key>",
		Short: "Create or update a content property (JSON)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if (value == "") == (file == "") {
				return invalid("give the JSON value with exactly one of --value or --file")
			}
			raw := value
			if file != "" {
				var err error
				if raw, err = readInput(a.stdin, file); err != nil {
					return err
				}
			}
			if !json.Valid([]byte(raw)) {
				return invalid("the property value must be valid JSON, e.g. '\"text\"' or '{\"a\":1}'")
			}
			c, err := a.connect(cmd)
			if err != nil {
				return err
			}
			page, err := a.getPage(cmd.Context(), c, args[0], "space")
			if err != nil {
				return err
			}
			if dry {
				return a.dryRun(c, "property-set", page, map[string]any{"key": args[1], "value": json.RawMessage(raw)},
					fmt.Sprintf("would set property %q on %q", args[1], page.Title))
			}
			p, err := c.SetProperty(cmd.Context(), page.ID, args[1], json.RawMessage(raw))
			if err != nil {
				return err
			}
			v := viewOf(c, page)
			line := fmt.Sprintf("set property %q on %q (property version %d)", p.Key, page.Title, versionOf(p.Version))
			return a.report(writeReport{Action: "property-set", Page: &v, Details: map[string]any{"key": p.Key}}, line)
		},
	}
	cmd.Flags().StringVarP(&value, "value", "v", "", "JSON value")
	cmd.Flags().StringVar(&file, "file", "", "read the JSON value from a file, or - for stdin")
	cmd.Flags().BoolVar(&dry, "dry-run", false, "validate without writing")
	return cmd
}

func versionOf(v *confluence.Version) int {
	if v == nil {
		return 0
	}
	return v.Number
}

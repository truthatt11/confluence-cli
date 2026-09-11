package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/truthatt11/confluence-cli/internal/confluence"
	"github.com/truthatt11/confluence-cli/internal/convert"
)

// contentFlags are the --file/--content/--format flags shared by writes.
type contentFlags struct{ file, content, format string }

func (f *contentFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&f.file, "file", "f", "", "read content from a file, or - for stdin")
	cmd.Flags().StringVarP(&f.content, "content", "c", "", "content as a string")
	cmd.Flags().StringVar(&f.format, "format", "storage", "content format: storage or markdown")
}

func (f contentFlags) given() bool { return f.file != "" || f.content != "" }

// storage returns the content as validated storage format.
func (f contentFlags) storage(stdin io.Reader) (string, error) {
	if f.format != "storage" && f.format != "markdown" {
		return "", invalid("--format must be storage or markdown")
	}
	if f.file != "" && f.content != "" || !f.given() {
		return "", invalid("give the content with exactly one of --file or --content")
	}
	text := f.content
	if f.file != "" {
		var err error
		if text, err = readInput(stdin, f.file); err != nil {
			return "", err
		}
	}
	if f.format == "markdown" {
		var err error
		if text, err = convert.MarkdownToStorage(text); err != nil {
			return "", err
		}
	}
	return text, validStorage(text)
}

func validStorage(s string) error {
	if err := convert.ValidateStorage(s); err != nil {
		return invalid("%v (Confluence would reject it)", err)
	}
	return nil
}

// ensureLossless refuses a Markdown update when the current page holds
// features the Markdown round trip would silently delete.
func ensureLossless(current confluence.Content) error {
	res, err := convert.StorageToMarkdown(current.Storage(), convert.StorageOptions{})
	if err != nil {
		return &cliError{code: "lossy_update", msg: fmt.Sprintf("cannot check page %s for content Markdown would lose: %v; update it in storage format", current.ID, err)}
	}
	if len(res.Lossy) == 0 {
		return nil
	}
	id := current.ID
	return &cliError{code: "lossy_update", msg: fmt.Sprintf(
		"page %s contains %s, which Markdown cannot reproduce, so updating it from Markdown would delete them. "+
			"Edit the storage format instead (cfl edit %s -o page.xml, change it, then cfl update %s -f page.xml), "+
			"or pass --allow-lossy if losing them is acceptable",
		id, strings.Join(res.Lossy, ", "), id, id)}
}

// writeReport is the JSON shape every write command prints.
type writeReport struct {
	Action  string         `json:"action"`
	DryRun  bool           `json:"dryRun"`
	Page    *pageView      `json:"page,omitempty"`
	Details map[string]any `json:"details,omitempty"`
}

func (a *app) report(r writeReport, line string) error {
	return a.emit(r, func(w io.Writer) { fmt.Fprintln(w, line) })
}

// done reports a completed page write with the details needed to check or undo it.
func (a *app) done(c *confluence.Client, action string, p confluence.Content) error {
	v := viewOf(c, p)
	line := fmt.Sprintf("%s %q (id %s, version %d)\n%s", action, v.Title, v.ID, v.Version, v.URL)
	return a.report(writeReport{Action: action, Page: &v}, line)
}

func (a *app) dryRun(c *confluence.Client, action string, p confluence.Content, details map[string]any, line string) error {
	v := viewOf(c, p)
	return a.report(writeReport{Action: action, DryRun: true, Page: &v, Details: details}, "dry run: "+line+"; nothing was sent")
}

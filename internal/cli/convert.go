package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/truthatt11/confluence-cli/internal/convert"
)

func (a *app) convertCmd() *cobra.Command {
	var in, out, from, to string
	cmd := &cobra.Command{
		Use:   "convert",
		Short: "Convert between Markdown and storage format locally",
		Long: "Convert between Markdown and Confluence storage format without contacting Confluence.\n" +
			"Reads stdin unless -i is given and writes stdout unless -o is given.",
		Args:        cobra.NoArgs,
		Annotations: readOnly,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !validFormat(from) || !validFormat(to) || from == to {
				return invalid("--input-format and --output-format must be markdown and storage, one each")
			}
			src, err := readInput(a.stdin, in)
			if err != nil {
				return err
			}
			result, err := convertText(src, from)
			if err != nil {
				return err
			}
			if out == "" {
				_, err = fmt.Fprintln(a.stdout, result)
				return err
			}
			return os.WriteFile(out, []byte(result+"\n"), 0o644)
		},
	}
	cmd.Flags().StringVarP(&in, "input-file", "i", "", "input file (default stdin)")
	cmd.Flags().StringVarP(&out, "output-file", "o", "", "output file (default stdout)")
	cmd.Flags().StringVar(&from, "input-format", "", "markdown or storage")
	cmd.Flags().StringVar(&to, "output-format", "", "markdown or storage")
	return cmd
}

func validFormat(f string) bool { return f == "markdown" || f == "storage" }

func convertText(src, from string) (string, error) {
	if from == "markdown" {
		return convert.MarkdownToStorage(src)
	}
	res, err := convert.StorageToMarkdown(src, convert.StorageOptions{})
	return res.Markdown, err
}

// readInput reads a file, or r when path is "" or "-".
func readInput(r io.Reader, path string) (string, error) {
	var b []byte
	var err error
	if path == "" || path == "-" {
		b, err = io.ReadAll(r)
	} else {
		b, err = os.ReadFile(path)
	}
	if err != nil {
		return "", fmt.Errorf("read input: %w", err)
	}
	return string(b), nil
}

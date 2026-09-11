package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

func (a *app) versionsCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "versions <page>",
		Short:       "List a page's versions",
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
			vs, err := c.Versions(cmd.Context(), id)
			if err != nil {
				return err
			}
			type view struct {
				Number    int    `json:"number"`
				When      string `json:"when"`
				By        string `json:"by"`
				MinorEdit bool   `json:"minorEdit"`
				Message   string `json:"message"`
			}
			out := make([]view, len(vs))
			for i, v := range vs {
				out[i] = view{v.Number, v.When, userName(v.By), v.MinorEdit, v.Message}
			}
			return a.emit(out, func(w io.Writer) {
				tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
				for _, v := range out {
					fmt.Fprintf(tw, "%d\t%s\t%s\t%s\n", v.Number, v.When, v.By, v.Message)
				}
				_ = tw.Flush()
			})
		},
	}
}

func (a *app) propertyListCmd() *cobra.Command {
	var limit, start int
	var all bool
	cmd := &cobra.Command{
		Use:         "property-list <page>",
		Short:       "List a page's content properties",
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
			max := limit
			if all {
				max = 0
			}
			props, err := c.Properties(cmd.Context(), id, max, start)
			if err != nil {
				return err
			}
			return a.emit(props, func(w io.Writer) {
				for _, p := range props {
					fmt.Fprintf(w, "%s\t%s\n", p.Key, compactJSON(p.Value))
				}
			})
		},
	}
	cmd.Flags().IntVarP(&limit, "limit", "l", 25, "maximum properties")
	cmd.Flags().IntVar(&start, "start", 0, "index of the first property")
	cmd.Flags().BoolVar(&all, "all", false, "list every property")
	return cmd
}

func (a *app) propertyGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:         "property-get <page> <key>",
		Short:       "Print one content property's value",
		Args:        cobra.ExactArgs(2),
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
			p, err := c.GetProperty(cmd.Context(), id, args[1])
			if err != nil {
				return err
			}
			return a.emit(p, func(w io.Writer) { fmt.Fprintln(w, prettyJSON(p.Value)) })
		},
	}
}

func compactJSON(raw []byte) string {
	var b bytes.Buffer
	if json.Compact(&b, raw) != nil {
		return string(raw)
	}
	return b.String()
}

func prettyJSON(raw []byte) string {
	var b bytes.Buffer
	if json.Indent(&b, raw, "", "  ") != nil {
		return string(raw)
	}
	return b.String()
}

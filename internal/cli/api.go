package cli

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/spf13/cobra"
)

func (a *app) apiCmd() *cobra.Command {
	var include bool
	cmd := &cobra.Command{
		Use:   "api <path>",
		Short: "Send a raw GET request to the REST API",
		Long: "Send a GET request and print the response, for endpoints cfl has no command for.\n" +
			"<path> is relative to the REST API (\"space/OPS\"), a site path (\"/rest/api/space/OPS\"),\n" +
			"or a full URL on the configured site. Only GET is supported, so this cannot change anything.",
		Args:        cobra.ExactArgs(1),
		Annotations: readOnly,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.connect(cmd)
			if err != nil {
				return err
			}
			status, header, body, err := c.Get(cmd.Context(), args[0])
			if include && status != 0 {
				fmt.Fprintf(a.stdout, "HTTP %d\n", status)
				keys := make([]string, 0, len(header))
				for k := range header {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					fmt.Fprintf(a.stdout, "%s: %s\n", k, header.Get(k))
				}
				fmt.Fprintln(a.stdout)
			}
			if len(body) > 0 {
				if json.Valid(body) {
					fmt.Fprintln(a.stdout, prettyJSON(body))
				} else {
					fmt.Fprintln(a.stdout, string(body))
				}
			}
			return err
		},
	}
	cmd.Flags().BoolVarP(&include, "include", "i", false, "print the status line and headers")
	return cmd
}

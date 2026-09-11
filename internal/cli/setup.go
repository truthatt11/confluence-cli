package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/truthatt11/confluence-cli/internal/config"
	"github.com/truthatt11/confluence-cli/internal/confluence"
)

// setupFlags describe a connection. There is deliberately no --token flag:
// a token typed on the command line ends up in shell history.
type setupFlags struct {
	domain, protocol, apiPath, authType, email string
	readOnly                                   bool
}

func (f *setupFlags) register(cmd *cobra.Command) {
	cmd.Flags().StringVarP(&f.domain, "domain", "d", "", "site, e.g. wiki.example.com or https://intranet.example.com/confluence")
	cmd.Flags().StringVar(&f.protocol, "protocol", "", "https (default) or http")
	cmd.Flags().StringVarP(&f.apiPath, "api-path", "p", "", "REST API path (default /rest/api, or <context>/rest/api)")
	cmd.Flags().StringVarP(&f.authType, "auth-type", "a", "", "bearer (personal access token, default) or basic")
	cmd.Flags().StringVarP(&f.email, "email", "e", "", "username for basic auth")
	cmd.Flags().BoolVar(&f.readOnly, "read-only", false, "block every command that changes Confluence")
}

const setupHelp = "The token is read from CONFLUENCE_API_TOKEN or, in a terminal, typed without echo.\n" +
	"Create a personal access token in Confluence under your avatar → Settings → Personal Access Tokens."

func (a *app) initCmd() *cobra.Command {
	var f setupFlags
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Configure and verify the connection to Confluence",
		Long:  "Save a connection profile (\"default\", or the one named with --profile) after checking it works.\n" + setupHelp,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			name := a.profile
			if name == "" {
				name = config.DefaultProfile
			}
			return a.setupProfile(cmd.Context(), name, f)
		},
	}
	f.register(cmd)
	return cmd
}

func (a *app) setupProfile(ctx context.Context, name string, f setupFlags) error {
	p, err := a.collectProfile(f)
	if err != nil {
		return err
	}
	p = p.Normalized()
	if err := p.Validate(); err != nil {
		return invalid("%v", err)
	}
	user, err := a.verify(ctx, p)
	if err != nil {
		return err
	}
	dir, err := config.Dir(a.env)
	if err != nil {
		return err
	}
	file, err := config.Load(dir)
	if err != nil {
		return err
	}
	next := file.WithProfile(name, p)
	if _, ok := file.Profiles[file.ActiveProfile]; !ok {
		next = next.WithActive(name)
	}
	if err := config.Save(dir, next); err != nil {
		return err
	}
	fmt.Fprintf(a.stdout, "Saved profile %q to %s\nConnected to %s as %s\n", name, config.Path(dir), p.Domain, userName(&user))
	return nil
}

// collectProfile takes values from flags, then the environment, then (in a
// terminal) prompts for whatever is still missing.
func (a *app) collectProfile(f setupFlags) (config.Profile, error) {
	p := config.Profile{
		Domain:   firstOf(f.domain, a.env("CONFLUENCE_DOMAIN")),
		Protocol: firstOf(f.protocol, a.env("CONFLUENCE_PROTOCOL")),
		APIPath:  firstOf(f.apiPath, a.env("CONFLUENCE_API_PATH")),
		AuthType: firstOf(f.authType, a.env("CONFLUENCE_AUTH_TYPE")),
		Email:    firstOf(f.email, a.env("CONFLUENCE_EMAIL")),
		Token:    a.env("CONFLUENCE_API_TOKEN"),
		ReadOnly: f.readOnly,
	}
	if a.isTTY {
		return a.prompt(p)
	}
	if p.Domain == "" {
		return p, invalid("give the site with --domain (or CONFLUENCE_DOMAIN)")
	}
	if p.Token == "" {
		return p, invalid("no token: set CONFLUENCE_API_TOKEN, or run `cfl init` in a terminal to type it without echo")
	}
	return p, nil
}

func (a *app) prompt(p config.Profile) (config.Profile, error) {
	in := bufio.NewReader(a.stdin)
	ask := func(q string) string {
		fmt.Fprint(a.stderr, q)
		line, _ := in.ReadString('\n')
		return strings.TrimSpace(line)
	}
	if p.Domain == "" {
		p.Domain = ask("Confluence site (e.g. https://wiki.example.com/confluence): ")
	}
	if p.AuthType == "" && p.Email == "" {
		if ask("Sign in with [1] personal access token (recommended) or [2] username and password? [1]: ") == "2" {
			p.AuthType = "basic"
		}
	}
	if p.AuthType == "basic" && p.Email == "" {
		p.Email = ask("Username: ")
	}
	if p.Token == "" {
		label := "Personal access token"
		if p.AuthType == "basic" {
			label = "Password"
		}
		fmt.Fprintf(a.stderr, "%s (input hidden): ", label)
		secret, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(a.stderr)
		if err != nil {
			return p, fmt.Errorf("read token: %w", err)
		}
		p.Token = strings.TrimSpace(string(secret))
	}
	return p, nil
}

// verify logs in once. Data Center may answer bad credentials with an
// anonymous user instead of 401, so the user type is checked too.
func (a *app) verify(ctx context.Context, p config.Profile) (confluence.User, error) {
	if p.Protocol == "http" {
		a.warnf("warning: %s uses http, so the token is sent unencrypted", p.Domain)
	}
	c, err := confluence.New(clientOptions(p, a.version))
	if err != nil {
		return confluence.User{}, err
	}
	u, err := c.CurrentUser(ctx)
	if err != nil {
		return u, fmt.Errorf("could not sign in to %s: %w", p.Domain, err)
	}
	if u.Type == "anonymous" || (u.Username == "" && u.UserKey == "") {
		return u, &cliError{code: confluence.CodeAuthFailed, msg: "Confluence treated the request as anonymous, so the token was not accepted"}
	}
	return u, nil
}

func firstOf(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func (a *app) profileCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "profile", Short: "Manage connection profiles"}
	cmd.AddCommand(a.profileListCmd(), a.profileUseCmd(), a.profileAddCmd(), a.profileRemoveCmd())
	return cmd
}

type profileView struct {
	Name     string `json:"name"`
	Active   bool   `json:"active"`
	Domain   string `json:"domain"`
	APIPath  string `json:"apiPath"`
	AuthType string `json:"authType"`
	Username string `json:"username,omitempty"`
	ReadOnly bool   `json:"readOnly"`
	TokenSet bool   `json:"tokenSet"`
}

func (a *app) profileListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List profiles (tokens are never shown)",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			dir, file, err := a.loadConfig()
			if err != nil {
				return err
			}
			envSet := a.env("CONFLUENCE_DOMAIN") != "" && a.env("CONFLUENCE_API_TOKEN") != ""
			if len(file.Profiles) == 0 && !envSet {
				return &cliError{code: "not_configured", msg: "no configuration found in " + config.Path(dir) + ": run `cfl init`"}
			}
			views := []profileView{}
			for _, name := range file.Names() {
				p := file.Profiles[name].Normalized()
				views = append(views, profileView{name, name == file.ActiveProfile, p.Domain, p.APIPath, p.AuthType, p.Email, p.ReadOnly, p.Token != ""})
			}
			return a.emit(views, func(w io.Writer) {
				printProfiles(w, views)
				if envSet {
					fmt.Fprintf(w, "CONFLUENCE_DOMAIN=%s is set and is used unless --profile is given\n", a.env("CONFLUENCE_DOMAIN"))
				}
			})
		},
	}
}

func printProfiles(w io.Writer, views []profileView) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, v := range views {
		mark, access, token := " ", "read-write", "token set"
		if v.Active {
			mark = "*"
		}
		if v.ReadOnly {
			access = "read-only"
		}
		if !v.TokenSet {
			token = "no token"
		}
		fmt.Fprintf(tw, "%s %s\t%s%s\t%s\t%s\t%s\n", mark, v.Name, v.Domain, v.APIPath, v.AuthType, access, token)
	}
	_ = tw.Flush()
}

func (a *app) loadConfig() (string, config.File, error) {
	dir, err := config.Dir(a.env)
	if err != nil {
		return "", config.File{}, err
	}
	f, err := config.Load(dir)
	return dir, f, err
}

func (a *app) profileUseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "use <name>",
		Short: "Make a profile the active one",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			dir, file, err := a.loadConfig()
			if err != nil {
				return err
			}
			if _, ok := file.Profiles[args[0]]; !ok {
				return invalid("no profile %q; available: %s", args[0], strings.Join(file.Names(), ", "))
			}
			if err := config.Save(dir, file.WithActive(args[0])); err != nil {
				return err
			}
			fmt.Fprintf(a.stdout, "Active profile: %s\n", args[0])
			return nil
		},
	}
}

func (a *app) profileAddCmd() *cobra.Command {
	var f setupFlags
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add (or replace) a profile after verifying it",
		Long:  "Add a named profile, like `cfl --profile <name> init`.\n" + setupHelp,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return a.setupProfile(cmd.Context(), args[0], f)
		},
	}
	f.register(cmd)
	return cmd
}

func (a *app) profileRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove <name>",
		Short: "Remove a profile from the local configuration",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			dir, file, err := a.loadConfig()
			if err != nil {
				return err
			}
			if _, ok := file.Profiles[args[0]]; !ok {
				return invalid("no profile %q", args[0])
			}
			next := file.WithoutProfile(args[0])
			if err := config.Save(dir, next); err != nil {
				return err
			}
			fmt.Fprintf(a.stdout, "Removed profile %s; active profile: %s\n", args[0], firstOf(next.ActiveProfile, "(none)"))
			return nil
		},
	}
}

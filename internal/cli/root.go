// Package cli implements the cfl commands.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/truthatt11/confluence-cli/internal/config"
	"github.com/truthatt11/confluence-cli/internal/confluence"
)

// Commands that only read are annotated; everything else counts as a write,
// so a command added without the annotation is blocked in read-only mode
// instead of slipping through.
const (
	accessKey  = "access"
	accessRead = "read"
)

var readOnly = map[string]string{accessKey: accessRead}

// app holds what commands share; tests swap the I/O and environment.
type app struct {
	stdout  io.Writer
	stderr  io.Writer
	stdin   io.Reader
	env     config.Env
	version string
	isTTY   bool
	json    bool
	profile string
}

// Execute runs cfl and returns the process exit code.
func Execute(version string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	a := &app{
		stdout: os.Stdout, stderr: os.Stderr, stdin: os.Stdin, env: os.Getenv,
		version: version, isTTY: term.IsTerminal(int(os.Stdin.Fd())),
	}
	return a.run(ctx, os.Args[1:])
}

func (a *app) run(ctx context.Context, args []string) int {
	root := a.rootCommand()
	root.SetArgs(args)
	root.SetOut(a.stdout)
	root.SetErr(a.stderr)
	root.SetIn(a.stdin)
	if err := root.ExecuteContext(ctx); err != nil {
		a.reportError(err)
		return 1
	}
	return 0
}

func (a *app) rootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "cfl",
		Short:         "Command-line client for Confluence Data Center",
		Long:          "cfl reads, searches, exports and edits Confluence Data Center pages from the terminal.\nContent can be read and written as Markdown or Confluence storage format.",
		Version:       a.version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&a.profile, "profile", "", "configuration profile to use")
	root.PersistentFlags().BoolVar(&a.json, "json", false, "print JSON (errors go to stderr as JSON too)")
	root.AddCommand(
		a.readCmd(), a.infoCmd(), a.findCmd(), a.searchCmd(), a.spacesCmd(), a.spaceLookupCmd(),
		a.childrenCmd(), a.attachmentsCmd(), a.attachmentLookupCmd(), a.commentsCmd(), a.commentLookupCmd(),
		a.versionsCmd(), a.propertyListCmd(), a.propertyGetCmd(), a.convertCmd(), a.apiCmd(), a.exportCmd(),
		a.createCmd(), a.createChildCmd(), a.updateCmd(), a.editCmd(), a.moveCmd(),
		a.commentCmd(), a.attachmentUploadCmd(), a.propertySetCmd(),
		a.initCmd(), a.profileCmd(), a.installSkillCmd(),
	)
	return root
}

// connect resolves the configuration, enforces read-only mode and builds a client.
func (a *app) connect(cmd *cobra.Command) (*confluence.Client, error) {
	dir, err := config.Dir(a.env)
	if err != nil {
		return nil, err
	}
	r, err := config.Resolve(dir, a.profile, a.env)
	if err != nil {
		return nil, err
	}
	if err := r.Profile.Validate(); err != nil {
		return nil, err
	}
	if r.Profile.ReadOnly && cmd.Annotations[accessKey] != accessRead {
		return nil, &cliError{code: "read_only", msg: fmt.Sprintf("%q changes Confluence, but this configuration is read-only", cmd.Name())}
	}
	if r.Profile.Protocol == "http" {
		a.warnf("warning: %s uses http, so the token is sent unencrypted", r.Profile.Domain)
	}
	return confluence.New(clientOptions(r.Profile, a.version))
}

func clientOptions(p config.Profile, version string) confluence.Options {
	return confluence.Options{
		Protocol: p.Protocol, Domain: p.Domain, APIPath: p.APIPath, AuthType: p.AuthType,
		Email: p.Email, Token: p.Token, UserAgent: "cfl/" + version,
	}
}

// cliError is a failure detected by cfl itself, with a stable code.
type cliError struct{ code, msg string }

func (e *cliError) Error() string { return e.msg }

func invalid(format string, args ...any) error {
	return &cliError{code: "invalid_input", msg: fmt.Sprintf(format, args...)}
}

func (a *app) reportError(err error) {
	code := "error"
	var apiErr *confluence.Error
	var ce *cliError
	switch {
	case errors.As(err, &apiErr):
		code = apiErr.Code
	case errors.As(err, &ce):
		code = ce.code
	}
	if !a.json {
		fmt.Fprintf(a.stderr, "Error: %s\n", err)
		return
	}
	payload := map[string]any{"error": map[string]string{"code": code, "message": err.Error()}}
	enc := json.NewEncoder(a.stderr)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(payload)
}

// emit prints v as JSON with --json, otherwise calls plain.
func (a *app) emit(v any, plain func(io.Writer)) error {
	if a.json {
		enc := json.NewEncoder(a.stdout)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		return enc.Encode(v)
	}
	plain(a.stdout)
	return nil
}

func (a *app) warnf(format string, args ...any) {
	fmt.Fprintf(a.stderr, format+"\n", args...)
}

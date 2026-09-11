package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"github.com/truthatt11/confluence-cli/internal/confluence"
	"github.com/truthatt11/confluence-cli/internal/convert"
)

// exportMarker marks a directory cfl created; --overwrite deletes nothing without it.
const exportMarker = ".cfl-export.json"

type exportOptions struct {
	format, dest, file, attachmentsDir, pattern string
	excludeAttachments, exclude                 []string
	referencedOnly, skipAttachments             bool
	recursive, dryRun, overwrite                bool
	maxDepth, delayMS                           int
}

type exportedPage struct {
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Path        string   `json:"path"`
	Attachments []string `json:"attachments"`
}

func (a *app) exportCmd() *cobra.Command {
	o := exportOptions{}
	cmd := &cobra.Command{
		Use:   "export <page>",
		Short: "Export pages to files, Markdown by default",
		Long: "Export a page (and with -r its descendants) into one folder per page, holding page.md\n" +
			"with YAML front matter (id, title, space, version, url, updated) and an attachments folder.\n" + pageArgHelp,
		Args:        cobra.ExactArgs(1),
		Annotations: readOnly,
		RunE: func(cmd *cobra.Command, args []string) error {
			if o.format != "markdown" && o.format != "storage" && o.format != "html" {
				return invalid("--format must be markdown, storage or html")
			}
			c, err := a.connect(cmd)
			if err != nil {
				return err
			}
			id, err := c.ResolvePageID(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			e := &exporter{app: a, c: c, o: o, names: map[string]map[string]int{}}
			return e.run(cmd.Context(), id)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.format, "format", "markdown", "markdown, storage or html")
	f.StringVar(&o.dest, "dest", ".", "directory to export into")
	f.StringVar(&o.file, "file", "", "content file name (default page.md, page.xml or page.html)")
	f.StringVar(&o.attachmentsDir, "attachments-dir", "attachments", "attachment folder name inside each page folder")
	f.StringVar(&o.pattern, "pattern", "", "only attachments matching this glob")
	f.StringSliceVar(&o.excludeAttachments, "exclude-attachments", nil, "attachment globs to skip")
	f.BoolVar(&o.referencedOnly, "referenced-only", false, "only attachments the page content refers to")
	f.BoolVar(&o.skipAttachments, "skip-attachments", false, "do not download attachments")
	f.BoolVarP(&o.recursive, "recursive", "r", false, "export descendants too")
	f.IntVar(&o.maxDepth, "max-depth", defaultMaxDepth, "how deep -r goes")
	f.StringSliceVar(&o.exclude, "exclude", nil, "title globs (case-insensitive) whose pages and subtrees are skipped")
	f.IntVar(&o.delayMS, "delay-ms", 100, "pause between pages, to spare the server")
	f.BoolVar(&o.dryRun, "dry-run", false, "show what would be exported without writing")
	f.BoolVar(&o.overwrite, "overwrite", false, "replace a previous export of the same page")
	return cmd
}

type exporter struct {
	app   *app
	c     *confluence.Client
	o     exportOptions
	names map[string]map[string]int // parent dir → used folder names
	done  []exportedPage
}

func (e *exporter) run(ctx context.Context, rootID string) error {
	root, err := e.fetch(ctx, rootID)
	if err != nil {
		return err
	}
	rootDir := filepath.Join(e.o.dest, safeName(root.Title, root.ID))
	if err := e.prepare(rootDir); err != nil {
		return err
	}
	dirs := map[string]string{root.ID: rootDir}
	if err := e.exportPage(ctx, root, rootDir); err != nil {
		return err
	}
	if e.o.recursive {
		err = walkTree(ctx, e.c, root.ID, e.o.maxDepth, func(p confluence.Content, _ int, parent string) (bool, error) {
			if len(e.o.exclude) > 0 && matchAny(strings.ToLower(p.Title), lower(e.o.exclude)) {
				return false, nil
			}
			if err := e.pause(ctx); err != nil {
				return false, err
			}
			full, err := e.fetch(ctx, p.ID)
			if err != nil {
				return false, err
			}
			dirs[p.ID] = e.childDir(dirs[parent], full)
			return true, e.exportPage(ctx, full, dirs[p.ID])
		})
		if err != nil {
			return err
		}
	}
	if !e.o.dryRun {
		if err := writeMarker(rootDir, root.ID); err != nil {
			return err
		}
	}
	return e.app.emit(e.done, func(w io.Writer) { printExport(w, e.done, e.o.dryRun) })
}

// prepare checks the root folder is free, or replaces a previous cfl export.
func (e *exporter) prepare(dir string) error {
	_, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) || e.o.dryRun {
		return nil
	}
	if err != nil {
		return err
	}
	if !e.o.overwrite {
		return invalid("%s already exists; pass --overwrite to replace a previous export", dir)
	}
	if _, err := os.Stat(filepath.Join(dir, exportMarker)); err != nil {
		return invalid("refusing to overwrite %s: it was not created by cfl export", dir)
	}
	return os.RemoveAll(dir)
}

func (e *exporter) fetch(ctx context.Context, id string) (confluence.Content, error) {
	expand := []string{"body.storage", "space", "version"}
	if e.o.format == "html" {
		expand = append(expand, "body.view")
	}
	return e.c.GetContent(ctx, id, expand...)
}

func (e *exporter) childDir(parentDir string, p confluence.Content) string {
	name := safeName(p.Title, p.ID)
	used := e.names[parentDir]
	if used == nil {
		used = map[string]int{}
		e.names[parentDir] = used
	}
	used[name]++
	if n := used[name]; n > 1 {
		name = fmt.Sprintf("%s-%d", name, n)
	}
	return filepath.Join(parentDir, name)
}

func (e *exporter) exportPage(ctx context.Context, p confluence.Content, dir string) error {
	e.app.warnf("exporting %s (%s)", p.Title, p.ID)
	content, err := e.render(ctx, p)
	if err != nil {
		return err
	}
	atts, err := e.attachments(ctx, p)
	if err != nil {
		return err
	}
	file := filepath.Join(dir, e.fileName())
	names := make([]string, 0, len(atts))
	for _, att := range atts {
		names = append(names, att.Title)
	}
	e.done = append(e.done, exportedPage{ID: p.ID, Title: p.Title, Path: file, Attachments: names})
	if e.o.dryRun {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		return err
	}
	for _, att := range atts {
		if _, err := saveAttachment(ctx, e.c, att, filepath.Join(dir, e.o.attachmentsDir)); err != nil {
			return fmt.Errorf("page %s attachment %s: %w", p.ID, att.Title, err)
		}
	}
	return nil
}

func (e *exporter) fileName() string {
	if e.o.file != "" {
		return filepath.Base(e.o.file)
	}
	return map[string]string{"markdown": "page.md", "storage": "page.xml", "html": "page.html"}[e.o.format]
}

func (e *exporter) render(ctx context.Context, p confluence.Content) (string, error) {
	switch e.o.format {
	case "storage":
		return p.Storage(), nil
	case "html":
		if p.Body != nil && p.Body.View != nil {
			return p.Body.View.Value, nil
		}
		return "", nil
	}
	res, err := e.app.toMarkdown(ctx, e.c, p, e.o.attachmentsDir)
	if err != nil {
		return "", err
	}
	return frontMatter(e.c, p) + res.Markdown + "\n", nil
}

func (e *exporter) attachments(ctx context.Context, p confluence.Content) ([]confluence.Content, error) {
	if e.o.skipAttachments {
		return nil, nil
	}
	all, err := e.c.Attachments(ctx, p.ID, 0)
	if err != nil {
		return nil, err
	}
	var referenced map[string]bool
	if e.o.referencedOnly {
		refs, _ := convert.ReferencedAttachments(p.Storage())
		referenced = map[string]bool{}
		for _, r := range refs {
			referenced[r] = true
		}
	}
	var out []confluence.Content
	for _, att := range all {
		switch {
		case referenced != nil && !referenced[att.Title]:
		case e.o.pattern != "" && !matchAny(att.Title, []string{e.o.pattern}):
		case len(e.o.excludeAttachments) > 0 && matchAny(att.Title, e.o.excludeAttachments):
		default:
			out = append(out, att)
		}
	}
	return out, nil
}

func (e *exporter) pause(ctx context.Context) error {
	if e.o.delayMS <= 0 {
		return nil
	}
	t := time.NewTimer(time.Duration(e.o.delayMS) * time.Millisecond)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// frontMatter uses JSON-quoted strings, which are valid YAML scalars.
func frontMatter(c *confluence.Client, p confluence.Content) string {
	q := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	updated := ""
	if p.Version != nil {
		updated = p.Version.When
	}
	return fmt.Sprintf("---\nid: %s\ntitle: %s\nspace: %s\nversion: %d\nurl: %s\nupdated: %s\n---\n\n",
		q(p.ID), q(p.Title), q(p.SpaceKey()), p.VersionNumber(), q(c.PageURL(p)), q(updated))
}

func writeMarker(dir, rootID string) error {
	b, _ := json.Marshal(map[string]string{"tool": "cfl", "rootId": rootID})
	return os.WriteFile(filepath.Join(dir, exportMarker), b, 0o644)
}

func printExport(w io.Writer, pages []exportedPage, dryRun bool) {
	verb := "exported"
	if dryRun {
		verb = "would export"
	}
	for _, p := range pages {
		fmt.Fprintf(w, "%s %s  (%s)", verb, p.Path, p.Title)
		if len(p.Attachments) > 0 {
			fmt.Fprintf(w, "  + %s", strings.Join(p.Attachments, ", "))
		}
		fmt.Fprintln(w)
	}
}

func lower(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = strings.ToLower(s)
	}
	return out
}

const maxNameBytes = 100

// safeName turns a page title into a folder name that is valid on every OS
// and cannot climb out of the export directory.
func safeName(title, id string) string {
	name := strings.Map(func(r rune) rune {
		if r < 0x20 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, title)
	name = strings.Trim(name, " .")
	for len(name) > maxNameBytes {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	name = strings.TrimRight(name, " .")
	if name == "" {
		return "page-" + id
	}
	return name
}

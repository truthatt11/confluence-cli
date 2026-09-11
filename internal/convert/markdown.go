package convert

import (
	"bytes"
	"fmt"
	"html"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	gmhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/util"
)

// Priorities below the default HTML renderer's 1000 register later and win.
var markdownEngine = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithParserOptions(parser.WithASTTransformers(util.Prioritized(transformer{}, 100))),
	goldmark.WithRendererOptions(
		gmhtml.WithXHTML(),
		renderer.WithNodeRenderers(util.Prioritized(storageRenderer{}, 100)),
	),
)

// MarkdownToStorage converts Markdown to Confluence storage format.
func MarkdownToStorage(markdown string) (string, error) {
	var b bytes.Buffer
	if err := markdownEngine.Convert([]byte(markdown), &b); err != nil {
		return "", fmt.Errorf("convert markdown: %w", err)
	}
	return strings.TrimSpace(b.String()), nil
}

// storageRenderer renders the nodes whose storage form differs from HTML.
type storageRenderer struct{}

func (r storageRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	reg.Register(kindMacro, r.macro)
	reg.Register(kindTaskList, r.taskList)
	reg.Register(kindTask, r.task)
	reg.Register(ast.KindFencedCodeBlock, r.code)
	reg.Register(ast.KindCodeBlock, r.code)
	reg.Register(ast.KindImage, r.image)
	reg.Register(ast.KindLink, r.link)
	reg.Register(ast.KindRawHTML, r.rawHTML)
	reg.Register(ast.KindHTMLBlock, r.htmlBlock)
	reg.Register(ast.KindText, r.text)
	reg.Register(extast.KindTaskCheckBox, r.checkBox)
}

func (storageRenderer) macro(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	m := n.(*macroNode)
	if !entering {
		if m.body {
			_, _ = w.WriteString("</ac:rich-text-body>")
		}
		_, _ = w.WriteString("</ac:structured-macro>")
		return ast.WalkContinue, nil
	}
	_, _ = w.WriteString(`<ac:structured-macro ac:name="` + html.EscapeString(m.name) + `">`)
	for _, p := range m.params {
		_, _ = w.WriteString(`<ac:parameter ac:name="` + html.EscapeString(p[0]) + `">` + html.EscapeString(p[1]) + `</ac:parameter>`)
	}
	if m.body {
		_, _ = w.WriteString("<ac:rich-text-body>")
	}
	return ast.WalkContinue, nil
}

func (storageRenderer) taskList(w util.BufWriter, _ []byte, _ ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		_, _ = w.WriteString("<ac:task-list>")
	} else {
		_, _ = w.WriteString("</ac:task-list>")
	}
	return ast.WalkContinue, nil
}

func (storageRenderer) task(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		_, _ = w.WriteString("</ac:task-body></ac:task>")
		return ast.WalkContinue, nil
	}
	status := "incomplete"
	if n.(*taskNode).done {
		status = "complete"
	}
	_, _ = w.WriteString("<ac:task><ac:task-status>" + status + "</ac:task-status><ac:task-body>")
	return ast.WalkContinue, nil
}

func (storageRenderer) code(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	lang := ""
	if f, ok := n.(*ast.FencedCodeBlock); ok {
		lang = string(f.Language(src))
	}
	var body strings.Builder
	for i := 0; i < n.Lines().Len(); i++ {
		seg := n.Lines().At(i)
		body.Write(seg.Value(src))
	}
	plain := "<ac:plain-text-body>" + cdata(strings.TrimSuffix(body.String(), "\n")) + "</ac:plain-text-body>"
	switch lang {
	case "plantuml":
		_, _ = w.WriteString(`<ac:structured-macro ac:name="plantuml">` + plain + "</ac:structured-macro>")
	case "":
		_, _ = w.WriteString(`<ac:structured-macro ac:name="code">` + plain + "</ac:structured-macro>")
	default:
		_, _ = w.WriteString(`<ac:structured-macro ac:name="code"><ac:parameter ac:name="language">` +
			html.EscapeString(lang) + "</ac:parameter>" + plain + "</ac:structured-macro>")
	}
	return ast.WalkSkipChildren, nil
}

func (storageRenderer) image(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	dest := string(n.(*ast.Image).Destination)
	if name, ok := attachmentName(dest); ok {
		_, _ = w.WriteString(`<ac:image><ri:attachment ri:filename="` + html.EscapeString(name) + `" /></ac:image>`)
	} else {
		_, _ = w.WriteString(`<ac:image><ri:url ri:value="` + html.EscapeString(dest) + `" /></ac:image>`)
	}
	return ast.WalkSkipChildren, nil
}

func (storageRenderer) link(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	l := n.(*ast.Link)
	dest := string(l.Destination)
	body := "<ac:plain-text-link-body>" + cdata(plainText(l, src)) + "</ac:plain-text-link-body>"
	if strings.HasPrefix(dest, "#") {
		if entering {
			_, _ = w.WriteString(`<ac:link ac:anchor="` + html.EscapeString(dest[1:]) + `">` + body + "</ac:link>")
		}
		return ast.WalkSkipChildren, nil
	}
	if name, ok := attachmentName(dest); ok {
		if entering {
			_, _ = w.WriteString(`<ac:link><ri:attachment ri:filename="` + html.EscapeString(name) + `" />` + body + "</ac:link>")
		}
		return ast.WalkSkipChildren, nil
	}
	if !entering {
		_, _ = w.WriteString("</a>")
		return ast.WalkContinue, nil
	}
	_, _ = w.WriteString(`<a href="` + html.EscapeString(dest) + `"`)
	if len(l.Title) > 0 {
		_, _ = w.WriteString(` title="` + html.EscapeString(string(l.Title)) + `"`)
	}
	_, _ = w.WriteString(">")
	return ast.WalkContinue, nil
}

// allowedInline is the raw inline HTML Markdown has no syntax for but
// storage format accepts. Anything else is shown as text.
var allowedInline = regexp.MustCompile(`^<(/?)(u|sub|sup|mark|s|del|ins|kbd)>$`)
var lineBreak = regexp.MustCompile(`^<br\s*/?>$`)

func (storageRenderer) rawHTML(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	segs := n.(*ast.RawHTML).Segments
	var raw strings.Builder
	for i := 0; i < segs.Len(); i++ {
		seg := segs.At(i)
		raw.Write(seg.Value(src))
	}
	tag := strings.ToLower(raw.String())
	switch {
	case strings.HasPrefix(tag, "<!--"):
	case lineBreak.MatchString(tag):
		_, _ = w.WriteString("<br />")
	case allowedInline.MatchString(tag):
		_, _ = w.WriteString(tag)
	default:
		_, _ = w.WriteString(html.EscapeString(raw.String()))
	}
	return ast.WalkSkipChildren, nil
}

func (storageRenderer) htmlBlock(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	text := blockText(n.(*ast.HTMLBlock), src)
	if text != "" && !(strings.HasPrefix(text, "<!--") && strings.HasSuffix(text, "-->")) {
		_, _ = w.WriteString("<p>" + html.EscapeString(text) + "</p>\n")
	}
	return ast.WalkSkipChildren, nil
}

// text writes line breaks as <br /> without the trailing newline the default
// renderer adds, which would read back as a paragraph break.
func (storageRenderer) text(w util.BufWriter, src []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	t := n.(*ast.Text)
	if t.IsRaw() {
		gmhtml.DefaultWriter.RawWrite(w, t.Segment.Value(src))
	} else {
		gmhtml.DefaultWriter.Write(w, t.Segment.Value(src))
	}
	if t.SoftLineBreak() || t.HardLineBreak() {
		_, _ = w.WriteString("<br />")
	}
	return ast.WalkContinue, nil
}

// checkBox covers task items in a list that is not entirely tasks.
func (storageRenderer) checkBox(w util.BufWriter, _ []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		box := "[ ] "
		if n.(*extast.TaskCheckBox).IsChecked {
			box = "[x] "
		}
		_, _ = w.WriteString(box)
	}
	return ast.WalkContinue, nil
}

// attachmentName treats a relative reference (no scheme, not site-absolute,
// not a fragment) as an attachment of the page.
func attachmentName(dest string) (string, bool) {
	if dest == "" || strings.HasPrefix(dest, "/") || strings.HasPrefix(dest, "#") || strings.Contains(dest, ":") {
		return "", false
	}
	name := path.Base(dest)
	if unescaped, err := url.PathUnescape(name); err == nil {
		name = unescaped
	}
	return name, true
}

func cdata(s string) string {
	return "<![CDATA[" + strings.ReplaceAll(s, "]]>", "]]]]><![CDATA[>") + "]]>"
}

func plainText(n ast.Node, src []byte) string {
	var b strings.Builder
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if t, ok := c.(*ast.Text); ok && entering {
			b.Write(t.Segment.Value(src))
		}
		return ast.WalkContinue, nil
	})
	return b.String()
}

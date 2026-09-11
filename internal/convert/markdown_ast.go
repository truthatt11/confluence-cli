package convert

import (
	"regexp"
	"strings"

	"github.com/yuin/goldmark/ast"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

var (
	kindMacro    = ast.NewNodeKind("ConfluenceMacro")
	kindTaskList = ast.NewNodeKind("ConfluenceTaskList")
	kindTask     = ast.NewNodeKind("ConfluenceTask")
)

// macroNode is an ac:structured-macro; body macros wrap their children in
// ac:rich-text-body.
type macroNode struct {
	ast.BaseBlock
	name   string
	params [][2]string
	body   bool
}

func (n *macroNode) Kind() ast.NodeKind { return kindMacro }
func (n *macroNode) Dump(src []byte, level int) {
	ast.DumpHelper(n, src, level, map[string]string{"Name": n.name}, nil)
}

type taskListNode struct{ ast.BaseBlock }

func (n *taskListNode) Kind() ast.NodeKind         { return kindTaskList }
func (n *taskListNode) Dump(src []byte, level int) { ast.DumpHelper(n, src, level, nil, nil) }

type taskNode struct {
	ast.BaseBlock
	done bool
}

func (n *taskNode) Kind() ast.NodeKind         { return kindTask }
func (n *taskNode) Dump(src []byte, level int) { ast.DumpHelper(n, src, level, nil, nil) }

var (
	anchorMarker = regexp.MustCompile(`^\*\*ANCHOR: (.+)\*\*$`)
	expandMarker = regexp.MustCompile(`^\*\*EXPAND: (.+)\*\*$`)
	boldCallout  = regexp.MustCompile(`^\*\*(INFO|TIP|NOTE|WARNING)\*\*$`)
	githubAlert  = regexp.MustCompile(`^\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\]$`)
	detailsOpen  = regexp.MustCompile(`(?s)^<details>\s*<summary>(.*?)</summary>$`)
	htmlTag      = regexp.MustCompile(`<[^>]+>`)
	// GitHub alerts mapped to the Confluence callout of the matching colour.
	alertMacro = map[string]string{"NOTE": "info", "TIP": "tip", "IMPORTANT": "info", "WARNING": "note", "CAUTION": "warning"}
)

// transformer rewrites the Markdown conventions StorageToMarkdown emits back
// into Confluence nodes before rendering.
type transformer struct{}

func (transformer) Transform(doc *ast.Document, reader text.Reader, _ parser.Context) {
	transformBlocks(doc, reader.Source())
}

func transformBlocks(parent ast.Node, src []byte) {
	for c := parent.FirstChild(); c != nil; c = c.NextSibling() {
		c = rewrite(parent, c, src)
		transformBlocks(c, src)
	}
}

// rewrite replaces c when it matches a convention and returns the node now in its place.
func rewrite(parent, c ast.Node, src []byte) ast.Node {
	switch n := c.(type) {
	case *ast.Paragraph:
		return rewriteParagraph(parent, n, src)
	case *ast.HTMLBlock:
		return rewriteDetails(parent, n, src)
	case *ast.Blockquote:
		return rewriteCallout(parent, n, src)
	case *ast.List:
		return rewriteTaskList(parent, n)
	}
	return c
}

func rewriteParagraph(parent ast.Node, p *ast.Paragraph, src []byte) ast.Node {
	line, ok := singleLine(p, src)
	if !ok {
		return p
	}
	switch line {
	case "[[_TOC_]]", "**TOC**":
		return replace(parent, p, &macroNode{name: "toc"})
	case "[[_LISTING_]]":
		return replace(parent, p, &macroNode{name: "children"})
	}
	if m := anchorMarker.FindStringSubmatch(line); m != nil {
		return replace(parent, p, &macroNode{name: "anchor", params: [][2]string{{"", m[1]}}})
	}
	if m := expandMarker.FindStringSubmatch(line); m != nil {
		expand := &macroNode{name: "expand", body: true, params: [][2]string{{"title", m[1]}}}
		return wrapUntil(parent, p, expand, func(n ast.Node) bool {
			q, ok := n.(*ast.Paragraph)
			l, single := singleLine(q, src)
			return ok && single && l == "**EXPAND_END**"
		})
	}
	return p
}

func rewriteDetails(parent ast.Node, h *ast.HTMLBlock, src []byte) ast.Node {
	m := detailsOpen.FindStringSubmatch(blockText(h, src))
	if m == nil {
		return h
	}
	expand := &macroNode{name: "expand", body: true}
	// "Expand Details" is the placeholder StorageToMarkdown uses for untitled expands.
	if title := strings.TrimSpace(htmlTag.ReplaceAllString(m[1], "")); title != "" && title != "Expand Details" {
		expand.params = [][2]string{{"title", title}}
	}
	return wrapUntil(parent, h, expand, func(n ast.Node) bool {
		end, ok := n.(*ast.HTMLBlock)
		return ok && blockText(end, src) == "</details>"
	})
}

func rewriteCallout(parent ast.Node, bq *ast.Blockquote, src []byte) ast.Node {
	p, ok := bq.FirstChild().(*ast.Paragraph)
	if !ok || p.Lines().Len() == 0 {
		return bq
	}
	seg := p.Lines().At(0)
	first := strings.TrimSpace(string(seg.Value(src)))
	name := ""
	if m := boldCallout.FindStringSubmatch(first); m != nil {
		name = strings.ToLower(m[1])
	} else if m := githubAlert.FindStringSubmatch(first); m != nil {
		name = alertMacro[m[1]]
	}
	if name == "" {
		return bq
	}
	dropFirstLine(bq, p)
	callout := &macroNode{name: name, body: true}
	moveChildren(bq, callout)
	return replace(parent, bq, callout)
}

// rewriteTaskList turns a list whose every item starts with a checkbox into ac:task-list.
func rewriteTaskList(parent ast.Node, l *ast.List) ast.Node {
	var boxes []*extast.TaskCheckBox
	for item := l.FirstChild(); item != nil; item = item.NextSibling() {
		box := taskBox(item)
		if box == nil {
			return l
		}
		boxes = append(boxes, box)
	}
	tasks := &taskListNode{}
	item := l.FirstChild()
	for _, box := range boxes {
		next := item.NextSibling()
		box.Parent().RemoveChild(box.Parent(), box)
		task := &taskNode{done: box.IsChecked}
		moveChildren(item, task)
		tasks.AppendChild(tasks, task)
		item = next
	}
	return replace(parent, l, tasks)
}

func taskBox(item ast.Node) *extast.TaskCheckBox {
	if block := item.FirstChild(); block != nil {
		if box, ok := block.FirstChild().(*extast.TaskCheckBox); ok {
			return box
		}
	}
	return nil
}

// dropFirstLine removes the inline nodes of p's first line; p goes if nothing is left.
func dropFirstLine(parent ast.Node, p *ast.Paragraph) {
	for c := p.FirstChild(); c != nil; {
		next := c.NextSibling()
		p.RemoveChild(p, c)
		if t, ok := c.(*ast.Text); ok && (t.SoftLineBreak() || t.HardLineBreak()) {
			break
		}
		c = next
	}
	if p.FirstChild() == nil {
		parent.RemoveChild(parent, p)
	}
}

// wrapUntil moves the siblings between start and the first sibling matching
// isEnd into m, replacing both markers. Without an end marker nothing changes.
func wrapUntil(parent, start ast.Node, m *macroNode, isEnd func(ast.Node) bool) ast.Node {
	end := start.NextSibling()
	for end != nil && !isEnd(end) {
		end = end.NextSibling()
	}
	if end == nil {
		return start
	}
	parent.InsertBefore(parent, start, m)
	for c := start.NextSibling(); c != end; {
		next := c.NextSibling()
		m.AppendChild(m, c)
		c = next
	}
	parent.RemoveChild(parent, start)
	parent.RemoveChild(parent, end)
	return m
}

func moveChildren(from, to ast.Node) {
	for c := from.FirstChild(); c != nil; {
		next := c.NextSibling()
		to.AppendChild(to, c)
		c = next
	}
}

func replace(parent, old, repl ast.Node) ast.Node {
	parent.ReplaceChild(parent, old, repl)
	return repl
}

func singleLine(p *ast.Paragraph, src []byte) (string, bool) {
	if p == nil || p.Lines().Len() != 1 {
		return "", false
	}
	seg := p.Lines().At(0)
	return strings.TrimSpace(string(seg.Value(src))), true
}

func blockText(h *ast.HTMLBlock, src []byte) string {
	var b strings.Builder
	for i := 0; i < h.Lines().Len(); i++ {
		seg := h.Lines().At(i)
		b.Write(seg.Value(src))
	}
	if h.HasClosure() {
		b.Write(h.ClosureLine.Value(src))
	}
	return strings.TrimSpace(b.String())
}

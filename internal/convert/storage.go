// Package convert translates between Confluence storage format and Markdown.
// Everything here is pure: lookups that need the API (user names, child
// pages) are resolved by the caller and passed in through options.
package convert

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// StorageOptions carries context the converter cannot discover on its own.
type StorageOptions struct {
	AttachmentsDir string            // relative directory for attachment links; default "attachments"
	Users          map[string]string // userkey → display name, for @mentions
	Children       []Link            // child pages, rendered for the children macro
	WebBase        string            // site URL including context path, for page links
	SpaceKey       string            // space of the converted page, for links without ri:space-key
}

// Link is a titled URL.
type Link struct{ Title, URL string }

// Result is converted Markdown plus the Confluence features that would not
// survive converting that Markdown back to storage format.
type Result struct {
	Markdown string
	Lossy    []string
}

// StorageToMarkdown converts a storage format fragment to Markdown.
func StorageToMarkdown(storage string, o StorageOptions) (Result, error) {
	root, err := parse(storage)
	if err != nil {
		return Result{}, err
	}
	if o.AttachmentsDir == "" {
		o.AttachmentsDir = "attachments"
	}
	w := &walker{opts: o, seen: map[string]bool{}}
	return Result{Markdown: cleanupWithFences(w.nodes(root.children)), Lossy: w.lossy}, nil
}

type walker struct {
	opts      StorageOptions
	seen      map[string]bool
	lossy     []string
	linkDepth int // > 0 while rendering a Markdown link label
	codeDepth int // > 0 while rendering an inline code span
}

func (w *walker) markLossy(reason string) {
	if !w.seen[reason] {
		w.seen[reason] = true
		w.lossy = append(w.lossy, reason)
	}
}

func (w *walker) nodes(ns []*node) string {
	var b strings.Builder
	for _, n := range ns {
		b.WriteString(w.node(n))
	}
	return b.String()
}

func (w *walker) node(n *node) string {
	if n.isText() {
		return w.text(n.text)
	}
	if _, ok := n.attrs["style"]; ok {
		w.markLossy("text styling")
	}
	return w.element(n)
}

// text escapes Markdown syntax only where it would break a link label.
func (w *walker) text(s string) string {
	if w.linkDepth > 0 && w.codeDepth == 0 {
		return escapeMarkdown(s)
	}
	return s
}

func (w *walker) element(n *node) string {
	switch n.name {
	case "p":
		return "\n" + jsTrim(w.nodes(n.children)) + "\n"
	case "h1", "h2", "h3", "h4", "h5", "h6":
		return "\n" + strings.Repeat("#", int(n.name[1]-'0')) + " " + jsTrim(w.nodes(n.children)) + "\n"
	case "strong", "b":
		return "**" + w.nodes(n.children) + "**"
	case "em", "i":
		return "*" + w.nodes(n.children) + "*"
	case "s", "del":
		return "~~" + w.nodes(n.children) + "~~"
	case "code":
		w.codeDepth++
		defer func() { w.codeDepth-- }()
		return codeSpan(w.nodes(n.children))
	case "br":
		return "\n"
	case "hr":
		return "\n---\n"
	case "a":
		return w.anchor(n)
	case "time":
		return w.date(n)
	case "ul", "ol":
		return w.list(n)
	case "table":
		return w.table(n)
	case "blockquote":
		return w.blockquote(n)
	case "details", "summary", "u", "sub", "sup", "mark":
		return "<" + n.name + ">" + w.nodes(n.children) + "</" + n.name + ">"
	}
	return w.confluenceElement(n)
}

func (w *walker) confluenceElement(n *node) string {
	switch n.name {
	case "ac:structured-macro":
		return w.macro(n)
	case "ac:image":
		return w.image(n)
	case "ac:link":
		return w.acLink(n)
	case "ac:task-list":
		return w.taskList(n)
	case "ac:layout":
		w.markLossy("page layout")
	case "ac:inline-comment-marker":
		w.markLossy("inline comment")
	case "ac:emoticon":
		w.markLossy("emoticon")
		return ":" + n.attrs["ac:name"] + ":"
	case "ac:placeholder", "ri:url", "ri:page", "ri:attachment", "ri:user",
		"ac:plain-text-body", "ac:plain-text-link-body", "ac:parameter":
		return ""
	}
	return w.nodes(n.children)
}

func (w *walker) anchor(n *node) string {
	href := n.attrs["href"]
	if href == "" {
		return w.nodes(n.children)
	}
	return "[" + w.label(n.children) + "](" + href + ")"
}

// label renders nodes as a Markdown link label.
func (w *walker) label(ns []*node) string {
	w.linkDepth++
	defer func() { w.linkDepth-- }()
	return w.nodes(ns)
}

func (w *walker) date(n *node) string {
	w.markLossy("date")
	if dt := w.text(n.attrs["datetime"]); dt != "" {
		return dt
	}
	return w.nodes(n.children)
}

// list renders ul/ol, indenting nested lists under their parent item so the
// structure survives. The original converter flattened them onto one line.
func (w *walker) list(n *node) string {
	var lines []string
	num := 1
	for _, li := range childElements(n, "li") {
		text, nested := w.listItem(li)
		if text == "" && nested == "" {
			continue
		}
		marker := "-"
		if n.name == "ol" {
			marker = strconv.Itoa(num) + "."
			num++
		}
		lines = append(lines, strings.TrimRight(marker+" "+text, " "))
		pad := strings.Repeat(" ", len(marker)+1)
		for _, l := range strings.Split(nested, "\n") {
			if l != "" {
				lines = append(lines, pad+l)
			}
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "\n" + strings.Join(lines, "\n") + "\n"
}

// listItem splits an <li> into its inline text and its rendered nested lists.
func (w *walker) listItem(li *node) (string, string) {
	var inline, nested strings.Builder
	for _, c := range li.children {
		if c.name == "ul" || c.name == "ol" {
			nested.WriteString(strings.Trim(w.list(c), "\n") + "\n")
			continue
		}
		inline.WriteString(w.node(c))
	}
	return jsTrim(collapseSpace(inline.String())), strings.TrimRight(nested.String(), "\n")
}

func (w *walker) table(n *node) string {
	var rows []string
	for _, tr := range descendants(n, "tr") {
		cells := childElements(tr, "th", "td")
		if len(cells) == 0 {
			continue
		}
		texts := make([]string, len(cells))
		for i, c := range cells {
			texts[i] = w.cell(c)
		}
		rows = append(rows, "| "+strings.Join(texts, " | ")+" |")
		if len(rows) == 1 {
			rows = append(rows, "| "+strings.Repeat("--- | ", len(cells)-1)+"--- |")
		}
	}
	if len(rows) == 0 {
		return ""
	}
	return "\n" + strings.Join(rows, "\n") + "\n"
}

func (w *walker) cell(c *node) string {
	if c.attrs["colspan"] != "" || c.attrs["rowspan"] != "" {
		w.markLossy("merged table cells")
	}
	for _, block := range []string{"ul", "ol", "table", "ac:structured-macro"} {
		if len(descendants(c, block)) > 0 {
			w.markLossy("complex table cells")
		}
	}
	t := strings.ReplaceAll(jsTrim(collapseSpace(w.nodes(c.children))), "|", `\|`)
	if t == "" {
		return " "
	}
	return t
}

func (w *walker) blockquote(n *node) string {
	inner := normalizeBlock(w.nodes(n.children))
	if inner == "" {
		return ""
	}
	return "\n" + quote(inner) + "\n"
}

func (w *walker) image(n *node) string {
	for _, attr := range []string{"ac:width", "ac:height", "ac:align", "ac:border", "ac:thumbnail"} {
		if n.attrs[attr] != "" {
			w.markLossy("image size or alignment")
		}
	}
	if a := child(n, "ri:attachment"); a != nil {
		f := w.text(a.attrs["ri:filename"])
		return "![" + f + "](" + w.attachmentPath(f) + ")"
	}
	if u := child(n, "ri:url"); u != nil && u.attrs["ri:value"] != "" {
		return "![](" + w.text(u.attrs["ri:value"]) + ")"
	}
	return ""
}

func (w *walker) acLink(n *node) string {
	if anchor := n.attrs["ac:anchor"]; anchor != "" {
		return linkOrEmpty(rawText(child(n, "ac:plain-text-link-body")), "#"+anchor)
	}
	if u := child(n, "ri:url"); u != nil {
		return linkOrEmpty(rawText(child(n, "ac:plain-text-link-body")), u.attrs["ri:value"])
	}
	if u := child(n, "ri:user"); u != nil {
		w.markLossy("user mention")
		return w.mention(u)
	}
	if a := child(n, "ri:attachment"); a != nil {
		w.markLossy("attachment link")
		f := a.attrs["ri:filename"]
		text := rawText(child(n, "ac:plain-text-link-body"))
		if text == "" {
			text = escapeMarkdown(f)
		}
		return "[" + text + "](" + w.attachmentPath(f) + ")"
	}
	return w.pageLink(n)
}

func (w *walker) pageLink(n *node) string {
	page := child(n, "ri:page")
	if page != nil {
		w.markLossy("page link")
	}
	target := w.pageURL(page)
	if body := child(n, "ac:link-body"); body != nil {
		if target == "" {
			return jsTrim(w.nodes(body.children))
		}
		return "[" + jsTrim(w.label(body.children)) + "](" + target + ")"
	}
	if page == nil {
		return ""
	}
	title := escapeMarkdown(page.attrs["ri:content-title"])
	if target == "" {
		return "[" + title + "]"
	}
	return "[" + title + "](" + target + ")"
}

func linkOrEmpty(text, target string) string {
	if text == "" {
		return ""
	}
	return "[" + text + "](" + target + ")"
}

func (w *walker) mention(u *node) string {
	key := u.attrs["ri:userkey"]
	for _, name := range []string{w.opts.Users[key], u.attrs["ri:username"], key} {
		if name != "" {
			return "@" + name
		}
	}
	return ""
}

// pageURL builds a /display/ URL, which Data Center resolves without a page ID.
func (w *walker) pageURL(page *node) string {
	if page == nil {
		return ""
	}
	space := page.attrs["ri:space-key"]
	if space == "" {
		space = w.opts.SpaceKey
	}
	title := page.attrs["ri:content-title"]
	if space == "" || title == "" {
		return ""
	}
	return displayURL(w.opts.WebBase, space, title)
}

func displayURL(base, space, title string) string {
	return base + "/display/" + escapePath(space) + "/" + escapePath(title)
}

// escapePath also escapes '+', which Confluence reads as a space in /display/ URLs.
func escapePath(s string) string {
	return strings.ReplaceAll(url.PathEscape(s), "+", "%2B")
}

// attachmentPath wraps destinations containing spaces or parentheses in <>,
// which CommonMark requires for them to stay a single link destination.
func (w *walker) attachmentPath(filename string) string {
	p := w.opts.AttachmentsDir + "/" + filename
	if strings.ContainsAny(p, " ()<>") {
		return "<" + p + ">"
	}
	return p
}

func (w *walker) taskList(n *node) string {
	var lines []string
	for _, t := range childElements(n, "ac:task") {
		text := ""
		if body := child(t, "ac:task-body"); body != nil {
			text = jsTrim(collapseSpace(w.nodes(body.children)))
		}
		if text == "" {
			continue
		}
		box := "[ ]"
		if textContent(child(t, "ac:task-status")) == "complete" {
			box = "[x]"
		}
		lines = append(lines, "- "+box+" "+text)
	}
	if len(lines) == 0 {
		return ""
	}
	return "\n" + strings.Join(lines, "\n") + "\n"
}

var markdownSpecial = regexp.MustCompile("([\\\\`*_\\[\\]()~|<>])")

// escapeMarkdown escapes characters that would break link syntax.
func escapeMarkdown(s string) string { return markdownSpecial.ReplaceAllString(s, `\$1`) }

func codeSpan(content string) string {
	delim := strings.Repeat("`", longestRun(content, '`')+1)
	pad := ""
	if strings.HasPrefix(content, "`") || strings.HasSuffix(content, "`") {
		pad = " "
	}
	return delim + pad + content + pad + delim
}

func quote(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l == "" {
			lines[i] = ">"
		} else {
			lines[i] = "> " + l
		}
	}
	return strings.Join(lines, "\n")
}

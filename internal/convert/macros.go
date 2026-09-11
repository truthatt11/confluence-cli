package convert

import (
	"strconv"
	"strings"
)

var callouts = map[string]bool{"info": true, "tip": true, "note": true, "warning": true}

func (w *walker) macro(n *node) string {
	name := n.attrs["ac:name"]
	switch name {
	case "toc", "floatmenu":
		w.markLossy("macro:" + name)
		return ""
	case "expand":
		return w.expand(n)
	case "code":
		return w.code(n)
	case "anchor":
		return w.anchorMacro(n)
	case "plantuml":
		return w.diagram(n, "plantuml")
	case "children":
		if len(w.opts.Children) > 0 {
			w.markLossy("macro:children")
			return w.children()
		}
	}
	if callouts[name] {
		return w.callout(n, name)
	}
	return w.lossyMacro(n, name)
}

// lossyMacro handles macros Markdown can show but not reproduce.
func (w *walker) lossyMacro(n *node, name string) string {
	switch name {
	case "panel":
		w.markLossy("macro:panel")
		return w.panel(n)
	case "mermaid-macro":
		w.markLossy("macro:mermaid-macro")
		return w.diagram(n, "mermaid")
	case "include":
		w.markLossy("macro:include")
		return w.include(n)
	case "shared-block", "include-shared-block":
		w.markLossy("macro:" + name)
		return w.sharedBlock(n, name)
	case "view-file":
		w.markLossy("macro:view-file")
		return w.viewFile(n)
	}
	return w.unknownMacro(n, name)
}

func param(n *node, name string) *node {
	for _, c := range childElements(n, "ac:parameter") {
		if c.attrs["ac:name"] == name {
			return c
		}
	}
	return nil
}

func macroBody(n *node) []*node {
	if body := child(n, "ac:rich-text-body"); body != nil {
		return body.children
	}
	return nil
}

func (w *walker) expand(n *node) string {
	title := jsTrim(textContent(param(n, "title")))
	body := normalizeBlock(w.nodes(macroBody(n)))
	if title != "" {
		return "\n**EXPAND: " + title + "**\n\n" + body + "\n\n**EXPAND_END**\n"
	}
	return "\n<details>\n<summary>Expand Details</summary>\n\n" + body + "\n\n</details>\n"
}

func (w *walker) code(n *node) string {
	for _, p := range childElements(n, "ac:parameter") {
		if p.attrs["ac:name"] != "language" {
			w.markLossy("code block options")
		}
	}
	return fenced(textContent(param(n, "language")), rawText(child(n, "ac:plain-text-body")))
}

func (w *walker) diagram(n *node, lang string) string {
	return fenced(lang, jsTrim(rawText(child(n, "ac:plain-text-body"))))
}

func fenced(lang, code string) string {
	fence := strings.Repeat("`", fenceLength(code))
	return "\n" + fence + lang + "\n" + code + "\n" + fence + "\n"
}

func (w *walker) callout(n *node, name string) string {
	if len(childElements(n, "ac:parameter")) > 0 {
		w.markLossy("callout options")
	}
	header := "> **" + strings.ToUpper(name) + "**"
	inner := normalizeBlock(w.nodes(macroBody(n)))
	if inner == "" {
		return "\n" + header + "\n"
	}
	return "\n" + header + "\n" + quote(inner) + "\n"
}

func (w *walker) anchorMacro(n *node) string {
	id := jsTrim(textContent(param(n, "")))
	if id == "" {
		return ""
	}
	return "\n**ANCHOR: " + id + "**\n"
}

func (w *walker) panel(n *node) string {
	title := jsTrim(textContent(param(n, "title")))
	body := normalizeBlock(w.nodes(macroBody(n)))
	switch {
	case title == "" && body == "":
		return ""
	case title == "":
		return "\n" + quote(body) + "\n"
	case body == "":
		return "\n> **" + title + "**\n"
	}
	return "\n> **" + title + "**\n>\n" + quote(body) + "\n"
}

func (w *walker) include(n *node) string {
	page := child(child(param(n, ""), "ac:link"), "ri:page")
	if page == nil {
		return ""
	}
	link := "[" + escapeMarkdown(page.attrs["ri:content-title"]) + "]"
	if target := w.pageURL(page); target != "" {
		link += "(" + target + ")"
	}
	return "\n> 📄 **Include Page**: " + link + "\n"
}

func (w *walker) sharedBlock(n *node, name string) string {
	key := jsTrim(textContent(param(n, "shared-block-key")))
	if name == "include-shared-block" {
		if page := child(child(param(n, "page"), "ac:link"), "ri:page"); page != nil {
			keyPart := " "
			if key != "" {
				keyPart = ": " + key + " "
			}
			title := escapeMarkdown(page.attrs["ri:content-title"])
			return "\n> 📄 **Include Shared Block**" + keyPart + "(from page: " + title + " [link needs manual correction])\n"
		}
	}
	body := normalizeBlock(w.nodes(macroBody(n)))
	if key == "" && body == "" {
		return ""
	}
	header := "**Shared Block**"
	if key != "" {
		header = "**Shared Block: " + key + "**"
	}
	if body == "" {
		return "\n> " + header + "\n"
	}
	return "\n> " + header + "\n>\n" + quote(body) + "\n"
}

func (w *walker) viewFile(n *node) string {
	a := child(param(n, "name"), "ri:attachment")
	if a == nil {
		return ""
	}
	f := a.attrs["ri:filename"]
	return "\n📎 [" + f + "](" + w.attachmentPath(f) + ")\n"
}

func (w *walker) children() string {
	lines := make([]string, len(w.opts.Children))
	for i, c := range w.opts.Children {
		lines[i] = "- [" + escapeMarkdown(c.Title) + "](" + c.URL + ")"
	}
	return "\n" + strings.Join(lines, "\n") + "\n"
}

// unknownMacro keeps whatever content the macro has and marks it with an
// HTML comment, so a reader can tell something was there. The original
// converter dropped unknown macros, content included.
func (w *walker) unknownMacro(n *node, name string) string {
	w.markLossy("macro:" + name)
	open := "<!-- macro: " + commentSafe(name+macroParams(n)) + " -->"
	end := "<!-- /macro: " + commentSafe(name) + " -->"
	if plain := child(n, "ac:plain-text-body"); plain != nil {
		return "\n" + open + fenced("", rawText(plain)) + end + "\n"
	}
	inner := w.nodes(macroBody(n))
	switch {
	case jsTrim(inner) == "":
		return open
	case strings.Contains(inner, "\n"):
		return "\n" + open + "\n" + normalizeBlock(inner) + "\n" + end + "\n"
	}
	return open + inner + end
}

func macroParams(n *node) string {
	var b strings.Builder
	for _, p := range childElements(n, "ac:parameter") {
		value := jsTrim(textContent(p))
		if value == "" {
			continue
		}
		key := p.attrs["ac:name"]
		if key == "" {
			key = "default"
		}
		if strings.ContainsAny(value, " \t\"") {
			value = strconv.Quote(value)
		}
		b.WriteString(" " + key + "=" + value)
	}
	return b.String()
}

// commentSafe stops page content from closing the HTML comment early.
func commentSafe(s string) string { return strings.ReplaceAll(s, "-->", "-- >") }

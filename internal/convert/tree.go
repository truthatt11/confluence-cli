package convert

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

// maxDepth guards the recursive walker against pathologically nested input.
const maxDepth = 256

const rootName = "cfl-root"

// node is a minimal DOM for storage format. Text nodes have an empty name.
type node struct {
	name     string // element name including prefix, e.g. "ac:link"
	attrs    map[string]string
	children []*node
	text     string
}

func (n *node) isText() bool { return n.name == "" }

// voidElements never have children. Storage format normally self-closes them,
// but an unclosed <br> must not swallow the text that follows it.
var voidElements = map[string]bool{
	"br": true, "hr": true, "img": true, "col": true, "wbr": true,
	"input": true, "area": true, "source": true,
}

// entities decodes HTML named entities the way the original converter did:
// a few typographic entities fold to ASCII, every other one to its character.
var entities = func() map[string]string {
	m := make(map[string]string, len(xml.HTMLEntity))
	for k, v := range xml.HTMLEntity {
		m[k] = v
	}
	for k, v := range map[string]string{
		"nbsp": " ", "ldquo": `"`, "rdquo": `"`, "lsquo": "'", "rsquo": "'", "hellip": "...",
	} {
		m[k] = v
	}
	return m
}()

// parse builds a tree from a storage fragment. It tolerates the defects real
// pages contain (stray end tags, unclosed elements) instead of failing.
func parse(storage string) (*node, error) {
	d := xml.NewDecoder(strings.NewReader("<" + rootName + ">" + storage + "</" + rootName + ">"))
	d.Strict = false
	d.Entity = entities
	// Deliberately no d.AutoClose = xml.HTMLAutoClose: it matches on the local
	// name only, so <ac:link> would be auto-closed as if it were HTML <link>.
	root := &node{name: rootName}
	stack := []*node{root}
	for {
		tok, err := d.RawToken()
		if errors.Is(err, io.EOF) {
			return root, nil
		}
		if err != nil {
			return nil, fmt.Errorf("parse storage format: %w", err)
		}
		top := stack[len(stack)-1]
		switch t := tok.(type) {
		case xml.StartElement:
			n := &node{name: qname(t.Name), attrs: attrMap(t.Attr)}
			top.children = append(top.children, n)
			if voidElements[n.name] {
				continue
			}
			if len(stack) > maxDepth {
				return nil, fmt.Errorf("storage format nests deeper than %d levels", maxDepth)
			}
			stack = append(stack, n)
		case xml.EndElement:
			stack = closeTo(stack, qname(t.Name))
		case xml.CharData:
			top.children = append(top.children, &node{text: string(t)})
		}
	}
}

// closeTo pops the stack up to the nearest open element called name,
// implicitly closing anything opened inside it. A stray end tag is ignored,
// and the root is never popped.
func closeTo(stack []*node, name string) []*node {
	for i := len(stack) - 1; i > 0; i-- {
		if stack[i].name == name {
			return stack[:i]
		}
	}
	return stack
}

// qname keeps the prefix as written; RawToken does not resolve namespaces,
// and storage format never declares the ac:/ri: prefixes anyway.
func qname(n xml.Name) string {
	if n.Space == "" {
		return n.Local
	}
	return n.Space + ":" + n.Local
}

func attrMap(attrs []xml.Attr) map[string]string {
	m := make(map[string]string, len(attrs))
	for _, a := range attrs {
		m[qname(a.Name)] = a.Value
	}
	return m
}

// child returns the first direct child element called name.
func child(n *node, name string) *node {
	if n == nil {
		return nil
	}
	for _, c := range n.children {
		if c.name == name {
			return c
		}
	}
	return nil
}

// childElements returns the direct child elements whose name is in names.
func childElements(n *node, names ...string) []*node {
	var out []*node
	for _, c := range n.children {
		for _, name := range names {
			if c.name == name {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

func descendants(n *node, name string) []*node {
	var out []*node
	for _, c := range n.children {
		if c.name == name {
			out = append(out, c)
		}
		out = append(out, descendants(c, name)...)
	}
	return out
}

// textContent concatenates all text below n.
func textContent(n *node) string {
	if n == nil {
		return ""
	}
	if n.isText() {
		return n.text
	}
	var b strings.Builder
	for _, c := range n.children {
		b.WriteString(textContent(c))
	}
	return b.String()
}

// rawText concatenates the direct text (and CDATA) children of n only.
func rawText(n *node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	for _, c := range n.children {
		if c.isText() {
			b.WriteString(c.text)
		}
	}
	return b.String()
}

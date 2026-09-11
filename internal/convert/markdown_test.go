package convert

import (
	"strings"
	"testing"
)

func storage(t *testing.T, markdown string) string {
	t.Helper()
	out, err := MarkdownToStorage(markdown)
	if err != nil {
		t.Fatalf("MarkdownToStorage: %v", err)
	}
	return out
}

func TestMarkdownToStorage(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    []string // fragments that must appear
		notWant []string // fragments that must not appear
	}{
		{
			name: "fenced code becomes code macro",
			in:   "```go\nfmt.Println(\"x\")\n```",
			want: []string{`<ac:structured-macro ac:name="code"><ac:parameter ac:name="language">go</ac:parameter><ac:plain-text-body><![CDATA[fmt.Println("x")]]></ac:plain-text-body></ac:structured-macro>`},
		},
		{
			name:    "code without language omits the parameter",
			in:      "```\nplain\n```",
			want:    []string{`<ac:structured-macro ac:name="code"><ac:plain-text-body><![CDATA[plain]]>`},
			notWant: []string{`ac:name="language"`},
		},
		{
			name: "CDATA terminator inside code is split",
			in:   "```\na]]>b\n```",
			want: []string{`<![CDATA[a]]]]><![CDATA[>b]]>`},
		},
		{
			name: "plantuml fence becomes plantuml macro",
			in:   "```plantuml\n@startuml\n@enduml\n```",
			want: []string{`<ac:structured-macro ac:name="plantuml"><ac:plain-text-body><![CDATA[@startuml` + "\n" + `@enduml]]>`},
		},
		{
			name: "bold marker callout, same line form",
			in:   "> **WARNING**\n> Careful now.",
			want: []string{`<ac:structured-macro ac:name="warning"><ac:rich-text-body><p>Careful now.</p>`},
		},
		{
			name:    "bold marker callout, separated form",
			in:      "> **NOTE**\n>\n> Para one.\n>\n> Para two.",
			want:    []string{`<ac:structured-macro ac:name="note"><ac:rich-text-body><p>Para one.</p>`, `<p>Para two.</p>`},
			notWant: []string{"NOTE"},
		},
		{
			name: "GitHub alert maps to callout",
			in:   "> [!CAUTION]\n> Do not run in production.",
			want: []string{`<ac:structured-macro ac:name="warning"><ac:rich-text-body><p>Do not run in production.</p>`},
		},
		{
			name: "ordinary blockquote stays a blockquote",
			in:   "> **Bold** start",
			want: []string{"<blockquote>", "<strong>Bold</strong> start"},
		},
		{
			name: "details becomes expand",
			in:   "<details>\n<summary>More info</summary>\n\nHidden body.\n\n</details>",
			want: []string{`<ac:structured-macro ac:name="expand"><ac:parameter ac:name="title">More info</ac:parameter><ac:rich-text-body>`, `<p>Hidden body.</p>`},
		},
		{
			name: "EXPAND markers become expand",
			in:   "**EXPAND: Show details**\n\nInside.\n\n**EXPAND_END**",
			want: []string{`<ac:parameter ac:name="title">Show details</ac:parameter><ac:rich-text-body><p>Inside.</p>`},
		},
		{
			name: "anchor, toc and children markers",
			in:   "**ANCHOR: top**\n\n[[_TOC_]]\n\n[[_LISTING_]]",
			want: []string{
				`<ac:structured-macro ac:name="anchor"><ac:parameter ac:name="">top</ac:parameter></ac:structured-macro>`,
				`<ac:structured-macro ac:name="toc"></ac:structured-macro>`,
				`<ac:structured-macro ac:name="children"></ac:structured-macro>`,
			},
		},
		{
			name: "task list",
			in:   "- [x] done\n- [ ] todo",
			want: []string{`<ac:task-list><ac:task><ac:task-status>complete</ac:task-status><ac:task-body>done</ac:task-body></ac:task><ac:task><ac:task-status>incomplete</ac:task-status><ac:task-body>todo</ac:task-body></ac:task></ac:task-list>`},
		},
		{
			name: "attachment image and external image",
			in:   "![a](<attachments/my file.png>) ![b](https://example.com/x.png)",
			want: []string{`<ac:image><ri:attachment ri:filename="my file.png" /></ac:image>`, `<ac:image><ri:url ri:value="https://example.com/x.png" /></ac:image>`},
		},
		{
			name: "anchor link and attachment link",
			in:   "[jump](#sec-1) and [spec](attachments/spec.pdf)",
			want: []string{
				`<ac:link ac:anchor="sec-1"><ac:plain-text-link-body><![CDATA[jump]]></ac:plain-text-link-body></ac:link>`,
				`<ac:link><ri:attachment ri:filename="spec.pdf" /><ac:plain-text-link-body><![CDATA[spec]]></ac:plain-text-link-body></ac:link>`,
			},
		},
		{
			name: "external link is a plain anchor",
			in:   "[site](https://example.com/?a=1&b=2)",
			want: []string{`<a href="https://example.com/?a=1&amp;b=2">site</a>`},
		},
		{
			name:    "line break inside a paragraph",
			in:      "line one\nline two",
			want:    []string{"<p>line one<br />line two</p>"},
			notWant: []string{"<br />\n"},
		},
		{
			name:    "allowed inline HTML passes, other HTML is escaped, comments dropped",
			in:      "a <u>u</u> <sub>2</sub> <br> <span onclick=\"x\">s</span> <!-- hidden -->",
			want:    []string{"<u>u</u>", "<sub>2</sub>", "<br />", "&lt;span onclick=&#34;x&#34;&gt;"},
			notWant: []string{"<span", "hidden"},
		},
		{
			name:    "HTML block is escaped",
			in:      "<div>\nraw\n</div>",
			want:    []string{"&lt;div&gt;"},
			notWant: []string{"<div>"},
		},
		{
			name: "table",
			in:   "| A | B |\n| --- | --- |\n| 1 | 2 |",
			want: []string{"<table>", "<th>A</th>", "<td>2</td>"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := storage(t, tt.in)
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("missing %q in\n%s", w, got)
				}
			}
			for _, nw := range tt.notWant {
				if strings.Contains(got, nw) {
					t.Errorf("unexpected %q in\n%s", nw, got)
				}
			}
		})
	}
}

// Markdown in the dialect StorageToMarkdown produces must survive a round trip.
func TestMarkdownRoundTrip(t *testing.T) {
	docs := []string{
		"# Title\n\nPlain **bold** *em* ~~del~~ `code` [link](https://example.com).\n\n---\n\nAfter rule.",
		"- first\n- second\n  - inner one\n  - inner two\n\n1. one\n2. two\n   1. nested",
		"| Name | Status |\n| --- | --- |\n| alpha | **done** |\n| beta | a\\|b |",
		"```javascript\nfunction add(a, b) {\n  return a + b;\n}\n```\n\n````markdown\nhas ``` inside\n````\n\n```plantuml\n@startuml\nA -> B\n@enduml\n```",
		"> **INFO**\n> Heads up.\n\n> **TIP**\n> First.\n>\n> Second.",
		"**EXPAND: Show details**\n\nHidden with **formatting**.\n\n**EXPAND_END**\n\n<details>\n<summary>Expand Details</summary>\n\nUntitled body.\n\n</details>",
		"**ANCHOR: section-one**\n\n[Jump to section](#section-one).",
		"- [x] Finished item\n- [ ] Pending item with *emphasis*",
		"Attached: ![diagram.png](attachments/diagram.png) and ![](https://example.com/r.jpg)",
		"Line one\nline two after a break.",
		"Some <u>underline</u>, H<sub>2</sub>O and x<sup>2</sup>.",
		"> A quoted paragraph.\n>\n> And a second.",
	}
	for _, doc := range docs {
		st := storage(t, doc)
		if err := ValidateStorage(st); err != nil {
			t.Errorf("MarkdownToStorage produced invalid storage: %v\n%s", err, st)
		}
		back, err := StorageToMarkdown(st, StorageOptions{})
		if err != nil {
			t.Fatalf("StorageToMarkdown: %v", err)
		}
		if back.Markdown != doc {
			t.Errorf("round trip changed the document\n--- in ---\n%s\n--- storage ---\n%s\n--- out ---\n%s", doc, st, back.Markdown)
		}
		if len(back.Lossy) > 0 {
			t.Errorf("round-trippable document reported lossy %v:\n%s", back.Lossy, doc)
		}
	}
}

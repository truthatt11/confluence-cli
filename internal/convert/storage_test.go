package convert

import (
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden .expected.md files")

func TestStorageToMarkdownGolden(t *testing.T) {
	files, err := filepath.Glob("testdata/storage-samples/*.xml")
	if err != nil || len(files) == 0 {
		t.Fatalf("no fixtures found: %v", err)
	}
	for _, f := range files {
		name := strings.TrimSuffix(filepath.Base(f), ".xml")
		t.Run(name, func(t *testing.T) {
			in, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			res, err := StorageToMarkdown(string(in), StorageOptions{})
			if err != nil {
				t.Fatal(err)
			}
			got := res.Markdown + "\n"
			golden := strings.TrimSuffix(f, ".xml") + ".expected.md"
			if *update {
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
			}
		})
	}
}

func md(t *testing.T, storage string, o StorageOptions) Result {
	t.Helper()
	res, err := StorageToMarkdown(storage, o)
	if err != nil {
		t.Fatalf("StorageToMarkdown: %v", err)
	}
	return res
}

func TestStorageToMarkdownOptions(t *testing.T) {
	tests := []struct {
		name    string
		storage string
		opts    StorageOptions
		want    string
	}{
		{
			name:    "mention resolved from Users",
			storage: `<p>Ask <ac:link><ri:user ri:userkey="abc123"/></ac:link> first.</p>`,
			opts:    StorageOptions{Users: map[string]string{"abc123": "Chris Yang"}},
			want:    "Ask @Chris Yang first.",
		},
		{
			name:    "mention falls back to username",
			storage: `<p><ac:link><ri:user ri:username="cyang"/></ac:link></p>`,
			want:    "@cyang",
		},
		{
			name:    "page link uses WebBase and default space",
			storage: `<p><ac:link><ri:page ri:content-title="Deploy Guide"/></ac:link></p>`,
			opts:    StorageOptions{WebBase: "https://wiki.example.com/confluence", SpaceKey: "OPS"},
			want:    "[Deploy Guide](https://wiki.example.com/confluence/display/OPS/Deploy%20Guide)",
		},
		{
			name:    "page link escapes plus sign",
			storage: `<p><ac:link><ri:page ri:space-key="DEV" ri:content-title="C++ Guide"/></ac:link></p>`,
			want:    "[C++ Guide](/display/DEV/C%2B%2B%20Guide)",
		},
		{
			name:    "page link with rich body becomes a link when target known",
			storage: `<p><ac:link><ri:page ri:space-key="DEV" ri:content-title="X"/><ac:link-body><strong>go</strong></ac:link-body></ac:link></p>`,
			want:    "[**go**](/display/DEV/X)",
		},
		{
			name:    "children macro rendered from Children",
			storage: `<ac:structured-macro ac:name="children"/>`,
			opts:    StorageOptions{Children: []Link{{Title: "A (1)", URL: "https://w/x?pageId=1"}, {Title: "B", URL: "https://w/x?pageId=2"}}},
			want:    "- [A \\(1\\)](https://w/x?pageId=1)\n- [B](https://w/x?pageId=2)",
		},
		{
			name:    "attachment with spaces uses angle-bracket destination",
			storage: `<p><ac:image><ri:attachment ri:filename="my diagram.png"/></ac:image></p>`,
			opts:    StorageOptions{AttachmentsDir: "files"},
			want:    "![my diagram.png](<files/my diagram.png>)",
		},
		{
			name:    "attachment link",
			storage: `<p><ac:link><ri:attachment ri:filename="spec.pdf"/><ac:plain-text-link-body><![CDATA[the spec]]></ac:plain-text-link-body></ac:link></p>`,
			want:    "[the spec](attachments/spec.pdf)",
		},
		{
			name:    "unknown macro parameters are summarised",
			storage: `<p><ac:structured-macro ac:name="status"><ac:parameter ac:name="title">IN PROGRESS</ac:parameter><ac:parameter ac:name="colour">Yellow</ac:parameter></ac:structured-macro></p>`,
			want:    `<!-- macro: status title="IN PROGRESS" colour=Yellow -->`,
		},
		{
			name:    "unknown macro with plain text body keeps the text",
			storage: `<ac:structured-macro ac:name="noformat"><ac:plain-text-body><![CDATA[raw  text]]></ac:plain-text-body></ac:structured-macro>`,
			want:    "<!-- macro: noformat -->\n```\nraw  text\n```\n<!-- /macro: noformat -->",
		},
		{
			name:    "comment terminator in parameter cannot close the marker",
			storage: `<ac:structured-macro ac:name="x"><ac:parameter ac:name="p">a--&gt;b</ac:parameter></ac:structured-macro>`,
			want:    "<!-- macro: x p=a-- >b -->",
		},
		{
			name:    "pipes in table cells are escaped",
			storage: `<table><tr><th>a|b</th></tr><tr><td>c</td></tr></table>`,
			want:    "| a\\|b |\n| --- |\n| c |",
		},
		{
			name:    "tip callout",
			storage: `<ac:structured-macro ac:name="tip"><ac:rich-text-body><p>Hint.</p></ac:rich-text-body></ac:structured-macro>`,
			want:    "> **TIP**\n> Hint.",
		},
		{
			name:    "cdata content is literal, not entity-decoded",
			storage: `<ac:structured-macro ac:name="code"><ac:plain-text-body><![CDATA[x &amp; y]]></ac:plain-text-body></ac:structured-macro>`,
			want:    "```\nx &amp; y\n```",
		},
		{
			name:    "ordered list nested in ordered list indents by marker width",
			storage: `<ol><li>one<ol><li>inner</li></ol></li></ol>`,
			want:    "1. one\n   1. inner",
		},
		{
			name:    "unclosed br does not swallow following text",
			storage: `<p>a<br>b</p>`,
			want:    "a\nb",
		},
		{
			name:    "stray end tag is ignored",
			storage: `<p>a</strong>b</p>`,
			want:    "ab",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := md(t, tt.storage, tt.opts).Markdown; got != tt.want {
				t.Errorf("got\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

func TestStorageToMarkdownLossy(t *testing.T) {
	tests := []struct {
		name    string
		storage string
		want    []string
	}{
		{"plain content is not lossy", `<p>Hello <strong>x</strong></p><ul><li>a<ul><li>b</li></ul></li></ul>`, nil},
		{"round-trippable macros are not lossy", `<ac:structured-macro ac:name="code"><ac:parameter ac:name="language">go</ac:parameter><ac:plain-text-body><![CDATA[x]]></ac:plain-text-body></ac:structured-macro><ac:structured-macro ac:name="info"><ac:rich-text-body><p>i</p></ac:rich-text-body></ac:structured-macro>`, nil},
		{"layout", `<ac:layout><ac:layout-section><ac:layout-cell><p>x</p></ac:layout-cell></ac:layout-section></ac:layout>`, []string{"page layout"}},
		{"unknown macro", `<ac:structured-macro ac:name="jira"/>`, []string{"macro:jira"}},
		{"panel and toc", `<ac:structured-macro ac:name="panel"><ac:rich-text-body><p>x</p></ac:rich-text-body></ac:structured-macro><ac:structured-macro ac:name="toc"/>`, []string{"macro:panel", "macro:toc"}},
		{"styled text", `<p><span style="color: red;">red</span></p>`, []string{"text styling"}},
		{"mention and page link", `<p><ac:link><ri:user ri:userkey="k"/></ac:link><ac:link><ri:page ri:content-title="T"/></ac:link></p>`, []string{"user mention", "page link"}},
		{"merged cells", `<table><tr><td colspan="2">x</td></tr></table>`, []string{"merged table cells"}},
		{"inline comment marker", `<p><ac:inline-comment-marker ac:ref="r">x</ac:inline-comment-marker></p>`, []string{"inline comment"}},
		{"code options", `<ac:structured-macro ac:name="code"><ac:parameter ac:name="title">T</ac:parameter><ac:plain-text-body><![CDATA[x]]></ac:plain-text-body></ac:structured-macro>`, []string{"code block options"}},
		{"duplicates reported once", `<ac:structured-macro ac:name="jira"/><ac:structured-macro ac:name="jira"/>`, []string{"macro:jira"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := md(t, tt.storage, StorageOptions{}).Lossy; !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStorageToMarkdownRejectsDeepNesting(t *testing.T) {
	deep := strings.Repeat("<span>", maxDepth+10) + "x" + strings.Repeat("</span>", maxDepth+10)
	if _, err := StorageToMarkdown(deep, StorageOptions{}); err == nil {
		t.Fatal("expected an error for pathologically nested input")
	}
}

package convert

import (
	"regexp"
	"strings"
)

// jsSpace matches JavaScript's \s, which the original converter's whitespace
// handling relied on. Unlike Go's \s it includes NBSP and the ideographic space.
func jsSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

func jsTrim(s string) string { return strings.TrimFunc(s, jsSpace) }

// collapseSpace replaces every whitespace run with a single space.
func collapseSpace(s string) string {
	var b strings.Builder
	inSpace := false
	for _, r := range s {
		if jsSpace(r) {
			if !inSpace {
				b.WriteByte(' ')
			}
			inSpace = true
			continue
		}
		inSpace = false
		b.WriteRune(r)
	}
	return b.String()
}

func longestRun(s string, c rune) int {
	longest, run := 0, 0
	for _, r := range s {
		if r != c {
			run = 0
			continue
		}
		run++
		longest = max(longest, run)
	}
	return longest
}

// fenceLength picks the shortest backtick fence that the body cannot close.
func fenceLength(body string) int { return max(3, longestRun(body, '`')+1) }

var fenceOpen = regexp.MustCompile("^ {0,3}(`{3,})")

// splitOnFences returns alternating segments: even indices are text outside
// fenced code, odd indices are complete fenced blocks (delimiters included).
func splitOnFences(text string) []string {
	lines := strings.Split(text, "\n")
	offsets := make([]int, len(lines)+1)
	for i, l := range lines {
		offsets[i+1] = offsets[i] + len(l) + 1
	}
	var segs []string
	last := 0
	for i := 0; i < len(lines); i++ {
		m := fenceOpen.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		end := closingFence(lines, i, len(m[1]))
		if end < 0 {
			continue
		}
		stop := offsets[end] + len(lines[end])
		segs = append(segs, text[last:offsets[i]], text[offsets[i]:stop])
		last = stop
		i = end
	}
	return append(segs, text[last:])
}

// closingFence finds the line closing a fence of n backticks. Like the
// original backtracking regex it retries with shorter runs, and the body must
// contain at least one line break.
func closingFence(lines []string, open, n int) int {
	for size := n; size >= 3; size-- {
		for j := open + 2; j < len(lines); j++ {
			if isClosingFence(lines[j], size) {
				return j
			}
		}
	}
	return -1
}

func isClosingFence(line string, size int) bool {
	rest := strings.TrimLeft(line, " ")
	if len(line)-len(rest) > 3 || !strings.HasPrefix(rest, strings.Repeat("`", size)) {
		return false
	}
	return strings.Trim(rest[size:], " \t") == ""
}

// cleanupWithFences normalises whitespace outside fenced code and trims the result.
func cleanupWithFences(text string) string {
	segs := splitOnFences(text)
	for i := 0; i < len(segs); i += 2 {
		segs[i] = cleanupOutsideFence(segs[i])
	}
	return jsTrim(strings.Join(segs, ""))
}

// normalizeBlock trims a rendered block body and collapses blank-line runs
// outside fenced code, so whitespace between tags in the source does not turn
// into empty quote lines.
func normalizeBlock(s string) string {
	segs := splitOnFences(s)
	for i := 0; i < len(segs); i += 2 {
		segs[i] = collapseBlankLines(segs[i])
	}
	return jsTrim(strings.Join(segs, ""))
}

func cleanupOutsideFence(text string) string {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = stripLeading(strings.TrimRight(l, " \t"))
	}
	lines = blankAfterHeadings(lines)
	s := collapseBlankLines(strings.Join(lines, "\n"))
	lines = strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = squashInline(l)
	}
	return strings.Join(lines, "\n")
}

var markerStart = regexp.MustCompile("^([`>]|[*+-] |\\d+[.)] )")

// stripLeading drops indentation unless it precedes a list, quote or code
// marker. Unlike the original, indentation before a marker is kept intact
// so nested lists survive.
func stripLeading(line string) string {
	rest := strings.TrimLeft(line, " \t")
	if rest == line || markerStart.MatchString(rest) {
		return line
	}
	return rest
}

// blankAfterHeadings ensures a heading line is followed by a blank line.
func blankAfterHeadings(lines []string) []string {
	out := make([]string, 0, len(lines))
	for i, l := range lines {
		out = append(out, l)
		isHeading := len(l) >= 2 && l[0] == '#'
		if isHeading && i+1 < len(lines) && lines[i+1] != "" {
			out = append(out, "")
		}
	}
	return out
}

// collapseBlankLines turns any whitespace run holding three or more line
// breaks into a single blank line.
func collapseBlankLines(s string) string {
	var b strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		if rs[i] != '\n' {
			b.WriteRune(rs[i])
			continue
		}
		j, breaks, lastBreak := i, 0, i
		for ; j < len(rs) && jsSpace(rs[j]); j++ {
			if rs[j] == '\n' {
				breaks++
				lastBreak = j
			}
		}
		if breaks >= 3 {
			b.WriteString("\n\n")
			i = lastBreak
			continue
		}
		b.WriteRune('\n')
	}
	return b.String()
}

// squashInline collapses runs of spaces and tabs, keeping leading indentation.
func squashInline(line string) string {
	rest := strings.TrimLeft(line, " \t")
	indent := line[:len(line)-len(rest)]
	var b strings.Builder
	inRun := false
	for _, r := range rest {
		if r == ' ' || r == '\t' {
			if !inRun {
				b.WriteByte(' ')
			}
			inRun = true
			continue
		}
		inRun = false
		b.WriteRune(r)
	}
	return indent + b.String()
}

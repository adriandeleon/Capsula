package sshconf

import "strings"

// Capsula parses with ssh_config but does not write with it.
//
// ssh_config.Config.String() is lossy in two ways that matter for a hand-edited
// file: leadingSpace is a column count re-emitted as spaces, so tab indentation
// collapses to a single space; and hasEquals is a bool, so "Key=Value" is
// rewritten as "Key = Value". Both silently reformat lines the user wrote.
//
// Instead the original bytes are kept, split into lines, and divided into block
// spans that line up 1:1 with the parsed tree. Writing then means emitting the
// original lines for everything untouched and rendering only what actually
// changed. Round-tripping an unedited file is exact by construction rather than
// by the library's diligence, which also makes CRLF files, missing trailing
// newlines and unusual spacing safe for free.

// splitLines splits b into lines, keeping each line's terminator so that CRLF
// endings and a missing final newline survive.
func splitLines(b []byte) []string {
	if len(b) == 0 {
		return nil
	}
	var out []string
	start := 0
	for i := 0; i < len(b); i++ {
		if b[i] == '\n' {
			out = append(out, string(b[start:i+1]))
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, string(b[start:]))
	}
	return out
}

// lineEnd returns the terminator of a line, or "" if it has none (last line of
// a file that does not end in a newline).
func lineEnd(s string) string {
	switch {
	case strings.HasSuffix(s, "\r\n"):
		return "\r\n"
	case strings.HasSuffix(s, "\n"):
		return "\n"
	default:
		return ""
	}
}

// dominantLineEnd picks the terminator to use for newly written lines.
func dominantLineEnd(lines []string) string {
	crlf := 0
	for _, l := range lines {
		if strings.HasSuffix(l, "\r\n") {
			crlf++
		}
	}
	if crlf > 0 && crlf*2 >= len(lines) {
		return "\r\n"
	}
	return "\n"
}

// indentOf returns the leading whitespace of a line, tabs included.
func indentOf(s string) string {
	return s[:len(s)-len(strings.TrimLeft(s, " \t"))]
}

// isBlockHeader reports whether a raw line opens a new block. ssh starts a new
// block at any Host or Match line regardless of indentation.
func isBlockHeader(line string) bool {
	t := strings.TrimLeft(line, " \t")
	if t == "" || t[0] == '#' {
		return false
	}
	kw := t
	if i := strings.IndexAny(t, " \t=\r\n"); i >= 0 {
		kw = t[:i]
	}
	return strings.EqualFold(kw, "Host") || strings.EqualFold(kw, "Match")
}

// blockSpans divides lines into half-open [start, end) ranges, one per block.
//
// Span 0 is always the preamble — the lines before the first Host or Match —
// which mirrors the implicit leading block that ssh_config always produces, so
// spans and Config.Hosts stay index-aligned.
func blockSpans(lines []string) [][2]int {
	var headers []int
	for i, l := range lines {
		if isBlockHeader(l) {
			headers = append(headers, i)
		}
	}
	first := len(lines)
	if len(headers) > 0 {
		first = headers[0]
	}
	spans := [][2]int{{0, first}}
	for k, h := range headers {
		end := len(lines)
		if k+1 < len(headers) {
			end = headers[k+1]
		}
		spans = append(spans, [2]int{h, end})
	}
	return spans
}

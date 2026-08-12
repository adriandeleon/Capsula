package sshconf

import (
	"strings"

	"github.com/kevinburke/ssh_config"
)

// Edits are committed into f.lines immediately rather than kept in a side map.
//
// The earlier design held rendered text in an override map while f.lines still
// contained the original lines. That works exactly once: after the first edit,
// the block's node list matches the override but blockLines still returns the
// pre-edit lines, so a second edit of the same block indexes the new nodes
// against the old lines and emits the wrong ones. Splicing in place keeps the
// lines, spans and parse tree describing the same file at all times, which is
// the invariant everything else depends on.

// splice replaces the lines of block i and shifts every later span to match.
func (f *File) splice(i int, newLines []string) {
	sp := f.spans[i]
	delta := len(newLines) - (sp[1] - sp[0])

	rest := make([]string, 0, len(f.lines)-(sp[1]-sp[0])+len(newLines))
	rest = append(rest, f.lines[:sp[0]]...)
	rest = append(rest, newLines...)
	rest = append(rest, f.lines[sp[1]:]...)
	f.lines = rest

	f.spans[i][1] += delta
	for k := i + 1; k < len(f.spans); k++ {
		f.spans[k][0] += delta
		f.spans[k][1] += delta
	}
	f.dirty = true
}

// removeBlock deletes block i entirely, along with a single blank line
// immediately before it if there is one.
//
// The blank separator belongs to the preceding block in ssh_config's model, so
// leaving it behind would accumulate empty lines across repeated add/delete
// cycles. Removing it means also dropping the corresponding Empty node from the
// previous block, or lines and nodes would stop agreeing.
func (f *File) removeBlock(i int) {
	start, end := f.spans[i][0], f.spans[i][1]

	if i > 0 && start > 0 && strings.TrimSpace(f.lines[start-1]) == "" {
		if dropTrailingEmpty(f.cfg.Hosts[i-1]) {
			start--
			f.spans[i-1][1]--
		}
	}

	removed := end - start
	f.lines = append(f.lines[:start], f.lines[end:]...)
	f.spans = append(f.spans[:i], f.spans[i+1:]...)
	f.cfg.Hosts = append(f.cfg.Hosts[:i], f.cfg.Hosts[i+1:]...)

	for k := i; k < len(f.spans); k++ {
		f.spans[k][0] -= removed
		f.spans[k][1] -= removed
	}
	f.dirty = true
}

// dropTrailingEmpty removes a trailing blank-line node from a block, reporting
// whether it did. A comment-bearing Empty is left alone: it is content.
func dropTrailingEmpty(h *ssh_config.Host) bool {
	n := len(h.Nodes)
	if n == 0 {
		return false
	}
	e, ok := h.Nodes[n-1].(*ssh_config.Empty)
	if !ok || e.Comment != "" {
		return false
	}
	h.Nodes = h.Nodes[:n-1]
	return true
}

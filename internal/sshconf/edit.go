package sshconf

import (
	"errors"
	"fmt"
	"strings"

	"github.com/kevinburke/ssh_config"
)

// Editing works at line granularity against the original bytes.
//
// Within an edited block, a keyword line whose value and comment are unchanged
// is emitted verbatim, so its original indentation and separator style survive
// even though a sibling line on the next row was rewritten. Only genuinely
// changed lines are re-rendered, and they inherit the indentation of the line
// they replace.
//
// Rendered text is fed back through the parser as a check before it is
// accepted. That catches a rendering mistake at the point it is made rather
// than after it has been written to the user's config.

// ErrReadOnly is returned when a caller tries to edit a block that Capsula
// declines to rewrite.
var ErrReadOnly = errors.New("block is read-only")

// ErrUnalignedFile is returned when the parser and the line scan disagree about
// a file's structure, in which case it is shown but not edited.
var ErrUnalignedFile = errors.New("file structure could not be verified; refusing to edit")

// AddHost appends a new Host block to f and returns it.
func (s *Set) AddHost(f *File, patterns []string, params []Param) (*Block, error) {
	if f == nil {
		return nil, errors.New("no target file")
	}
	if !f.aligned {
		return nil, ErrUnalignedFile
	}
	if err := validatePatterns(patterns); err != nil {
		return nil, err
	}
	if err := validateParams(params); err != nil {
		return nil, err
	}

	indent := inferIndent(f)
	var sb strings.Builder
	sb.WriteString("Host " + strings.Join(patterns, " ") + f.eol)
	for _, p := range params {
		sb.WriteString(paramLine(indent, p, f.eol))
	}
	body := sb.String()

	// Validate the block on its own. A leading separator would parse as a
	// second (empty) block and trip the one-block check below.
	host, err := parseBlock(body)
	if err != nil {
		return nil, err
	}

	// The new block joins f.lines and f.spans like any other, so it is a
	// first-class block immediately: it appears in Blocks(), can be edited or
	// deleted before the file is ever saved, and needs no special case in the
	// writer.
	if n := len(f.lines); n > 0 {
		// A file that ended without a newline would otherwise have the new
		// header appended to its last line.
		if lineEnd(f.lines[n-1]) == "" {
			f.lines[n-1] += f.eol
		}
		if strings.TrimSpace(f.lines[n-1]) != "" {
			// Blank line between blocks. In ssh_config's model a blank line
			// before a Host header belongs to the preceding block, so it is
			// added there to keep spans and nodes aligned.
			f.lines = append(f.lines, f.eol)
			last := len(f.spans) - 1
			f.spans[last][1] = len(f.lines)
			prev := f.cfg.Hosts[last]
			prev.Nodes = append(prev.Nodes, &ssh_config.Empty{})
		}
	}

	start := len(f.lines)
	f.lines = append(f.lines, splitLines([]byte(body))...)
	f.spans = append(f.spans, [2]int{start, len(f.lines)})
	f.cfg.Hosts = append(f.cfg.Hosts, host)
	f.dirty = true

	return &Block{
		Kind:     KindHost,
		Patterns: patterns,
		File:     f,
		host:     host,
		index:    len(f.cfg.Hosts) - 1,
	}, nil
}

// Update rewrites the block in place.
//
// Comment lines, blank lines and Include directives inside the block keep their
// original positions and text. A keyword present in params keeps its position;
// one absent from params is removed; one params has but the block did not is
// appended at the end of the block.
func (b *Block) Update(patterns []string, params []Param) error {
	if b.Kind == KindMatch {
		return fmt.Errorf("%w: Match blocks are conditional and are not rewritten", ErrReadOnly)
	}
	if b.index < 0 {
		return errors.New("block must be reloaded before it can be edited")
	}
	if !b.File.aligned {
		return ErrUnalignedFile
	}
	if b.Kind == KindHost {
		if err := validatePatterns(patterns); err != nil {
			return err
		}
	}
	if err := validateParams(params); err != nil {
		return err
	}

	text := b.render(patterns, params)
	host, err := parseBlock(text)
	if err != nil {
		return err
	}
	if b.Kind != KindGlobal && kindOf(host) != b.Kind {
		return fmt.Errorf("edit would change the block from %v to %v", b.Kind, kindOf(host))
	}

	b.File.splice(b.index, splitLines([]byte(text)))
	b.File.cfg.Hosts[b.index] = host
	b.host = host
	b.Patterns = patterns
	return nil
}

// Delete removes the block from its file.
func (b *Block) Delete() error {
	switch b.Kind {
	case KindMatch:
		return fmt.Errorf("%w: Match blocks are conditional and are not removed", ErrReadOnly)
	case KindGlobal:
		return fmt.Errorf("%w: the defaults block is part of the file preamble", ErrReadOnly)
	}
	if b.index < 0 {
		return errors.New("block must be reloaded before it can be deleted")
	}
	if !b.File.aligned {
		return ErrUnalignedFile
	}
	b.File.removeBlock(b.index)
	// Every Block after this one in the same file now has a stale index, so
	// callers must re-read Set.Blocks(). That is documented on Block.
	b.index = -1
	return nil
}

// render produces the new text of an edited block, reusing original lines
// wherever the edit did not touch them.
func (b *Block) render(patterns []string, params []Param) string {
	raw := b.File.blockLines(b.index)
	eol := b.File.eol

	offset := 0
	var sb strings.Builder
	if b.Kind != KindGlobal {
		offset = 1
		if samePatterns(b.Patterns, patterns) && len(raw) > 0 {
			sb.WriteString(raw[0]) // header untouched, keep its exact text
		} else {
			sb.WriteString("Host " + strings.Join(patterns, " ") + eol)
		}
	}

	used := make([]bool, len(params))
	take := func(key string) (Param, bool) {
		for i, p := range params {
			if !used[i] && strings.EqualFold(p.Key, key) {
				used[i] = true
				return p, true
			}
		}
		return Param{}, false
	}

	indent := ""
	for i, n := range b.host.Nodes {
		line := ""
		if offset+i < len(raw) {
			line = raw[offset+i]
		}
		kv, isKV := n.(*ssh_config.KV)
		if !isKV {
			// Blank line, comment, or Include: never rewritten.
			sb.WriteString(line)
			continue
		}
		if indent == "" {
			indent = indentOf(line)
		}
		p, ok := take(kv.Key)
		if !ok {
			continue // keyword removed
		}
		if p.Value == renderedValue(kv) && p.Comment == kv.Comment && p.Key == kv.Key {
			sb.WriteString(line) // unchanged: keep the original spacing verbatim
			continue
		}
		end := lineEnd(line)
		if end == "" {
			end = eol
		}
		sb.WriteString(paramLine(indentOf(line), p, end))
	}

	if indent == "" {
		indent = inferIndent(b.File)
	}
	for i, p := range params {
		if used[i] {
			continue
		}
		sb.WriteString(paramLine(indent, p, eol))
	}

	out := sb.String()
	// A block that ended at EOF without a newline must still terminate cleanly
	// once something follows it.
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += eol
	}
	return out
}

func paramLine(indent string, p Param, eol string) string {
	line := indent + p.Key
	if p.Value != "" {
		line += " " + p.Value
	}
	if p.Comment != "" {
		line += " #" + p.Comment
	}
	return line + eol
}

// parseBlock re-parses rendered text as a check that it says what was meant.
func parseBlock(text string) (*ssh_config.Host, error) {
	cfg, err := ssh_config.DecodeBytes([]byte(text))
	if err != nil {
		return nil, fmt.Errorf("render produced invalid config: %w", err)
	}
	var blocks []*ssh_config.Host
	for _, h := range cfg.Hosts {
		if kindOf(h) != KindGlobal || len(h.Nodes) > 0 {
			blocks = append(blocks, h)
		}
	}
	switch len(blocks) {
	case 0:
		return nil, errors.New("render produced an empty block")
	case 1:
		return blocks[0], nil
	default:
		// More than one block means a value broke out of its line. validateParams
		// should have caught it; this is the backstop.
		return nil, errors.New("render produced more than one block")
	}
}

// inferIndent guesses a file's house style, so a new line matches the lines
// around it rather than imposing one.
func inferIndent(f *File) string {
	if f != nil {
		for _, l := range f.lines {
			if isBlockHeader(l) || strings.TrimSpace(l) == "" {
				continue
			}
			if in := indentOf(l); in != "" {
				return in
			}
		}
	}
	return "  "
}

func samePatterns(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func validatePatterns(patterns []string) error {
	if len(patterns) == 0 {
		return errors.New("a Host block needs at least one pattern")
	}
	for _, p := range patterns {
		if p == "" {
			return errors.New("empty host pattern")
		}
		if strings.ContainsAny(p, " \t\r\n#") {
			return fmt.Errorf("invalid host pattern %q", p)
		}
	}
	return nil
}

// validateParams rejects anything that would let a value break out of its own
// line. A newline in a value would splice arbitrary directives into the config,
// and values are routinely pasted from elsewhere.
func validateParams(params []Param) error {
	for _, p := range params {
		if strings.TrimSpace(p.Key) == "" {
			return errors.New("empty keyword")
		}
		if strings.ContainsAny(p.Key, " \t\r\n#=") {
			return fmt.Errorf("invalid keyword %q", p.Key)
		}
		if strings.ContainsAny(p.Value, "\r\n") {
			return fmt.Errorf("value for %s spans multiple lines", p.Key)
		}
		if strings.ContainsAny(p.Comment, "\r\n") {
			return fmt.Errorf("comment for %s spans multiple lines", p.Key)
		}
	}
	return nil
}

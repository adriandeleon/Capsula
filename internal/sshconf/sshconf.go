// Package sshconf loads, edits and writes OpenSSH client configuration files
// without disturbing the parts a human wrote by hand.
//
// The guiding constraint is that ~/.ssh/config is hand-edited. Comments, blank
// lines, indentation, keyword casing and — critically — block order all carry
// meaning, so this package never round-trips through a lossy struct. Blocks the
// user has not touched are written back byte-for-byte from the parse tree.
//
// Note that this package deliberately does not answer "what settings apply to
// host X?". Precedence in ssh_config is subtle (first value wins per keyword,
// Match blocks are evaluated in order, Include splices files inline) and
// reimplementing it is a reliable source of wrong answers. Ask ssh itself
// instead — see package internal/effective.
package sshconf

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kevinburke/ssh_config"
)

// maxIncludeDepth matches OpenSSH's own limit on nested Include directives.
const maxIncludeDepth = 5

// Kind distinguishes the three sorts of block that can appear in a config file.
type Kind int

const (
	// KindGlobal is the implicit block at the top of a file, before any Host
	// or Match line. Settings here apply to every host.
	KindGlobal Kind = iota
	// KindHost is an ordinary "Host <patterns>" block.
	KindHost
	// KindMatch is a "Match ..." block. These are conditional and are treated
	// as read-only: rewriting one risks changing when it fires.
	KindMatch
)

func (k Kind) String() string {
	switch k {
	case KindGlobal:
		return "global"
	case KindMatch:
		return "match"
	default:
		return "host"
	}
}

// Set is a loaded ~/.ssh/config together with every file reachable from it
// through Include directives.
type Set struct {
	// Files in load order; Files[0] is the root config.
	Files []*File
}

// File is one physical configuration file on disk.
//
// A File keeps the bytes it was loaded from. Writing splices those bytes rather
// than re-rendering the parse tree, so anything the user did not edit comes
// back out exactly as it went in. See lines.go for why.
type File struct {
	Path string

	cfg   *ssh_config.Config
	lines []string // original lines, terminators kept
	spans [][2]int // one [start, end) line range per block, index-aligned with cfg.Hosts
	eol   string   // terminator to use for newly written lines

	// aligned is false when the line scan and the parser disagree about the
	// shape of the file. Editing is refused in that state rather than risking a
	// splice against the wrong lines.
	aligned bool

	dirty bool
}

// Dirty reports whether this file has unsaved changes.
func (f *File) Dirty() bool { return f.dirty }

// Editable reports whether the parser and the line scan agree about this file.
// A false result means Capsula will display the file but refuse to write it.
func (f *File) Editable() bool { return f.aligned }

// Bytes renders the file as it would be written to disk.
func (f *File) Bytes() []byte {
	// Edits are spliced into f.lines as they are made, so the lines are always
	// the file's current contents.
	return []byte(strings.Join(f.lines, ""))
}

// blockLines returns the original lines of block i.
func (f *File) blockLines(i int) []string {
	sp := f.spans[i]
	return f.lines[sp[0]:sp[1]]
}

// Block is one Host, Match or global block, flattened for display and editing.
//
// A Block is a view onto its File, not a copy. It is invalidated by any
// mutation of that File; call Set.Blocks again after an edit.
type Block struct {
	Kind Kind
	// Patterns as written, e.g. ["web-*", "!web-old"]. Empty for KindGlobal.
	Patterns []string
	// File the block lives in. Matters because a Set spans several files and
	// an edit has to be written back to the right one.
	File *File

	host  *ssh_config.Host
	index int // position within File.cfg.Hosts
}

// Param is a single keyword/value line as written in the file.
type Param struct {
	Key     string
	Value   string
	Comment string
}

// Load reads path and every file it Includes. A missing root config is not an
// error — it yields an empty Set, which is the correct state for a user who has
// simply never made one.
func Load(path string) (*Set, error) {
	set := &Set{}
	seen := map[string]bool{}
	if err := set.load(path, 0, seen); err != nil {
		return nil, err
	}
	return set, nil
}

// EnsureFile returns the loaded file at path, creating an empty in-memory one
// if the set does not have it. This is how a user with no ~/.ssh/config yet
// gets somewhere to put their first host; nothing touches disk until Save.
func (s *Set) EnsureFile(path string) *File {
	for _, f := range s.Files {
		if f.Path == path {
			return f
		}
	}
	f := &File{
		Path:    path,
		cfg:     &ssh_config.Config{},
		spans:   [][2]int{{0, 0}},
		eol:     "\n",
		aligned: true,
	}
	// Config always carries an implicit leading block; mirror it so spans and
	// Hosts stay index-aligned.
	if empty, err := ssh_config.DecodeBytes(nil); err == nil {
		f.cfg = empty
		f.spans = [][2]int{{0, 0}}
		f.aligned = checkAlignment(f)
	}
	s.Files = append(s.Files, f)
	return f
}

// DefaultPath returns ~/.ssh/config.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ssh", "config"), nil
}

func (s *Set) load(path string, depth int, seen map[string]bool) error {
	if depth > maxIncludeDepth {
		return nil // match OpenSSH: stop descending rather than fail the load
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	if seen[abs] {
		return nil // cycle, or the same file included twice
	}
	seen[abs] = true

	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && depth == 0 {
			return nil // no config yet; not an error
		}
		if os.IsNotExist(err) {
			return nil // a glob that matched nothing, or a dangling include
		}
		return fmt.Errorf("read %s: %w", path, err)
	}

	cfg, err := ssh_config.DecodeBytes(raw)
	if err != nil {
		return fmt.Errorf("parse %s: %w", path, err)
	}
	lines := splitLines(raw)
	f := &File{
		Path:  path,
		cfg:   cfg,
		lines: lines,
		spans: blockSpans(lines),
		eol:   dominantLineEnd(lines),
	}
	f.aligned = checkAlignment(f)
	s.Files = append(s.Files, f)

	for _, inc := range includedPaths(cfg, filepath.Dir(path)) {
		if err := s.load(inc, depth+1, seen); err != nil {
			return err
		}
	}
	return nil
}

// checkAlignment verifies that the raw line scan and the parse tree describe
// the same file, which is what makes byte-splicing safe.
//
// The invariant relied on elsewhere is that within a block, node k corresponds
// to exactly one raw line: the line after the header for k=0, and so on. ssh
// config has no line continuations, and blank and comment lines both become
// Empty nodes, so the correspondence holds — but it is verified rather than
// assumed, because a wrong mapping would splice edits onto the wrong lines.
func checkAlignment(f *File) bool {
	if len(f.spans) != len(f.cfg.Hosts) {
		return false
	}
	for i, sp := range f.spans {
		body := sp[1] - sp[0]
		if i > 0 {
			body-- // the header line
		}
		if body != len(f.cfg.Hosts[i].Nodes) {
			return false
		}
	}
	return true
}

// includedPaths extracts and expands the targets of every Include directive in
// cfg.
//
// The directive list is unexported in ssh_config, so the arguments are
// recovered from Include.String(). That is deliberate: the alternative is
// re-lexing the file ourselves, and String() is the same text the library will
// write back, so the two cannot drift apart.
func includedPaths(cfg *ssh_config.Config, dir string) []string {
	var out []string
	for _, h := range cfg.Hosts {
		for _, n := range h.Nodes {
			inc, ok := n.(*ssh_config.Include)
			if !ok {
				continue
			}
			line := strings.TrimSpace(inc.String())
			if i := strings.Index(line, "#"); i >= 0 {
				line = strings.TrimSpace(line[:i])
			}
			line = strings.TrimSpace(strings.TrimPrefix(line, "Include"))
			line = strings.TrimSpace(strings.TrimPrefix(line, "="))
			for _, arg := range strings.Fields(line) {
				out = append(out, expandIncludeArg(arg, dir)...)
			}
		}
	}
	return out
}

// expandIncludeArg resolves one Include argument to concrete paths. Per
// ssh_config(5), a relative path in a user config is taken relative to ~/.ssh,
// not the working directory.
func expandIncludeArg(arg, dir string) []string {
	arg = strings.Trim(arg, `"`)
	if arg == "" {
		return nil
	}
	if strings.HasPrefix(arg, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			arg = filepath.Join(home, strings.TrimPrefix(arg, "~"))
		}
	}
	if !filepath.IsAbs(arg) {
		arg = filepath.Join(dir, arg)
	}
	matches, err := filepath.Glob(arg)
	if err != nil || len(matches) == 0 {
		// A literal path that does not exist yet is still worth returning; load
		// treats a missing file as a no-op.
		if !strings.ContainsAny(arg, "*?[") {
			return []string{arg}
		}
		return nil
	}
	return matches
}

// Blocks returns every block across every loaded file, in file order and then
// in the order they appear within each file.
//
// Order is significant: ssh takes the first value it finds for each keyword, so
// callers must not sort this list for display without making it obvious that
// the displayed order is not the effective one.
func (s *Set) Blocks() []*Block {
	var out []*Block
	for _, f := range s.Files {
		for i, h := range f.cfg.Hosts {
			b := &Block{File: f, host: h, index: i, Kind: kindOf(h)}
			for _, p := range h.Patterns {
				b.Patterns = append(b.Patterns, p.String())
			}
			out = append(out, b)
		}
	}
	return out
}

// Hosts returns only the editable Host blocks, skipping the global preamble and
// any Match blocks.
func (s *Set) Hosts() []*Block {
	var out []*Block
	for _, b := range s.Blocks() {
		if b.Kind == KindHost {
			out = append(out, b)
		}
	}
	return out
}

// kindOf classifies a block using its rendered header line.
//
// ssh_config keeps the implicit and isMatch flags unexported, but String() is
// derived from them, so reading the header back is equivalent and stays correct
// if the library's own rules change.
func kindOf(h *ssh_config.Host) Kind {
	s := h.String()
	line := s
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		line = s[:i]
	}
	switch f := strings.Fields(strings.TrimSpace(line)); {
	case len(f) == 0:
		return KindGlobal
	case strings.EqualFold(f[0], "Match"):
		return KindMatch
	case strings.EqualFold(f[0], "Host"), strings.EqualFold(strings.TrimSuffix(f[0], "="), "Host"):
		return KindHost
	default:
		// First line is a keyword, so this is the implicit leading block.
		return KindGlobal
	}
}

// Alias returns the first non-negated pattern, which is what a user would type
// after "ssh". Returns "" for a global block.
func (b *Block) Alias() string {
	for _, p := range b.Patterns {
		if !strings.HasPrefix(p, "!") {
			return p
		}
	}
	if len(b.Patterns) > 0 {
		return b.Patterns[0]
	}
	return ""
}

// Title is a human label for the block, suitable for a list row.
func (b *Block) Title() string {
	switch b.Kind {
	case KindGlobal:
		return "(defaults)"
	case KindMatch:
		line := b.host.String()
		if i := strings.IndexByte(line, '\n'); i >= 0 {
			line = line[:i]
		}
		return strings.TrimSpace(line)
	default:
		return strings.Join(b.Patterns, " ")
	}
}

// Params returns the keyword lines of the block, in file order, as written.
// Comment-only and blank lines are omitted; Include directives are reported
// with the key "Include".
func (b *Block) Params() []Param {
	var out []Param
	for _, n := range b.host.Nodes {
		switch v := n.(type) {
		case *ssh_config.KV:
			out = append(out, Param{Key: v.Key, Value: renderedValue(v), Comment: v.Comment})
		case *ssh_config.Include:
			line := strings.TrimSpace(v.String())
			line = strings.TrimSpace(strings.TrimPrefix(line, "Include"))
			out = append(out, Param{Key: "Include", Value: strings.TrimSpace(strings.TrimPrefix(line, "=")), Comment: v.Comment})
		}
	}
	return out
}

// renderedValue returns the value exactly as it will be written back.
//
// KV.Value holds the unquoted form, but KV.String() prefers an unexported
// rawValue that preserves the original quoting. Reading Value alone would show
// the user something different from what is in the file, so the value is
// recovered from the rendered line instead.
func renderedValue(kv *ssh_config.KV) string {
	line := kv.String()
	if i := strings.Index(line, "#"); i >= 0 && kv.Comment != "" {
		line = line[:i]
	}
	line = strings.TrimSpace(line)
	line = strings.TrimSpace(strings.TrimPrefix(line, kv.Key))
	line = strings.TrimSpace(strings.TrimPrefix(line, "="))
	return strings.TrimSpace(line)
}

// IdentityFiles returns every IdentityFile value written in this block.
// IdentityFile is one of the few keywords that legitimately repeats, so unlike
// Get this returns all of them.
func (b *Block) IdentityFiles() []string {
	var out []string
	for _, p := range b.Params() {
		if strings.EqualFold(p.Key, "IdentityFile") {
			out = append(out, p.Value)
		}
	}
	return out
}

// Get returns the first value for key within this block, as written. It does
// not consult other blocks — use internal/effective for the resolved value.
func (b *Block) Get(key string) string {
	for _, p := range b.Params() {
		if strings.EqualFold(p.Key, key) {
			return p.Value
		}
	}
	return ""
}

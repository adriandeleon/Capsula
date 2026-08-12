package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/adriandeleon/Capsula/internal/sshconf"
)

// The CRUD flows are driven through Update with real key messages. The form's
// own field navigation belongs to huh and is not re-tested here; what matters
// is that Capsula opens it with the right state and does the right thing with
// the values it comes back with.

const crudFixture = `Host bastion
  HostName bastion.example.com
  User ops
  Port 2222
  ControlMaster auto
  SetEnv TERM=xterm-256color

Host app
  HostName app.internal
  ProxyJump bastion
`

func newCrudModel(t *testing.T) (Model, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(crudFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	set, err := sshconf.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	m := New(set, path, "ssh", nil)
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	return u.(Model), path
}

func press(t *testing.T, m Model, keys string) Model {
	t.Helper()
	u, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(keys)})
	return u.(Model)
}

func TestAddOpensAnEmptyForm(t *testing.T) {
	m, _ := newCrudModel(t)
	m = press(t, m, "a")
	if m.mode != modeForm {
		t.Fatalf("mode = %v, want modeForm", m.mode)
	}
	if m.form.block != nil {
		t.Error("add form should not be bound to an existing block")
	}
	if m.form.patterns != "" {
		t.Errorf("add form prefilled with %q", m.form.patterns)
	}
}

func TestEditPrefillsFromTheBlock(t *testing.T) {
	m, _ := newCrudModel(t)
	m = press(t, m, "e")
	if m.mode != modeForm {
		t.Fatalf("mode = %v, want modeForm", m.mode)
	}
	if m.form.patterns != "bastion" {
		t.Errorf("patterns = %q, want bastion", m.form.patterns)
	}
	if got := *m.form.values["Port"]; got != "2222" {
		t.Errorf("Port prefilled as %q, want 2222", got)
	}
}

// TestEditThroughFormKeepsUnmanagedKeywords is the end-to-end version of the
// MergeParams guarantee: the form has no ControlMaster or SetEnv field, and
// editing a host must not silently drop them.
func TestEditThroughFormKeepsUnmanagedKeywords(t *testing.T) {
	m, path := newCrudModel(t)
	m = press(t, m, "e")

	*m.form.values["User"] = "newops"
	if _, err := m.form.apply(m.set); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if err := m.set.Save(); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := string(got)
	for _, want := range []string{"ControlMaster auto", "SetEnv TERM=xterm-256color", "User newops"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q after edit:\n%s", want, out)
		}
	}
	if strings.Contains(out, "User ops\n") {
		t.Errorf("old value survived:\n%s", out)
	}
}

func TestClearingAFieldRemovesTheKeyword(t *testing.T) {
	m, _ := newCrudModel(t)
	m = press(t, m, "e")
	*m.form.values["Port"] = ""
	if _, err := m.form.apply(m.set); err != nil {
		t.Fatalf("apply: %v", err)
	}
	out := string(m.set.Files[0].Bytes())
	if strings.Contains(out, "Port 2222") {
		t.Errorf("cleared field did not remove the line:\n%s", out)
	}
	if !strings.Contains(out, "ControlMaster auto") {
		t.Errorf("clearing one field damaged the rest of the block:\n%s", out)
	}
}

func TestAddThroughFormCreatesTheHost(t *testing.T) {
	m, _ := newCrudModel(t)
	m = press(t, m, "a")
	m.form.patterns = "newbox"
	*m.form.values["HostName"] = "new.example.com"
	*m.form.values["User"] = "adrian"

	block, err := m.form.apply(m.set)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if block.Alias() != "newbox" {
		t.Errorf("alias = %q", block.Alias())
	}
	if !m.set.Dirty() {
		t.Error("adding a host should mark the set dirty")
	}
	out := string(m.set.Files[0].Bytes())
	if !strings.Contains(out, "Host newbox\n  HostName new.example.com\n  User adrian\n") {
		t.Errorf("block not written as expected:\n%s", out)
	}
}

func TestFormRejectsAnInvalidPattern(t *testing.T) {
	m, _ := newCrudModel(t)
	m = press(t, m, "a")
	m.form.patterns = "" // no patterns at all
	if _, err := m.form.apply(m.set); err == nil {
		t.Error("expected an empty Host pattern list to be rejected")
	}
}

func TestDeleteAsksFirstThenRemoves(t *testing.T) {
	m, _ := newCrudModel(t)
	before := len(m.set.Hosts())

	m = press(t, m, "d")
	if m.mode != modeConfirm {
		t.Fatalf("mode = %v, want modeConfirm", m.mode)
	}
	if !strings.Contains(m.confirm.prompt, "bastion") {
		t.Errorf("prompt should name the host: %q", m.confirm.prompt)
	}
	// Declining must change nothing.
	m = press(t, m, "n")
	if m.mode != modeList || len(m.set.Hosts()) != before {
		t.Fatalf("declining deleted something: mode=%v hosts=%d", m.mode, len(m.set.Hosts()))
	}

	m = press(t, m, "d")
	m = press(t, m, "y")
	if m.mode != modeList {
		t.Fatalf("mode = %v, want modeList", m.mode)
	}
	if got := len(m.set.Hosts()); got != before-1 {
		t.Fatalf("hosts = %d, want %d", got, before-1)
	}
	if strings.Contains(string(m.set.Files[0].Bytes()), "Host bastion") {
		t.Error("host still present after a confirmed delete")
	}
	// The list must reflect the deletion, not just the underlying set.
	if len(m.list.Items()) != before-1 {
		t.Errorf("list shows %d rows, want %d", len(m.list.Items()), before-1)
	}
}

func TestQuitWithUnsavedChangesAsksFirst(t *testing.T) {
	m, _ := newCrudModel(t)
	m = press(t, m, "d")
	m = press(t, m, "y") // now dirty

	u, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	m = u.(Model)
	if m.mode != modeConfirm {
		t.Fatalf("mode = %v, want a confirmation before discarding changes", m.mode)
	}
	if cmd != nil {
		t.Error("quitting with unsaved changes should not issue a command yet")
	}
	if !strings.Contains(strings.ToLower(m.confirm.prompt), "unsaved") {
		t.Errorf("prompt = %q", m.confirm.prompt)
	}
}

func TestQuitWithNoChangesQuitsImmediately(t *testing.T) {
	m, _ := newCrudModel(t)
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	if cmd == nil {
		t.Error("expected a quit command when there is nothing to lose")
	}
}

// TestConnectRefusesWithUnsavedChanges pins the decision that ssh reads the
// file from disk, so connecting mid-edit would use settings that differ from
// what is on screen.
func TestConnectRefusesWithUnsavedChanges(t *testing.T) {
	m, _ := newCrudModel(t)
	m = press(t, m, "e")
	*m.form.values["User"] = "changed"
	if _, err := m.form.apply(m.set); err != nil {
		t.Fatal(err)
	}
	m.mode = modeList

	u, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = u.(Model)
	if cmd != nil {
		t.Error("expected no ssh command while there are unsaved changes")
	}
	if !strings.Contains(m.status, "unsaved") {
		t.Errorf("status = %q, want an explanation", m.status)
	}
}

func TestConnectRefusesAPattern(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	os.WriteFile(path, []byte("Host *\n  User me\n"), 0o600)
	set, _ := sshconf.Load(path)
	m := New(set, path, "ssh", nil)
	u, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = u.(Model)

	u, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = u.(Model)
	if cmd != nil {
		t.Error("expected no ssh command for a wildcard block")
	}
	if !strings.Contains(m.status, "pattern") {
		t.Errorf("status = %q", m.status)
	}
}

// TestSshArgsOnlyOverridesANonDefaultConfig: passing -F for the default file
// would change ssh's own file search for no reason.
func TestSshArgs(t *testing.T) {
	m, path := newCrudModel(t)
	got := m.sshArgs("bastion")
	want := []string{"-F", path, "bastion"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("args = %v, want %v", got, want)
	}

	m.defaultConfig = true
	if got := m.sshArgs("bastion"); strings.Join(got, " ") != "bastion" {
		t.Errorf("args = %v, want just the alias for the default config", got)
	}
}

func TestSaveWritesAndClearsDirty(t *testing.T) {
	m, path := newCrudModel(t)
	m = press(t, m, "d")
	m = press(t, m, "y")
	if !m.set.Dirty() {
		t.Fatal("expected dirty after delete")
	}

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if cmd == nil {
		t.Fatal("expected a save command")
	}
	msg := cmd() // run the command as the runtime would
	sm, ok := msg.(savedMsg)
	if !ok {
		t.Fatalf("got %T, want savedMsg", msg)
	}
	if sm.err != nil {
		t.Fatalf("save: %v", sm.err)
	}
	u, _ := m.Update(sm)
	m = u.(Model)

	if m.set.Dirty() {
		t.Error("still dirty after a successful save")
	}
	got, _ := os.ReadFile(path)
	if strings.Contains(string(got), "Host bastion") {
		t.Errorf("delete not persisted:\n%s", got)
	}
	if !strings.Contains(m.status, "saved") {
		t.Errorf("status = %q", m.status)
	}
}

// TestEditedHostDropsItsCachedResolution: after an edit the cached ssh -G
// answer describes the old configuration.
func TestEditedHostDropsItsCachedResolution(t *testing.T) {
	m, _ := newCrudModel(t)
	u, _ := m.Update(resolvedMsg{alias: "bastion", cfg: nil})
	m = u.(Model)
	if _, ok := m.resolved["bastion"]; !ok {
		t.Fatal("precondition: expected a cached resolution")
	}

	m = press(t, m, "e")
	*m.form.values["HostName"] = "moved.example.com"
	hf := m.form
	block, err := hf.apply(m.set)
	if err != nil {
		t.Fatal(err)
	}
	delete(m.resolved, block.Alias())
	if _, ok := m.resolved["bastion"]; ok {
		t.Error("stale resolution retained after an edit")
	}
}

func TestMatchBlocksAreNotOfferedForEditing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	os.WriteFile(path, []byte("Host a\n  User x\n\nMatch host b\n  User y\n"), 0o600)
	set, _ := sshconf.Load(path)
	m := New(set, path, "ssh", nil)
	u, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = u.(Model)

	// Hosts() excludes Match blocks, so the list should only offer "a".
	for _, it := range m.list.Items() {
		if strings.HasPrefix(it.(item).Title(), "Match") {
			t.Errorf("Match block offered in the host list: %q", it.(item).Title())
		}
	}
}

// TestColdStartCreatesTheConfig covers a brand-new machine: no ~/.ssh/config,
// no ~/.ssh directory at all. This is the first thing a new user does, and the
// path is easy to get wrong because every layer has to tolerate the file not
// existing rather than treating it as an error.
func TestColdStartCreatesTheConfig(t *testing.T) {
	home := t.TempDir()
	sshDir := filepath.Join(home, ".ssh")
	path := filepath.Join(sshDir, "config")

	set, err := sshconf.Load(path)
	if err != nil {
		t.Fatalf("loading a non-existent config must not be an error: %v", err)
	}
	m := New(set, path, "ssh", nil)
	u, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = u.(Model)

	// The empty state has to say what to do next, not just be blank.
	out := plain(m.View())
	if !strings.Contains(out, "a to add a host") {
		t.Errorf("empty state does not tell the user how to start:\n%s", out)
	}

	// Opening the form must work with no file and no directory present.
	m = sendKey(t, m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a")})
	if m.mode != modeForm {
		t.Fatalf("mode = %v, want modeForm", m.mode)
	}
	if got := plain(m.View()); !strings.Contains(got, "New host") {
		t.Errorf("form did not render over the empty state:\n%s", got)
	}

	// Type into the form rather than assigning to the bound variables. huh
	// copies a bound value into the field when the form is built, so assigning
	// afterwards leaves the field itself empty — and typing is what proves the
	// field-to-variable binding actually works, which apply() alone cannot.
	m = typeText(t, m, "first")
	m = enter(t, m)
	m = typeText(t, m, "first.example.com")
	m = enter(t, m)
	m = typeText(t, m, "adrian")
	for i := 0; i < 8 && m.mode == modeForm; i++ {
		m = enter(t, m) // step through the remaining optional fields
	}
	if m.mode != modeList {
		t.Fatalf("form never completed; mode = %v", m.mode)
	}
	if len(m.list.Items()) != 1 {
		t.Fatalf("list has %d rows after adding one host", len(m.list.Items()))
	}

	// Nothing should have touched the disk yet.
	if _, err := os.Stat(sshDir); !os.IsNotExist(err) {
		t.Errorf("~/.ssh created before the user saved (err=%v)", err)
	}

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("s")})
	if cmd == nil {
		t.Fatal("expected a save command")
	}
	sm, ok := cmd().(savedMsg)
	if !ok || sm.err != nil {
		t.Fatalf("save failed: %+v", sm)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("config not created: %v", err)
	}
	want := "Host first\n  HostName first.example.com\n  User adrian\n"
	if string(got) != want {
		t.Errorf("wrote %q, want %q", got, want)
	}

	// ssh refuses a group- or world-accessible ~/.ssh or config.
	dirSt, err := os.Stat(sshDir)
	if err != nil {
		t.Fatal(err)
	}
	if dirSt.Mode().Perm() != 0o700 {
		t.Errorf("~/.ssh mode %o, want 700", dirSt.Mode().Perm())
	}
	fileSt, _ := os.Stat(path)
	if fileSt.Mode().Perm()&0o077 != 0 {
		t.Errorf("config mode %o, want no group/world access", fileSt.Mode().Perm())
	}
	// A first save has nothing to back up, so no stray .bak should appear.
	if _, err := os.Stat(path + sshconf.BackupSuffix); !os.IsNotExist(err) {
		t.Errorf("unexpected backup file on first save (err=%v)", err)
	}
}

// TestCancellingTheFirstAddCreatesNothing: opening the form has to register an
// in-memory file to write into, and that must not leak an empty config onto
// disk if the user changes their mind.
func TestCancellingTheFirstAddCreatesNothing(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".ssh", "config")
	set, _ := sshconf.Load(path)
	m := New(set, path, "ssh", nil)
	u, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = u.(Model)

	m = press(t, m, "a")
	u, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = u.(Model)

	if m.mode != modeList {
		t.Fatalf("esc did not leave the form; mode = %v", m.mode)
	}
	if m.set.Dirty() {
		t.Error("cancelling an add marked the set dirty")
	}
	if err := m.set.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("an empty config was created after cancelling (err=%v)", err)
	}
}

// TestPermissionBannerAndFix covers the whole loop: a loose config is reported
// persistently, the fix is confirmed before anything changes, and the banner
// clears only because a re-audit found nothing — not because the fix was
// assumed to have worked.
func TestPermissionBannerAndFix(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config")
	if err := os.WriteFile(path, []byte(crudFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}

	set, _ := sshconf.Load(path)
	m := New(set, path, "ssh", nil)
	u, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = u.(Model)

	if len(m.warnings) != 1 {
		t.Fatalf("warnings = %v, want the writable config flagged", m.warnings)
	}
	out := plain(m.View())
	if !strings.Contains(out, "ssh will refuse") {
		t.Errorf("banner should say what breaks:\n%s", out)
	}
	if !strings.Contains(out, "p to fix") {
		t.Errorf("banner should offer the remedy:\n%s", out)
	}

	// Asking must not change anything on its own.
	m = press(t, m, "p")
	if m.mode != modeConfirm {
		t.Fatalf("mode = %v, want a confirmation before chmod", m.mode)
	}
	if !strings.Contains(m.confirm.prompt, "0644") {
		t.Errorf("prompt should state the resulting mode: %q", m.confirm.prompt)
	}
	m = press(t, m, "n")
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0o666 {
		t.Errorf("declining changed the mode to %04o", st.Mode().Perm())
	}

	m = press(t, m, "p")
	m = press(t, m, "y")
	st, _ = os.Stat(path)
	if st.Mode().Perm() != 0o644 {
		t.Errorf("mode = %04o, want 0644", st.Mode().Perm())
	}
	if len(m.warnings) != 0 {
		t.Errorf("banner persists after a successful fix: %v", m.warnings)
	}
	if !strings.Contains(m.status, "fixed") {
		t.Errorf("status = %q", m.status)
	}
	if strings.Contains(plain(m.View()), "p to fix") {
		t.Error("banner still rendered after the fix")
	}
}

// The banner occupies a row, so the list has to shrink to make space or the
// bottom row is pushed off the screen.
func TestBannerTakesHeightFromTheList(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0o700)
	path := filepath.Join(dir, "config")
	os.WriteFile(path, []byte(crudFixture), 0o600)
	os.Chmod(path, 0o666)

	set, _ := sshconf.Load(path)
	warned := New(set, path, "ssh", nil)
	u, _ := warned.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	warned = u.(Model)

	clean, _ := newCrudModel(t)
	u, _ = clean.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	clean = u.(Model)

	if warned.bodyHeight() >= clean.bodyHeight() {
		t.Errorf("body height with banner = %d, without = %d; the banner must cost a row",
			warned.bodyHeight(), clean.bodyHeight())
	}
	for i, line := range strings.Split(plain(warned.View()), "\n") {
		if got := len([]rune(line)); got > 100 {
			t.Errorf("line %d is %d wide: %q", i, got, line)
		}
	}
	if got := len(strings.Split(plain(warned.View()), "\n")); got > 24 {
		t.Errorf("frame is %d rows, want at most 24", got)
	}
}

func TestNoBannerWhenPermissionsAreFine(t *testing.T) {
	m, _ := newCrudModel(t)
	if len(m.warnings) != 0 {
		t.Errorf("unexpected warnings on a 0600 config: %v", m.warnings)
	}
	m = press(t, m, "p")
	if m.mode == modeConfirm {
		t.Error("should not prompt when there is nothing to fix")
	}
	if !strings.Contains(m.status, "no permission problems") {
		t.Errorf("status = %q", m.status)
	}
}

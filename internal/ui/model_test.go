package ui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/adriandeleon/Capsula/internal/effective"
	"github.com/adriandeleon/Capsula/internal/sshconf"
)

// These tests drive the model directly rather than through a terminal. Because
// Update performs no I/O — it only returns commands describing I/O to be done —
// the whole interface is exercisable in-process, and no test here spawns ssh.

const fixture = `# work machines
Host bastion
  HostName bastion.example.com
  User ops
  Port 2222

Host app
  HostName app.internal
  ProxyJump bastion

Host *
  ServerAliveInterval 60
`

func newTestModel(t *testing.T) Model {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	set, err := sshconf.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	m := New(set, path, "ssh", nil)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	return updated.(Model)
}

func TestListsHostsInFileOrder(t *testing.T) {
	m := newTestModel(t)
	items := m.list.Items()
	var got []string
	for _, it := range items {
		got = append(got, it.(item).Title())
	}
	want := []string{"bastion", "app", "*"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("hosts = %v, want %v (file order, since first match wins in ssh)", got, want)
	}
}

func TestViewShowsSelectedHostDetail(t *testing.T) {
	m := newTestModel(t)
	out := plain(m.View())
	for _, want := range []string{"bastion", "bastion.example.com", "ops", "2222", "As written", "Effective"} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q:\n%s", want, out)
		}
	}
}

func TestMovingSelectionSwitchesDetail(t *testing.T) {
	m := newTestModel(t)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = next.(Model)
	if got := m.selected().Alias(); got != "app" {
		t.Fatalf("selected = %q, want app", got)
	}
	out := plain(m.View())
	if !strings.Contains(out, "app.internal") {
		t.Errorf("detail did not follow the selection:\n%s", out)
	}
}

// TestEffectiveDivergenceIsFlagged is the case the whole two-representation
// design exists for: the block says nothing about Port, but ssh resolves one
// from a wildcard block, and the user cannot see that by reading their file.
func TestEffectiveDivergenceIsFlagged(t *testing.T) {
	m := newTestModel(t)
	resolved := effective.Parse("hostname bastion.example.com\nuser someoneelse\nport 2222\n")
	next, _ := m.Update(resolvedMsg{alias: "bastion", cfg: resolved})
	m = next.(Model)

	out := plain(m.View())
	if !strings.Contains(out, "block says ops") {
		t.Errorf("divergence between written and effective User was not flagged:\n%s", out)
	}
}

func TestInheritedValuesAreLabelled(t *testing.T) {
	m := newTestModel(t)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown}) // app
	m = next.(Model)
	resolved := effective.Parse("hostname app.internal\nuser adrian\nport 22\nproxyjump bastion\n")
	next, _ = m.Update(resolvedMsg{alias: "app", cfg: resolved})
	m = next.(Model)

	out := plain(m.View())
	if !strings.Contains(out, "inherited") {
		t.Errorf("a value absent from the block should be marked inherited:\n%s", out)
	}
}

// TestPatternsAreNotResolved guards against asking ssh -G about "*", which
// would resolve it as a literal hostname and report confident nonsense.
func TestPatternsAreNotResolved(t *testing.T) {
	m := newTestModel(t)
	for range 2 {
		next, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = next.(Model)
	}
	if got := m.selected().Alias(); got != "*" {
		t.Fatalf("selected = %q, want the wildcard block", got)
	}
	if cmd := m.resolveSelected(); cmd != nil {
		t.Error("resolveSelected should refuse a pattern")
	}
	if out := plain(m.View()); !strings.Contains(out, "pattern, not a single host") {
		t.Errorf("wildcard block should say why it has no effective config:\n%s", out)
	}
}

func TestResolutionIsCachedPerAlias(t *testing.T) {
	m := newTestModel(t)
	if cmd := m.resolveSelected(); cmd == nil {
		t.Fatal("expected a resolve command for a fresh host")
	}
	next, _ := m.Update(resolvedMsg{alias: "bastion", cfg: effective.Parse("hostname x\n")})
	m = next.(Model)
	if cmd := m.resolveSelected(); cmd != nil {
		t.Error("a cached alias should not be resolved again")
	}
}

func TestResolveFailureIsShownNotSwallowed(t *testing.T) {
	m := newTestModel(t)
	next, _ := m.Update(resolvedMsg{alias: "bastion", err: errString("ssh: no such host")})
	m = next.(Model)
	if out := plain(m.View()); !strings.Contains(out, "no such host") {
		t.Errorf("resolve failure should be visible:\n%s", out)
	}
}

func TestEmptyConfigExplainsItself(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config")
	set, err := sshconf.Load(path) // never created
	if err != nil {
		t.Fatal(err)
	}
	m := New(set, path, "ssh", nil)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	out := plain(updated.(Model).View())
	if !strings.Contains(out, "No hosts found") {
		t.Errorf("empty state should explain itself:\n%s", out)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

var ansi = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// plain strips styling so assertions match on content, not on escape codes.
func plain(s string) string { return ansi.ReplaceAllString(s, "") }

// TestFrameNeverExceedsTerminalWidth guards the layout at sizes a user might
// actually have. A single over-long line wraps and shifts every row below it,
// which in a full-screen app looks like corruption rather than a truncation.
func TestFrameNeverExceedsTerminalWidth(t *testing.T) {
	sizes := []struct{ w, h int }{{80, 24}, {100, 30}, {140, 40}, {60, 20}, {40, 15}}
	for _, sz := range sizes {
		m := newTestModel(t)
		u, _ := m.Update(tea.WindowSizeMsg{Width: sz.w, Height: sz.h})
		m = u.(Model)
		// A long path is the realistic worst case for the detail header.
		u, _ = m.Update(resolvedMsg{alias: "bastion", cfg: effective.Parse("hostname a.very.long.hostname.example.com\nuser someone\nport 22\n")})
		m = u.(Model)

		checkWidth(t, m, sz.w, sz.h, "list")

		// Confirm and form modes render different content and must fit too.
		confirmed := press(t, m, "d")
		checkWidth(t, confirmed, sz.w, sz.h, "confirm")

		form := press(t, m, "e")
		checkWidth(t, form, sz.w, sz.h, "form")

		// The empty state renders its own layout and has to fit too; its path
		// can easily be longer than the terminal.
		deep := filepath.Join(t.TempDir(), "some", "quite", "deeply", "nested", "directory", ".ssh", "config")
		empty, _ := sshconf.Load(deep)
		em := New(empty, deep, "ssh", nil)
		u, _ = em.Update(tea.WindowSizeMsg{Width: sz.w, Height: sz.h})
		checkWidth(t, u.(Model), sz.w, sz.h, "empty")
	}
}

func checkWidth(t *testing.T, m Model, w, h int, label string) {
	t.Helper()
	for i, line := range strings.Split(plain(m.View()), "\n") {
		if got := len([]rune(line)); got > w {
			t.Errorf("%s at %dx%d: line %d is %d wide (max %d): %q", label, w, h, i, got, w, line)
		}
	}
}

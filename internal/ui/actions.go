package ui

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"

	"github.com/adriandeleon/Capsula/internal/effective"
	"github.com/adriandeleon/Capsula/internal/keys"
	"github.com/adriandeleon/Capsula/internal/probe"
	"github.com/adriandeleon/Capsula/internal/sshconf"
)

type mode int

const (
	modeList mode = iota
	modeForm
	modeConfirm
)

type savedMsg struct{ err error }

type connectedMsg struct {
	alias string
	err   error
}

type probeMsg probe.Result

// confirmState is a yes/no question and what to do when the answer is yes.
type confirmState struct {
	prompt string
	yes    func(m *Model) tea.Cmd
}

// --- list-mode key handling -------------------------------------------------

func (m Model) updateList(msg tea.Msg) (tea.Model, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m.passToList(msg)
	}
	// While a filter is being typed, every key belongs to the filter.
	if m.list.FilterState() == list.Filtering {
		return m.passToList(msg)
	}

	switch km.String() {
	case "q", "ctrl+c":
		if m.set.Dirty() {
			m.mode = modeConfirm
			m.confirm = confirmState{
				prompt: "Discard unsaved changes and quit?",
				yes:    func(*Model) tea.Cmd { return tea.Quit },
			}
			return m, nil
		}
		return m, tea.Quit

	case "a":
		return m.openForm(nil)

	case "e":
		b := m.selected()
		if b == nil {
			return m, nil
		}
		if b.Kind == sshconf.KindMatch {
			return m.withStatus("Match blocks are shown but not edited", true), nil
		}
		if !b.File.Editable() {
			return m.withStatus("this file could not be verified and is read-only", true), nil
		}
		return m.openForm(b)

	case "d":
		b := m.selected()
		if b == nil {
			return m, nil
		}
		if b.Kind != sshconf.KindHost {
			return m.withStatus("only Host blocks can be deleted", true), nil
		}
		title := b.Title()
		m.mode = modeConfirm
		m.confirm = confirmState{
			// The path is elided rather than truncated at render time so the
			// question itself always survives, however deep the file lives.
			prompt: fmt.Sprintf("Delete %s from %s?", title, elideLeft(shortPath(b.File.Path), 40)),
			yes: func(mm *Model) tea.Cmd {
				target := mm.selected()
				if target == nil {
					return nil
				}
				if err := target.Delete(); err != nil {
					mm.setStatus(err.Error(), true)
					return nil
				}
				mm.refreshItems(mm.list.Index())
				mm.setStatus("deleted "+title+" — press s to save", false)
				return nil
			},
		}
		return m, nil

	case "s":
		if !m.set.Dirty() {
			return m.withStatus("no changes to save", false), nil
		}
		return m, m.save()

	case "enter":
		return m.startConnect()

	case "r":
		return m, m.probeAll()

	case "p":
		if len(m.warnings) == 0 {
			return m.withStatus("no permission problems found", false), nil
		}
		var paths []string
		for _, w := range m.warnings {
			// The full (home-collapsed) path, not the base name: the base of a
			// directory is often uninformative, and this prompt authorises a
			// change to the filesystem, so it should say exactly where.
			paths = append(paths, fmt.Sprintf("%s to %04o", elideLeft(shortPath(w.Path), 32), w.Want.Perm()))
		}
		m.mode = modeConfirm
		m.confirm = confirmState{
			prompt: "Change " + strings.Join(paths, " and ") + "?",
			yes: func(mm *Model) tea.Cmd {
				var failed []string
				for _, w := range mm.warnings {
					if err := keys.Fix(w); err != nil {
						failed = append(failed, err.Error())
					}
				}
				// Re-audit rather than assuming success: a chmod can fail on a
				// file the user does not own, and claiming it worked would be
				// worse than the original warning.
				mm.warnings = keys.Audit(mm.configPath)
				mm.relayout()
				switch {
				case len(failed) > 0:
					mm.setStatus(strings.Join(failed, "; "), true)
				case len(mm.warnings) > 0:
					mm.setStatus("some permissions could not be changed", true)
				default:
					mm.setStatus("permissions fixed", false)
				}
				return nil
			},
		}
		return m, nil
	}

	return m.passToList(msg)
}

func (m Model) passToList(msg tea.Msg) (tea.Model, tea.Cmd) {
	before := m.list.Index()
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	cmds := []tea.Cmd{cmd}
	if m.list.Index() != before {
		cmds = append(cmds, m.resolveSelected())
	}
	return m, tea.Batch(cmds...)
}

// --- form mode --------------------------------------------------------------

func (m Model) openForm(b *sshconf.Block) (tea.Model, tea.Cmd) {
	file := m.targetFile(b)
	if file == nil {
		return m.withStatus("no writable config file", true), nil
	}
	m.form = newHostForm(b, file, m.width, m.height)
	m.mode = modeForm
	m.status = ""
	return m, m.form.form.Init()
}

// targetFile decides which physical file an edit belongs to: the one the block
// already lives in, or the root config for a new host.
//
// Writing a new host into whichever included file happened to be loaded last
// would scatter hosts across a user's carefully organised conf.d, so new hosts
// go to the root config unless the user is editing something that already has
// a home.
func (m Model) targetFile(b *sshconf.Block) *sshconf.File {
	if b != nil {
		return b.File
	}
	return m.set.EnsureFile(m.configPath)
}

func (m Model) updateForm(msg tea.Msg) (tea.Model, tea.Cmd) {
	if km, ok := msg.(tea.KeyMsg); ok && km.String() == "esc" {
		m.mode = modeList
		m.form = nil
		return m, nil
	}

	f, cmd := m.form.form.Update(msg)
	if hf, ok := f.(*huh.Form); ok {
		m.form.form = hf
	}

	switch m.form.form.State {
	case huh.StateAborted:
		m.mode = modeList
		m.form = nil
		return m, nil

	case huh.StateCompleted:
		hf := m.form
		m.mode = modeList
		m.form = nil

		block, err := hf.apply(m.set)
		if err != nil {
			m.setStatus(err.Error(), true)
			return m, nil
		}
		// Indices shift when a block is added or removed, so the list is
		// rebuilt from the set rather than patched.
		m.refreshItems(m.indexOfAlias(block.Alias()))
		// The effective configuration just changed, so anything cached about
		// this host is now wrong.
		delete(m.resolved, block.Alias())
		delete(m.failed, block.Alias())
		m.setStatus("updated "+block.Title()+" — press s to save", false)
		return m, nil
	}

	return m, cmd
}

// --- confirm mode -----------------------------------------------------------

func (m Model) updateConfirm(msg tea.Msg) (tea.Model, tea.Cmd) {
	km, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch km.String() {
	case "y", "Y", "enter":
		action := m.confirm.yes
		m.mode = modeList
		m.confirm = confirmState{}
		if action != nil {
			// The call must complete before m is used as a return operand: Go
			// does not specify the order between a plain operand and a call
			// that mutates it, so "return m, action(&m)" could return the
			// un-mutated copy and make a confirmed delete do nothing.
			cmd := action(&m)
			return m, cmd
		}
		return m, nil
	case "n", "N", "esc", "q", "ctrl+c":
		m.mode = modeList
		m.confirm = confirmState{}
		return m, nil
	}
	return m, nil
}

// --- commands ---------------------------------------------------------------

func (m Model) save() tea.Cmd {
	set := m.set
	return func() tea.Msg { return savedMsg{err: set.Save()} }
}

// startConnect hands the terminal to ssh.
//
// Connecting is refused while there are unsaved changes: ssh reads the file
// from disk, so it would use the old configuration while the screen shows the
// new one. Silently connecting with stale settings is the kind of surprise that
// is very hard to debug from the other end.
func (m Model) startConnect() (tea.Model, tea.Cmd) {
	b := m.selected()
	if b == nil {
		return m, nil
	}
	alias := b.Alias()
	if alias == "" || strings.ContainsAny(alias, "*?!") {
		return m.withStatus("that is a pattern, not a host to connect to", true), nil
	}
	if m.set.Dirty() {
		return m.withStatus("unsaved changes — press s to save, then Enter to connect", true), nil
	}

	args := m.sshArgs(alias)
	cmd := exec.Command(m.sshBin, args...)
	// ExecProcess releases the terminal, runs ssh with a full TTY, and restores
	// the UI when it exits. Doing this by hand means reimplementing terminal
	// save/restore, which is exactly the sort of thing that leaves a user's
	// shell in a broken state after a crash.
	return m, tea.ExecProcess(cmd, func(err error) tea.Msg {
		return connectedMsg{alias: alias, err: err}
	})
}

// sshArgs builds the ssh command line.
//
// -F is passed only for a non-default config, because passing it for the
// default file would subtly change ssh's own search behaviour for no reason.
func (m Model) sshArgs(alias string) []string {
	var args []string
	if !m.defaultConfig {
		args = append(args, "-F", m.configPath)
	}
	return append(args, alias)
}

// probeAll checks reachability for every listed host.
func (m Model) probeAll() tea.Cmd {
	var targets []probe.Target
	for _, it := range m.list.Items() {
		b := it.(item).block
		alias := b.Alias()
		if alias == "" || strings.ContainsAny(alias, "*?!") {
			continue
		}
		t := probe.Target{Alias: alias, ProxyJump: b.Get("ProxyJump")}
		// Prefer resolved values: the alias is frequently not the name that
		// resolves, and a wildcard block may supply the port.
		if cfg, ok := m.resolved[alias]; ok {
			t.HostName = cfg.Get("hostname")
			t.Port = cfg.Get("port")
			if pj := cfg.Get("proxyjump"); pj != "" {
				t.ProxyJump = pj
			}
		} else {
			t.HostName = b.Get("HostName")
			if t.HostName == "" {
				t.HostName = alias
			}
			t.Port = b.Get("Port")
		}
		targets = append(targets, t)
	}
	if len(targets) == 0 {
		return nil
	}

	// Each result is delivered as its own message so rows fill in as answers
	// arrive rather than all at once when the slowest host times out.
	cmds := make([]tea.Cmd, 0, len(targets))
	for _, t := range targets {
		t := t
		cmds = append(cmds, func() tea.Msg {
			return probeMsg(probe.One(context.Background(), t, probe.DefaultTimeout))
		})
	}
	return tea.Batch(cmds...)
}

func (m *Model) resolveAlias(alias string) tea.Cmd {
	sshBin, cfgPath := m.sshBin, m.configPath
	return func() tea.Msg {
		cfg, err := effective.Resolve(context.Background(), sshBin, cfgPath, alias)
		return resolvedMsg{alias: alias, cfg: cfg, err: err}
	}
}

// --- helpers ----------------------------------------------------------------

func (m *Model) setStatus(s string, isErr bool) {
	m.status, m.statusErr = s, isErr
}

func (m Model) withStatus(s string, isErr bool) Model {
	m.setStatus(s, isErr)
	return m
}

// refreshItems rebuilds the list from the set, which is necessary after any
// add or delete because block indices shift.
func (m *Model) refreshItems(selectIndex int) {
	hosts := m.set.Hosts()
	items := make([]list.Item, 0, len(hosts))
	for _, b := range hosts {
		items = append(items, item{block: b, state: m.probes[b.Alias()]})
	}
	m.list.SetItems(items)
	if selectIndex < 0 {
		selectIndex = 0
	}
	if selectIndex >= len(items) {
		selectIndex = len(items) - 1
	}
	if selectIndex >= 0 {
		m.list.Select(selectIndex)
	}
}

func (m Model) indexOfAlias(alias string) int {
	for i, b := range m.set.Hosts() {
		if b.Alias() == alias {
			return i
		}
	}
	return m.list.Index()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

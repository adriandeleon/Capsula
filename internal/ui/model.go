package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/adriandeleon/Capsula/internal/effective"
	"github.com/adriandeleon/Capsula/internal/keys"
	"github.com/adriandeleon/Capsula/internal/probe"
	"github.com/adriandeleon/Capsula/internal/sshconf"
)

// All I/O in this package happens inside a tea.Cmd, never inside Update.
// Update stays a pure function of (model, message), which is what makes the
// behaviour testable without a terminal and what keeps a slow subprocess from
// freezing the interface.

// item adapts a config block to the list widget.
type item struct {
	block *sshconf.Block
	state probe.State
}

func (i item) Title() string { return i.block.Title() }

func (i item) Description() string {
	b := i.block
	var parts []string
	if h := b.Get("HostName"); h != "" {
		parts = append(parts, h)
	}
	if u := b.Get("User"); u != "" {
		parts = append(parts, "user "+u)
	}
	if p := b.Get("Port"); p != "" {
		parts = append(parts, "port "+p)
	}
	if j := b.Get("ProxyJump"); j != "" {
		parts = append(parts, "via "+j)
	}
	if len(parts) == 0 {
		// Fall back to whatever the block does set. A wildcard block often
		// carries only keywords this row has no column for, and reporting "no
		// settings" for it would be plainly wrong.
		for _, p := range b.Params() {
			parts = append(parts, strings.ToLower(p.Key)+" "+p.Value)
			if len(parts) == 2 {
				break
			}
		}
	}
	d := strings.Join(parts, " · ")
	if len(parts) == 0 {
		d = keyStyle.Render("no settings")
	}
	switch i.state {
	case probe.Reachable:
		return okStyle.Render("●") + " " + d
	case probe.Unreachable:
		return errStyle.Render("●") + " " + d
	case probe.Skipped, probe.Checking:
		// Deliberately not a red dot: a host behind a bastion is not
		// unreachable, it is unanswerable from here.
		return hintStyle.Render("○") + " " + d
	}
	return d
}

// FilterValue is what "/" searches. It includes the resolved hostname so that
// searching for a machine by its real name finds the alias for it, which is
// usually what someone half-remembers.
func (i item) FilterValue() string {
	return i.block.Title() + " " + i.block.Get("HostName") + " " + i.block.Get("User")
}

// resolvedMsg carries the result of an "ssh -G" lookup.
type resolvedMsg struct {
	alias string
	cfg   effective.Config
	err   error
}

// Model is the root model.
type Model struct {
	set        *sshconf.Set
	configPath string
	sshBin     string

	list list.Model

	// resolved caches "ssh -G" output per alias. Without it, moving through the
	// list would spawn a subprocess per keystroke.
	resolved map[string]effective.Config
	failed   map[string]error
	pending  string

	width, height int
	loadErr       error

	mode      mode
	form      *hostForm
	confirm   confirmState
	status    string
	statusErr bool

	// defaultConfig records whether configPath is the path ssh would read
	// anyway, which decides whether -F needs to be passed when connecting.
	defaultConfig bool

	// probes holds the last reachability result per alias.
	probes map[string]probe.State

	// warnings are permission problems with the config file or its directory.
	// They are shown persistently rather than as a status message: they do not
	// go away on their own, and every host stops working while one is present.
	warnings []keys.Issue
}

// New builds the model from an already-loaded set.
func New(set *sshconf.Set, configPath, sshBin string, loadErr error) Model {
	defaultPath, _ := sshconf.DefaultPath()
	items := make([]list.Item, 0, len(set.Hosts()))
	for _, b := range set.Hosts() {
		items = append(items, item{block: b})
	}

	l := list.New(items, list.NewDefaultDelegate(), 0, 0)
	l.Title = "Capsula"
	l.Styles.Title = titleStyle
	l.SetShowStatusBar(true)
	l.SetFilteringEnabled(true)
	l.StatusMessageLifetime = 0
	// Order is meaningful in ssh_config — the first matching value for a
	// keyword wins — so the list must never offer to sort itself.
	//
	// The widget's built-in help is switched off because it renders at its
	// natural width regardless of SetSize, which overflows the pane on a narrow
	// terminal and wraps every row below it. The footer in View is truncated.
	l.SetShowHelp(false)

	return Model{
		set:           set,
		configPath:    configPath,
		sshBin:        sshBin,
		list:          l,
		resolved:      map[string]effective.Config{},
		failed:        map[string]error{},
		probes:        map[string]probe.State{},
		loadErr:       loadErr,
		defaultConfig: configPath == defaultPath,
		warnings:      keys.Audit(configPath),
	}
}

func (m Model) Init() tea.Cmd {
	return m.resolveSelected()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Size and async results are handled in every mode: a reply that arrived
	// while a form was open must not be dropped on the floor.
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.relayout()
		if m.form != nil {
			m.form.resize(m.width, m.height)
		}
		return m, nil

	case resolvedMsg:
		if msg.err != nil {
			m.failed[msg.alias] = msg.err
		} else {
			m.resolved[msg.alias] = msg.cfg
		}
		return m, nil

	case probeMsg:
		m.probes[msg.Alias] = msg.State
		m.refreshItems(m.list.Index())
		return m, nil

	case savedMsg:
		if msg.err != nil {
			m.setStatus("save failed: "+msg.err.Error(), true)
		} else {
			m.setStatus("saved "+shortPath(m.configPath), false)
			// A first save creates the file and possibly ~/.ssh, so there may
			// now be permissions worth reporting that did not exist before.
			m.warnings = keys.Audit(m.configPath)
			m.relayout()
		}
		return m, nil

	case connectedMsg:
		if msg.err != nil {
			// A non-zero exit is normal — the remote command may have failed,
			// or the user may have been disconnected — so it is reported
			// rather than treated as an error in Capsula.
			m.setStatus("ssh "+msg.alias+" exited: "+msg.err.Error(), false)
		} else {
			m.setStatus("ssh "+msg.alias+" closed", false)
		}
		return m, nil
	}

	switch m.mode {
	case modeForm:
		return m.updateForm(msg)
	case modeConfirm:
		return m.updateConfirm(msg)
	default:
		return m.updateList(msg)
	}
}

// resolveSelected asks ssh for the effective config of the highlighted host,
// unless the answer is already known.
func (m *Model) resolveSelected() tea.Cmd {
	b := m.selected()
	if b == nil {
		return nil
	}
	alias := b.Alias()
	if alias == "" || strings.ContainsAny(alias, "*?!") {
		// A pattern is not a host; ssh -G would resolve it as a literal name
		// and report something misleading.
		return nil
	}
	if _, ok := m.resolved[alias]; ok {
		return nil
	}
	if _, ok := m.failed[alias]; ok {
		return nil
	}
	m.pending = alias
	return m.resolveAlias(alias)
}

func (m Model) selected() *sshconf.Block {
	it, ok := m.list.SelectedItem().(item)
	if !ok {
		return nil
	}
	return it.block
}

// minDetailWidth is the point below which a detail pane is more confusing than
// no detail pane.
const minDetailWidth = 28

// showDetail reports whether the terminal is wide enough for two panes.
func (m Model) showDetail() bool {
	return m.width-m.listWidth()-4 >= minDetailWidth
}

// listWidth is the natural width of the host list.
func (m Model) listWidth() int {
	w := m.width / 3
	if w < 28 {
		w = 28
	}
	if w > 44 {
		w = 44
	}
	if w > m.width {
		w = m.width
	}
	return w
}

// paneWidth is the width actually given to the list: its natural width when
// there is a detail pane beside it, the whole terminal when there is not.
func (m Model) paneWidth() int {
	if m.showDetail() {
		return m.listWidth()
	}
	return m.width
}

// bodyHeight leaves one row for the footer, and one more for the warning line
// when there is something to warn about.
func (m Model) bodyHeight() int {
	h := m.height - 1
	if len(m.warnings) > 0 {
		h--
	}
	if h < 1 {
		h = 1
	}
	return h
}

// relayout re-sizes the list after anything that changes the available height.
func (m *Model) relayout() {
	m.list.SetSize(m.paneWidth(), m.bodyHeight())
}

// warningLine renders the permission banner, or "" when all is well.
func (m Model) warningLine() string {
	if len(m.warnings) == 0 {
		return ""
	}
	txt := m.warnings[0].String()
	if n := len(m.warnings) - 1; n > 0 {
		txt += fmt.Sprintf(" (+%d more)", n)
	}
	return truncate(warnStyle.Render("! "+txt)+hintStyle.Render("  p to fix"), m.width)
}

func (m Model) View() string {
	if m.width == 0 {
		return "" // wait for the first size message
	}
	if m.mode == modeForm && m.form != nil {
		return m.form.form.View() + "\n" + truncate(hintStyle.Render("esc cancel"), m.width)
	}
	if m.loadErr != nil {
		return errStyle.Render("Could not read configuration: "+m.loadErr.Error()) + "\n"
	}
	if len(m.list.Items()) == 0 {
		return m.withBanner(m.emptyView())
	}

	var body string
	if m.showDetail() {
		detailWidth := m.width - m.listWidth() - 4
		detail := detailPane.Width(detailWidth).Height(m.bodyHeight()).Render(m.detailView(detailWidth - 2))
		body = lipgloss.JoinHorizontal(lipgloss.Top, m.list.View(), detail)
	} else {
		// Too narrow for two panes; the list alone beats a truncated detail.
		body = m.list.View()
	}
	return m.withBanner(body + "\n" + m.footer())
}

// withBanner puts the permission warning above whatever is being shown.
func (m Model) withBanner(s string) string {
	if w := m.warningLine(); w != "" {
		return w + "\n" + s
	}
	return s
}

func (m Model) footer() string {
	if m.mode == modeConfirm {
		return truncate(warnStyle.Render(m.confirm.prompt+"  [y/n]"), m.width)
	}
	if m.status != "" {
		st := hintStyle
		if m.statusErr {
			st = errStyle
		}
		return truncate(st.Render(m.status), m.width)
	}

	hint := "a add · e edit · d delete · s save · enter connect · r check · / filter · q quit"
	if !m.showDetail() {
		hint = "a add · e edit · d del · s save · ⏎ ssh · q quit"
	}
	if m.set.Dirty() {
		hint = warnStyle.Render("● unsaved") + hintStyle.Render("  "+hint)
		return truncate(hint, m.width)
	}
	return truncate(hintStyle.Render(hint), m.width)
}

func (m Model) emptyView() string {
	var sb strings.Builder
	sb.WriteString(titleStyle.Render("Capsula") + "\n\n")
	// Elide only the path, so the sentence that explains the screen always
	// survives however deep the file lives.
	const label = "No hosts found in "
	sb.WriteString(label + elideLeft(shortPath(m.configPath), max(m.width-len(label), 12)) + "\n\n")
	sb.WriteString(truncate(hintStyle.Render("The file is empty, missing, or contains only global defaults."), m.width) + "\n\n")
	sb.WriteString(truncate(hintStyle.Render("Press a to add a host, q to quit."), m.width) + "\n")
	return sb.String()
}

func (m Model) detailView(width int) string {
	b := m.selected()
	if b == nil {
		return ""
	}
	var sb strings.Builder

	sb.WriteString(truncate(titleStyle.Render(b.Title()), width) + "\n")
	// Elided from the left: the filename is the part that identifies which of
	// several included files this host lives in.
	sb.WriteString(hintStyle.Render(elideLeft(shortPath(b.File.Path), width)))
	if !b.File.Editable() {
		sb.WriteString("  " + warnStyle.Render("read-only"))
	}
	sb.WriteString("\n\n")

	sb.WriteString(keyStyle.Render("As written") + "\n")
	params := b.Params()
	if len(params) == 0 {
		sb.WriteString(hintStyle.Render("  (no settings)") + "\n")
	}
	for _, p := range params {
		line := fmt.Sprintf("  %s %s", p.Key, valStyle.Render(p.Value))
		if p.Comment != "" {
			line += hintStyle.Render("  #" + p.Comment)
		}
		sb.WriteString(truncate(line, width) + "\n")
	}

	sb.WriteString("\n" + keyStyle.Render("Effective") + hintStyle.Render("  (ssh -G)") + "\n")
	sb.WriteString(m.effectiveView(b, width))

	return sb.String()
}

// effectiveView shows what ssh will actually do, which can differ from the
// block above whenever a wildcard or Match block also matches this host.
func (m Model) effectiveView(b *sshconf.Block, width int) string {
	alias := b.Alias()
	if alias == "" || strings.ContainsAny(alias, "*?!") {
		return hintStyle.Render("  pattern, not a single host") + "\n"
	}
	if err, ok := m.failed[alias]; ok {
		return errStyle.Render("  "+truncate(err.Error(), width-2)) + "\n"
	}
	cfg, ok := m.resolved[alias]
	if !ok {
		return hintStyle.Render("  resolving…") + "\n"
	}

	var sb strings.Builder
	for _, k := range []string{"hostname", "user", "port", "proxyjump"} {
		v := cfg.Get(k)
		if v == "" || v == "none" {
			continue
		}
		line := fmt.Sprintf("  %s %s", k, v)
		// Call out where the resolved value differs from what this block says,
		// since that is exactly the case a user cannot see by reading the file.
		if written := b.Get(k); written != "" && !strings.EqualFold(written, v) {
			line += warnStyle.Render("  ← block says " + written)
		} else if written == "" {
			line += hintStyle.Render("  (inherited)")
		}
		sb.WriteString(truncate(line, width) + "\n")
	}

	ids := keys.For(cfg, b.IdentityFiles())
	for _, id := range ids {
		switch {
		case id.Missing():
			sb.WriteString(warnStyle.Render("  key missing: "+truncate(shortPath(id.Path), width-16)) + "\n")
		case id.TooOpen():
			sb.WriteString(errStyle.Render(fmt.Sprintf("  key %s is mode %04o; ssh will refuse it", shortPath(id.Path), id.Mode.Perm())) + "\n")
		case id.Private && id.Explicit:
			sb.WriteString(hintStyle.Render("  key ok: "+truncate(shortPath(id.Path), width-12)) + "\n")
		}
	}
	return sb.String()
}

func truncate(s string, width int) string {
	if width <= 1 || lipgloss.Width(s) <= width {
		return s
	}
	return lipgloss.NewStyle().MaxWidth(width).Render(s)
}

// elideLeft trims the front of a string, keeping the tail. Paths are elided
// this way because the end — the filename — is what distinguishes one included
// config from another.
func elideLeft(s string, width int) string {
	if width <= 1 || lipgloss.Width(s) <= width {
		return s
	}
	r := []rune(s)
	keep := width - 1
	if keep < 1 {
		keep = 1
	}
	if keep > len(r) {
		keep = len(r)
	}
	return "…" + string(r[len(r)-keep:])
}

func shortPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(p, home) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return filepath.Clean(p)
}

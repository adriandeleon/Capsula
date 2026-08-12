package ui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"

	"github.com/adriandeleon/Capsula/internal/sshconf"
)

// managedKeys are the keywords the form exposes, in the order new ones are
// appended to a block.
//
// The form deliberately does not try to cover every ssh keyword — there are
// well over a hundred. Anything not listed here is preserved untouched by
// sshconf.MergeParams, so editing a host does not cost the user the
// ControlMaster or SetEnv lines they set up by hand.
var managedKeys = []string{"HostName", "User", "Port", "IdentityFile", "ProxyJump"}

// hostForm wraps a huh form together with what it is editing.
type hostForm struct {
	form  *huh.Form
	block *sshconf.Block // nil when adding
	file  *sshconf.File  // target file when adding

	patterns string
	values   map[string]*string
}

func newHostForm(b *sshconf.Block, f *sshconf.File, width, height int) *hostForm {
	hf := &hostForm{block: b, file: f, values: map[string]*string{}}
	for _, k := range managedKeys {
		v := ""
		if b != nil {
			v = b.Get(k)
		}
		hf.values[k] = &v
	}
	if b != nil {
		hf.patterns = strings.Join(b.Patterns, " ")
	}

	title := "New host"
	if b != nil {
		title = "Edit " + b.Title()
	}

	fields := []huh.Field{
		huh.NewInput().
			Title("Host").
			Description("One or more patterns, space separated").
			Value(&hf.patterns).
			Validate(validatePatternField),
		huh.NewInput().Title("HostName").
			Description("Address to connect to; blank means the alias itself").
			Value(hf.values["HostName"]).Validate(noSpaces("HostName")),
		huh.NewInput().Title("User").Value(hf.values["User"]).Validate(noSpaces("User")),
		huh.NewInput().Title("Port").Description("Blank means 22").
			Value(hf.values["Port"]).Validate(validatePort),
		huh.NewInput().Title("IdentityFile").Value(hf.values["IdentityFile"]),
		huh.NewInput().Title("ProxyJump").Description("Bastion to connect through").
			Value(hf.values["ProxyJump"]).Validate(noSpaces("ProxyJump")),
	}

	hf.form = huh.NewForm(huh.NewGroup(fields...).Title(title)).
		WithShowHelp(true).
		WithWidth(formWidth(width)).
		WithHeight(max(height-2, 10))
	return hf
}

func (hf *hostForm) resize(width, height int) {
	hf.form = hf.form.WithWidth(formWidth(width)).WithHeight(max(height-2, 10))
}

func formWidth(w int) int {
	if w > 72 {
		return 72
	}
	if w < 30 {
		return 30
	}
	return w
}

// apply writes the form's values back into the configuration.
//
// Existing parameters are the base, so keywords the form has no field for
// survive the edit; see sshconf.MergeParams.
func (hf *hostForm) apply(set *sshconf.Set) (*sshconf.Block, error) {
	patterns := sshconf.SplitPatterns(hf.patterns)
	if err := sshconf.ValidatePatterns(patterns); err != nil {
		return nil, err
	}

	edited := make(map[string]string, len(hf.values))
	for k, v := range hf.values {
		edited[k] = strings.TrimSpace(*v)
	}

	if hf.block == nil {
		params := sshconf.MergeParams(nil, edited, managedKeys)
		return set.AddHost(hf.file, patterns, params)
	}
	params := sshconf.MergeParams(hf.block.Params(), edited, managedKeys)
	if err := hf.block.Update(patterns, params); err != nil {
		return nil, err
	}
	return hf.block, nil
}

func validatePatternField(s string) error {
	return sshconf.ValidatePatterns(sshconf.SplitPatterns(s))
}

// noSpaces rejects whitespace in a single-valued field. ssh would treat the
// second word as a separate token, so the value would silently not mean what
// the user typed.
func noSpaces(key string) func(string) error {
	return func(s string) error {
		if strings.ContainsAny(strings.TrimSpace(s), " \t") {
			return fmt.Errorf("%s cannot contain spaces", key)
		}
		return nil
	}
}

func validatePort(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fmt.Errorf("port must be a number")
	}
	if n < 1 || n > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	return nil
}

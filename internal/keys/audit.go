package keys

import (
	"fmt"
	"os"
	"path/filepath"
)

// Audit reports permission problems around the ssh configuration.
//
// The checks are deliberately narrow, and each corresponds to something that
// actually goes wrong rather than to a general tidiness preference:
//
//   - A config file others can write is refused outright by ssh, with "Bad
//     owner or permissions on ~/.ssh/config". Every host stops working at once
//     and the message does not obviously point at a permission bit.
//   - A ~/.ssh directory others can write is not checked by ssh at all, but it
//     lets another local user replace the config or swap in their own key, so
//     the private keys inside being correctly locked down does not help.
//
// Notably absent: group/world *readable* on the directory. ssh does not care,
// it leaks nothing but filenames, and warning about it would train people to
// ignore the warning line — which is the only thing keeping the two real
// problems above visible.
//
// Private key permissions are checked per host by For, since which keys matter
// depends on which host is selected.
type Issue struct {
	Path string
	Mode os.FileMode
	// Want is the mode that would resolve the issue, derived from the current
	// mode so nothing else about it is changed.
	Want os.FileMode
	// Reason is a short explanation in terms of the consequence.
	Reason string
}

func (i Issue) String() string {
	return fmt.Sprintf("%s is mode %04o: %s", filepath.Base(i.Path), i.Mode.Perm(), i.Reason)
}

// groupWorldWrite is the bit pattern that makes both of the checks below fail.
const groupWorldWrite = 0o022

// Audit checks the config file and the directory containing it. A path that
// does not exist yet produces no issue: there is nothing wrong with a machine
// that has not been set up.
func Audit(configPath string) []Issue {
	var out []Issue

	if st, err := os.Stat(configPath); err == nil && !st.IsDir() {
		if m := st.Mode().Perm(); m&groupWorldWrite != 0 {
			out = append(out, Issue{
				Path:   configPath,
				Mode:   st.Mode(),
				Want:   m &^ groupWorldWrite,
				Reason: "others can write it, so ssh will refuse to use it",
			})
		}
	}

	dir := filepath.Dir(configPath)
	if st, err := os.Stat(dir); err == nil && st.IsDir() {
		if m := st.Mode().Perm(); m&groupWorldWrite != 0 {
			out = append(out, Issue{
				Path:   dir,
				Mode:   st.Mode(),
				Want:   m &^ groupWorldWrite,
				Reason: "others can write it, so they can replace your config or keys",
			})
		}
	}

	return out
}

// Fix applies the narrowest change that resolves an issue: it clears the
// offending bits and leaves everything else alone.
//
// Capsula does not do this on its own initiative. Permissions on a directory
// the user created are theirs to choose, and silently changing them is a poor
// trade for a warning line they can act on.
func Fix(i Issue) error {
	if err := os.Chmod(i.Path, i.Want); err != nil {
		return fmt.Errorf("chmod %s: %w", i.Path, err)
	}
	return nil
}

// Package keys inspects the identity files a host is configured to use.
//
// Identity paths are taken from resolved configuration rather than from the
// raw config text, because IdentityFile is where ssh's token expansion shows
// up most: ~ for the home directory and %d, %u, %h, %r and %p for the local
// home, local user, remote host, remote user and port. ssh -G expands all of
// them, so there is nothing left to interpret here beyond checking the disk.
package keys

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/adriandeleon/Capsula/internal/effective"
)

// Identity is one IdentityFile entry and what is actually on disk for it.
type Identity struct {
	Path string
	// Explicit is false for the paths ssh always appends as defaults
	// (~/.ssh/id_rsa and friends), which are usually absent and whose absence
	// is not a problem worth flagging.
	Explicit bool
	Private  bool // the private key file exists
	Public   bool // the matching .pub exists
	// Mode is the private key's permissions, if it exists. ssh refuses to use a
	// key that is readable by anyone else.
	Mode os.FileMode
}

// TooOpen reports whether the private key's permissions would make ssh refuse
// to use it.
func (i Identity) TooOpen() bool {
	return i.Private && i.Mode.Perm()&0o077 != 0
}

// Missing reports an identity the user explicitly asked for that is not there.
func (i Identity) Missing() bool { return i.Explicit && !i.Private }

// For returns the identities of a resolved host configuration.
//
// explicitPaths should be the IdentityFile values written in the config block,
// used only to tell an explicit choice from ssh's built-in default list.
func For(cfg effective.Config, explicitPaths []string) []Identity {
	explicit := map[string]bool{}
	for _, p := range explicitPaths {
		explicit[normalize(p)] = true
	}

	var out []Identity
	for _, p := range cfg.All("identityfile") {
		p = strings.Trim(p, `"`)
		if p == "" {
			continue
		}
		abs := expand(p)
		id := Identity{Path: abs, Explicit: explicit[normalize(p)]}
		if st, err := os.Stat(abs); err == nil && !st.IsDir() {
			id.Private = true
			id.Mode = st.Mode()
		}
		if _, err := os.Stat(abs + ".pub"); err == nil {
			id.Public = true
		}
		out = append(out, id)
	}
	return out
}

// normalize makes an explicit path from the config comparable to what ssh -G
// prints, which differs mainly in whether ~ has been expanded.
func normalize(p string) string {
	p = strings.Trim(p, `"`)
	return expand(p)
}

func expand(p string) string {
	if strings.HasPrefix(p, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

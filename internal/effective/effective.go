// Package effective answers "what settings actually apply to this host?" by
// asking ssh, rather than by reimplementing its rules.
//
// Precedence in ssh_config is subtle: the first value found for a keyword wins,
// Match blocks are evaluated in order and can run commands, Include splices
// files inline at the point of the directive, and tokens like %h and %r are
// expanded late. Reimplementing that is a reliable way to display something
// confidently wrong — a host's real Port when the block on screen says
// otherwise because a Host * further up already set it.
//
// "ssh -G <host>" prints the fully resolved configuration and costs one short
// subprocess. The parse tree in package sshconf is for editing; this is for
// truth.
package effective

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// DefaultTimeout bounds the subprocess. A Match exec directive can run an
// arbitrary command, so resolution is not guaranteed to be fast.
const DefaultTimeout = 5 * time.Second

// Config is a resolved configuration. Keys are lowercased, as ssh prints them.
// Some keywords legitimately repeat (IdentityFile, LocalForward), so values are
// held as a slice.
type Config map[string][]string

// Get returns the first value for key, or "".
func (c Config) Get(key string) string {
	v := c[strings.ToLower(key)]
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

// All returns every value for key.
func (c Config) All(key string) []string { return c[strings.ToLower(key)] }

// Resolve runs "ssh -G alias" and parses the result.
//
// configPath, when non-empty, is passed with -F so that Capsula reports on the
// file it is editing even if that is not the user's default.
func Resolve(ctx context.Context, sshBin, configPath, alias string) (Config, error) {
	if alias == "" {
		return nil, fmt.Errorf("no host alias")
	}
	if sshBin == "" {
		sshBin = "ssh"
	}
	ctx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()

	args := []string{"-G"}
	if configPath != "" {
		args = append(args, "-F", configPath)
	}
	args = append(args, alias)

	cmd := exec.CommandContext(ctx, sshBin, args...)
	out, err := cmd.Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("ssh -G %s timed out after %s", alias, DefaultTimeout)
		}
		var ee *exec.ExitError
		if ok := asExitError(err, &ee); ok && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("ssh -G %s: %s", alias, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("ssh -G %s: %w", alias, err)
	}
	return Parse(string(out)), nil
}

// Parse turns "ssh -G" output into a Config. Exported so it can be tested
// without a working ssh, and so recorded output can be used as a fixture.
func Parse(out string) Config {
	cfg := Config{}
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if line == "" {
			continue
		}
		key, val, found := strings.Cut(line, " ")
		if !found {
			// A keyword with no value still tells us it was set.
			key, val = line, ""
		}
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" {
			continue
		}
		cfg[key] = append(cfg[key], strings.TrimSpace(val))
	}
	return cfg
}

// Available reports whether an ssh binary can be found and understands -G.
// Anything older than OpenSSH 6.8 does not.
func Available(ctx context.Context, sshBin string) bool {
	if sshBin == "" {
		sshBin = "ssh"
	}
	ctx, cancel := context.WithTimeout(ctx, DefaultTimeout)
	defer cancel()
	// A host that cannot resolve is fine; -G does not connect.
	return exec.CommandContext(ctx, sshBin, "-G", "capsula-probe.invalid").Run() == nil
}

func asExitError(err error, target **exec.ExitError) bool {
	if ee, ok := err.(*exec.ExitError); ok {
		*target = ee
		return true
	}
	return false
}

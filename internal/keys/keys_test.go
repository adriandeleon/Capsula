package keys

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adriandeleon/Capsula/internal/effective"
)

// write creates a file and forces its mode. os.WriteFile applies the process
// umask, so the mode has to be set explicitly or these tests would assert
// against whatever the developer's shell happens to be configured with.
func write(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func TestAuditAcceptsACorrectlyLockedDownSetup(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "config")
	write(t, cfg, 0o600)

	if got := Audit(cfg); len(got) != 0 {
		t.Errorf("expected no issues, got %v", got)
	}
}

func TestAuditFlagsAWritableConfig(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0o700)
	cfg := filepath.Join(dir, "config")
	write(t, cfg, 0o666)

	got := Audit(cfg)
	if len(got) != 1 {
		t.Fatalf("got %d issues, want 1: %v", len(got), got)
	}
	if got[0].Path != cfg {
		t.Errorf("issue path = %s, want the config file", got[0].Path)
	}
	if got[0].Want.Perm() != 0o644 {
		t.Errorf("want mode %04o, expected only the write bits cleared (0644)", got[0].Want.Perm())
	}
}

func TestAuditFlagsAWritableDirectory(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, ".ssh")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(sub, 0o777); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(sub, "config")
	write(t, cfg, 0o600)

	got := Audit(cfg)
	if len(got) != 1 {
		t.Fatalf("got %d issues, want 1: %v", len(got), got)
	}
	if got[0].Path != sub {
		t.Errorf("issue path = %s, want the directory", got[0].Path)
	}
	if got[0].Want.Perm() != 0o755 {
		t.Errorf("want mode %04o, expected 0755", got[0].Want.Perm())
	}
}

// A group- or world-*readable* directory is deliberately not an issue: ssh does
// not care, and warning about it would dilute the two warnings that matter.
func TestAuditIgnoresAReadableDirectory(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, ".ssh")
	os.Mkdir(sub, 0o700)
	if err := os.Chmod(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(sub, "config")
	write(t, cfg, 0o644)

	if got := Audit(cfg); len(got) != 0 {
		t.Errorf("readable-but-not-writable should not warn, got %v", got)
	}
}

func TestAuditOnAMachineWithNothingSetUp(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "nope", ".ssh", "config")
	if got := Audit(cfg); len(got) != 0 {
		t.Errorf("a missing setup is not a problem, got %v", got)
	}
}

func TestFixClearsOnlyTheOffendingBits(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0o700)
	cfg := filepath.Join(dir, "config")
	write(t, cfg, 0o646)

	issues := Audit(cfg)
	if len(issues) != 1 {
		t.Fatalf("want 1 issue, got %v", issues)
	}
	if err := Fix(issues[0]); err != nil {
		t.Fatal(err)
	}

	st, err := os.Stat(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// 0646 -> 0644: the group/world read bits are none of our business.
	if got := st.Mode().Perm(); got != 0o644 {
		t.Errorf("mode = %04o, want 0644", got)
	}
	if got := Audit(cfg); len(got) != 0 {
		t.Errorf("issue persists after Fix: %v", got)
	}
}

// --- identity inspection ----------------------------------------------------

func TestForReportsMissingAndPresentKeys(t *testing.T) {
	dir := t.TempDir()
	present := filepath.Join(dir, "id_present")
	write(t, present, 0o600)
	write(t, present+".pub", 0o644)
	missing := filepath.Join(dir, "id_missing")

	cfg := effective.Parse("identityfile " + present + "\nidentityfile " + missing + "\n")
	got := For(cfg, []string{present, missing})
	if len(got) != 2 {
		t.Fatalf("got %d identities, want 2", len(got))
	}

	if !got[0].Private || !got[0].Public {
		t.Errorf("present key reported as %+v", got[0])
	}
	if got[0].Missing() {
		t.Error("present key reported missing")
	}
	if !got[1].Missing() {
		t.Errorf("missing key not reported: %+v", got[1])
	}
}

// TestForOnlyFlagsExplicitKeysAsMissing: ssh appends a default list of identity
// paths that almost never all exist, and reporting those as missing would be
// noise on every host.
func TestForOnlyFlagsExplicitKeysAsMissing(t *testing.T) {
	dir := t.TempDir()
	implicit := filepath.Join(dir, "id_rsa") // never created

	cfg := effective.Parse("identityfile " + implicit + "\n")
	got := For(cfg, nil) // nothing explicit in the block
	if len(got) != 1 {
		t.Fatalf("got %d identities, want 1", len(got))
	}
	if got[0].Explicit {
		t.Error("a key the user did not ask for should not be explicit")
	}
	if got[0].Missing() {
		t.Error("an absent default key should not be reported as missing")
	}
}

func TestTooOpenDetectsAReadableKey(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "id_loose")
	write(t, key, 0o644)

	cfg := effective.Parse("identityfile " + key + "\n")
	got := For(cfg, []string{key})
	if len(got) != 1 {
		t.Fatalf("got %d identities", len(got))
	}
	if !got[0].TooOpen() {
		t.Errorf("mode %04o should be rejected by ssh", got[0].Mode.Perm())
	}

	os.Chmod(key, 0o600)
	got = For(cfg, []string{key})
	if got[0].TooOpen() {
		t.Error("0600 should be acceptable")
	}
}

func TestForExpandsTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	cfg := effective.Parse("identityfile ~/.ssh/id_capsula_nonexistent\n")
	got := For(cfg, nil)
	if len(got) != 1 {
		t.Fatalf("got %d identities", len(got))
	}
	if want := filepath.Join(home, ".ssh", "id_capsula_nonexistent"); got[0].Path != want {
		t.Errorf("path = %q, want %q", got[0].Path, want)
	}
}

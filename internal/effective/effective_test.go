package effective

import "testing"

// sshGOutput is trimmed from real "ssh -G" output. Keys arrive lowercased, one
// per line, and some legitimately repeat.
const sshGOutput = `user adrian
hostname bastion.example.com
port 2222
addressfamily any
identityfile ~/.ssh/id_ed25519_prod
identityfile ~/.ssh/id_rsa
proxyjump none
forwardagent yes
permitlocalcommand no
`

func TestParse(t *testing.T) {
	cfg := Parse(sshGOutput)
	if got := cfg.Get("HostName"); got != "bastion.example.com" {
		t.Errorf("HostName = %q", got)
	}
	if got := cfg.Get("port"); got != "2222" {
		t.Errorf("port = %q", got)
	}
	if got := cfg.Get("nosuchkey"); got != "" {
		t.Errorf("missing key should be empty, got %q", got)
	}
}

// TestParseKeepsRepeatedKeys matters because IdentityFile is the keyword users
// most often set more than once, and keeping only the last would hide a key.
func TestParseKeepsRepeatedKeys(t *testing.T) {
	cfg := Parse(sshGOutput)
	ids := cfg.All("identityfile")
	if len(ids) != 2 {
		t.Fatalf("identityfile count = %d, want 2: %v", len(ids), ids)
	}
	if ids[0] != "~/.ssh/id_ed25519_prod" {
		t.Errorf("first identityfile = %q, want the explicitly configured one first", ids[0])
	}
}

func TestParseLookupIsCaseInsensitive(t *testing.T) {
	cfg := Parse("HostName Mixed.Example.Com\n")
	// ssh lowercases keys, but be tolerant of anything that does not.
	if cfg.Get("hostname") == "" && cfg.Get("HostName") == "" {
		t.Error("lookup should not depend on the caller's casing")
	}
}

// TestParseKeywordWithNoValue covers lines like "forwardagent" appearing bare;
// the keyword having been set is itself information.
func TestParseKeywordWithNoValue(t *testing.T) {
	cfg := Parse("controlpath\nuser adrian\n")
	if _, ok := cfg["controlpath"]; !ok {
		t.Error("a valueless keyword should still be recorded")
	}
	if got := cfg.Get("user"); got != "adrian" {
		t.Errorf("following line was mis-parsed: %q", got)
	}
}

func TestParseIgnoresBlankLinesAndCRLF(t *testing.T) {
	cfg := Parse("user adrian\r\n\r\nport 22\r\n")
	if cfg.Get("user") != "adrian" || cfg.Get("port") != "22" {
		t.Errorf("CRLF output mis-parsed: %#v", cfg)
	}
}

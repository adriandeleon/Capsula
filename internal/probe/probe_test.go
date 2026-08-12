package probe

import (
	"context"
	"net"
	"testing"
	"time"
)

// TestProxyJumpIsSkippedNotFailed is the honesty rule this package exists for.
// A host behind a bastion is not directly dialable, so a TCP attempt fails even
// though ssh would connect. Reporting that as "down" is worse than reporting
// nothing: it is a confident wrong answer about production infrastructure.
func TestProxyJumpIsSkippedNotFailed(t *testing.T) {
	got := One(context.Background(), Target{
		Alias:     "app",
		HostName:  "10.255.255.1", // unroutable; a dial would hang then fail
		ProxyJump: "bastion",
	}, 50*time.Millisecond)

	if got.State != Skipped {
		t.Errorf("state = %v, want Skipped", got.State)
	}
}

// "none" is what ssh -G prints when no jump host applies, so it must not be
// mistaken for a configured one.
func TestProxyJumpNoneIsProbedNormally(t *testing.T) {
	ln := listen(t)
	defer ln.Close()
	host, port, _ := net.SplitHostPort(ln.Addr().String())

	got := One(context.Background(), Target{Alias: "a", HostName: host, Port: port, ProxyJump: "none"}, time.Second)
	if got.State != Reachable {
		t.Errorf("state = %v (%v), want Reachable", got.State, got.Err)
	}
}

func TestReachableAndUnreachable(t *testing.T) {
	ln := listen(t)
	host, port, _ := net.SplitHostPort(ln.Addr().String())

	up := One(context.Background(), Target{Alias: "up", HostName: host, Port: port}, time.Second)
	if up.State != Reachable {
		t.Errorf("open port reported %v (%v)", up.State, up.Err)
	}

	ln.Close() // nothing listening now
	down := One(context.Background(), Target{Alias: "down", HostName: host, Port: port}, time.Second)
	if down.State != Unreachable {
		t.Errorf("closed port reported %v, want Unreachable", down.State)
	}
}

func TestMissingHostNameIsSkipped(t *testing.T) {
	got := One(context.Background(), Target{Alias: "x"}, time.Second)
	if got.State != Skipped {
		t.Errorf("state = %v, want Skipped when there is nothing to dial", got.State)
	}
}

// TestDefaultPortIsUsed checks that an empty Port becomes 22 rather than
// producing a malformed address. The assertion is deliberately about a dial
// having been attempted at all: whether 127.0.0.1:22 answers depends on whether
// the machine running the tests has sshd enabled, so asserting Reachable or
// Unreachable would make the test report on the host rather than on the code.
func TestDefaultPortIsUsed(t *testing.T) {
	got := One(context.Background(), Target{Alias: "x", HostName: "127.0.0.1"}, time.Second)
	if got.State != Reachable && got.State != Unreachable {
		t.Errorf("state = %v; an empty port should still produce a real dial", got.State)
	}
	if got.State == Unreachable && got.Err == nil {
		t.Error("an unreachable result should carry the dial error")
	}
}

func TestRunStreamsEveryTarget(t *testing.T) {
	ln := listen(t)
	defer ln.Close()
	host, port, _ := net.SplitHostPort(ln.Addr().String())

	targets := []Target{
		{Alias: "a", HostName: host, Port: port},
		{Alias: "b", HostName: host, Port: port},
		{Alias: "c", ProxyJump: "a"},
	}
	seen := map[string]State{}
	for r := range Run(context.Background(), targets, 2, time.Second) {
		seen[r.Alias] = r.State
	}
	if len(seen) != 3 {
		t.Fatalf("got %d results, want 3: %v", len(seen), seen)
	}
	if seen["c"] != Skipped {
		t.Errorf("c = %v, want Skipped", seen["c"])
	}
}

// TestRunHonoursCancellation guards against the channel never closing, which
// would leak a goroutine per refresh in a long-running TUI.
func TestRunHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	targets := make([]Target, 50)
	for i := range targets {
		targets[i] = Target{Alias: "t", HostName: "10.255.255.1", Port: "22"}
	}
	ch := Run(ctx, targets, 4, 5*time.Second)
	cancel()

	done := make(chan struct{})
	go func() {
		for range ch {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not close its channel after cancellation")
	}
}

func listen(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return ln
}

// Package probe checks whether hosts are reachable.
//
// The design constraint is honesty. A red dot next to a host the user knows is
// up costs more trust than showing nothing at all, so a probe reports Unknown
// whenever it cannot answer the question properly rather than guessing.
//
// The main case is ProxyJump: a host reached through a bastion is not directly
// dialable, and a TCP connection attempt to it from here will fail even though
// ssh would connect fine. Those are reported as Skipped, not Unreachable.
package probe

import (
	"context"
	"net"
	"sync"
	"time"
)

// State is the outcome of a probe.
type State int

const (
	// Unknown means no probe has run yet.
	Unknown State = iota
	// Checking means a probe is in flight.
	Checking
	// Reachable means a TCP connection was accepted.
	Reachable
	// Unreachable means the connection was refused or timed out.
	Unreachable
	// Skipped means the host cannot be probed meaningfully from here.
	Skipped
)

func (s State) String() string {
	switch s {
	case Checking:
		return "checking"
	case Reachable:
		return "up"
	case Unreachable:
		return "down"
	case Skipped:
		return "n/a"
	default:
		return "?"
	}
}

// DefaultTimeout is the per-host dial budget.
const DefaultTimeout = 3 * time.Second

// DefaultConcurrency bounds in-flight dials. Enough to feel instant on a normal
// config, low enough not to look like a port scan from the network's side.
const DefaultConcurrency = 12

// Target is one host to check. HostName and Port must already be resolved —
// the alias a user types is frequently not the name that resolves.
type Target struct {
	Alias    string
	HostName string
	Port     string
	// ProxyJump, when set, means the host is reached through another and a
	// direct dial would be misleading.
	ProxyJump string
}

// Result is the outcome for one target.
type Result struct {
	Alias   string
	State   State
	Latency time.Duration
	Err     error
}

// Run probes every target, sending results as they arrive and closing the
// channel when all are done.
//
// Results are streamed rather than returned as a slice so that a UI can fill in
// each row as its answer lands instead of waiting for the slowest host.
func Run(ctx context.Context, targets []Target, concurrency int, timeout time.Duration) <-chan Result {
	if concurrency <= 0 {
		concurrency = DefaultConcurrency
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	out := make(chan Result)

	go func() {
		defer close(out)
		sem := make(chan struct{}, concurrency)
		var wg sync.WaitGroup

		for _, t := range targets {
			t := t
			wg.Add(1)
			go func() {
				defer wg.Done()
				select {
				case sem <- struct{}{}:
					defer func() { <-sem }()
				case <-ctx.Done():
					return
				}
				select {
				case out <- One(ctx, t, timeout):
				case <-ctx.Done():
				}
			}()
		}
		wg.Wait()
	}()

	return out
}

// One probes a single target.
func One(ctx context.Context, t Target, timeout time.Duration) Result {
	if t.ProxyJump != "" && t.ProxyJump != "none" {
		return Result{Alias: t.Alias, State: Skipped}
	}
	if t.HostName == "" {
		return Result{Alias: t.Alias, State: Skipped}
	}
	port := t.Port
	if port == "" {
		port = "22"
	}

	d := net.Dialer{Timeout: timeout}
	start := time.Now()
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(t.HostName, port))
	elapsed := time.Since(start)
	if err != nil {
		if ctx.Err() != nil {
			return Result{Alias: t.Alias, State: Unknown, Err: ctx.Err()}
		}
		return Result{Alias: t.Alias, State: Unreachable, Latency: elapsed, Err: err}
	}
	conn.Close()
	return Result{Alias: t.Alias, State: Reachable, Latency: elapsed}
}

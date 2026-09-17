package main

import (
	"net"
	"strings"
	"testing"
)

// A probe must never put a host, a port, a path or a key into the detail
// an operator reads -- the dashboard is a wider audience than the System
// Console.
func assertNoConnectionDetail(t *testing.T, c healthCheck) {
	t.Helper()
	low := strings.ToLower(c.Detail)
	for _, leak := range []string{"192.168", "31013", "database@", "/home/", ".ssh", "ssh:", "bearer", "token"} {
		if strings.Contains(low, leak) {
			t.Errorf("probe detail leaks %q: %q", leak, c.Detail)
		}
	}
}

// With a local command configured, the probe reports that and does NOT
// execute it: running it would spend a real summarisation.
func TestProbeSummarizerLocalCommand(t *testing.T) {
	p := &Plugin{}
	c := p.probeSummarizerWith(configuration{SummarizerCommand: "/opt/honco/summarize.sh"})
	if c.Status != HealthUp {
		t.Errorf("a configured local command should be healthy, got %q", c.Status)
	}
	if !strings.Contains(c.Detail, "not executed") {
		t.Errorf("the detail should say the command was not run: %q", c.Detail)
	}
	assertNoConnectionDetail(t, c)
}

// An unreachable host is reported as down, with the class and not the
// error text.
func TestProbeSummarizerUnreachable(t *testing.T) {
	p := &Plugin{}
	// 198.51.100.0/24 is TEST-NET-2 (RFC 5737): guaranteed not routed.
	c := p.probeSummarizerWith(configuration{
		SummarizerHost: "198.51.100.1", SummarizerPort: 31013,
		SummarizerKeyPath: "/home/harshi/.ssh/k",
	})
	if c.Status != HealthDown {
		t.Errorf("an unroutable host should be down, got %q (%s)", c.Status, c.Detail)
	}
	if c.Detail != "not reachable from this server" {
		t.Errorf("unexpected detail: %q", c.Detail)
	}
	assertNoConnectionDetail(t, c)
}

// A host shaped like a flag must be refused before it reaches the dialer.
func TestProbeSummarizerRejectsBadHost(t *testing.T) {
	p := &Plugin{}
	for _, h := range []string{"-oProxyCommand=x", "host name", "a;b"} {
		c := p.probeSummarizerWith(configuration{SummarizerHost: h, SummarizerPort: 31013})
		if c.Status != HealthDown || c.Detail != "bad host configuration" {
			t.Errorf("host %q should be refused, got %q/%q", h, c.Status, c.Detail)
		}
	}
}

// Reachable but with no key is degraded, not healthy: a generation would
// still fail, and saying "healthy" would mislead an operator.
func TestProbeSummarizerReachableWithoutKey(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot listen: %v", err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	host, port, _ := net.SplitHostPort(ln.Addr().String())
	p := &Plugin{}

	c := p.probeSummarizerWith(configuration{SummarizerHost: host, SummarizerPort: atoiOrZero(port)})
	if c.Status != HealthDegraded {
		t.Errorf("reachable without a key should be degraded, got %q (%s)", c.Status, c.Detail)
	}
	if !strings.Contains(c.Detail, "no SSH key") {
		t.Errorf("the detail should name the missing key: %q", c.Detail)
	}

	// With a key it is healthy, but must still say the key is unverified.
	c = p.probeSummarizerWith(configuration{
		SummarizerHost: host, SummarizerPort: atoiOrZero(port),
		SummarizerKeyPath: "/home/harshi/.ssh/k",
	})
	if c.Status != HealthUp {
		t.Errorf("reachable with a key should be healthy, got %q (%s)", c.Status, c.Detail)
	}
	if !strings.Contains(c.Detail, "not verified") {
		t.Errorf("the probe must not claim the key works: %q", c.Detail)
	}
	assertNoConnectionDetail(t, c)
}

func atoiOrZero(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
	}
	return n
}

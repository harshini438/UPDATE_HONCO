package main

import "testing"

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Honco":                "honco",
		"  Honco  Inc  ":       "honco-inc",
		"Acme, Corp.":          "acme-corp",
		"UPPER_snake case":     "upper-snake-case",
		"multi---dash":         "multi-dash",
		"---leading-trailing-": "leading-trailing",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidOrgSlug(t *testing.T) {
	ok := []string{"honco", "acme-corp", "a1", "team-42"}
	bad := []string{"", "-lead", "trail-", "UP", "has space", "a", "with_underscore", "toolong-" + string(make([]byte, 70))}
	for _, s := range ok {
		if !validOrgSlug(s) {
			t.Errorf("validOrgSlug(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if validOrgSlug(s) {
			t.Errorf("validOrgSlug(%q) = true, want false", s)
		}
	}
}

func TestTrimTo(t *testing.T) {
	if got := trimTo("  hello  ", 100); got != "hello" {
		t.Errorf("trim: %q", got)
	}
	if got := trimTo("abcdef", 3); got != "abc" {
		t.Errorf("cap: %q", got)
	}
	// rune-safe: a multibyte string is not cut mid-character
	if got := trimTo("héllo", 2); got != "hé" {
		t.Errorf("rune cap: %q", got)
	}
}

// dmDecision is the DM boundary rule. These cover the isolation contract --
// same-org allow, cross-org block, guest block, and (Phase 6) SAFE DENIAL
// when membership could not be determined -- without a database or a
// running server.
func TestDMDecision(t *testing.T) {
	cases := []struct {
		name         string
		orgs         []string
		guest        bool
		lookupFailed bool
		allowed      bool
	}{
		{"same org DM", []string{"orgA", "orgA"}, false, false, true},
		{"single participant", []string{"orgA"}, false, false, true},
		{"missing membership resolves to one org (allow)", []string{"default", "default"}, false, false, true},
		{"empty set is allowed", nil, false, false, true},
		{"cross org DM blocked", []string{"orgA", "orgB"}, false, false, false},
		{"cross org GM blocked", []string{"orgA", "orgA", "orgB"}, false, false, false},
		{"guest blocked even same org", []string{"orgA", "orgA"}, true, false, false},
		{"guest blocked alone", nil, true, false, false},
		{"lookup failure = SAFE DENIAL", []string{"orgA", "orgA"}, false, true, false},
		{"lookup failure overrides a would-be allow", []string{"orgA"}, false, true, false},
		{"lookup failure takes precedence over guest", nil, true, true, false},
	}
	for _, c := range cases {
		allowed, reason := dmDecision(c.orgs, c.guest, c.lookupFailed)
		if allowed != c.allowed {
			t.Errorf("%s: allowed=%v want %v (reason=%q)", c.name, allowed, c.allowed, reason)
		}
		if !allowed && reason == "" {
			t.Errorf("%s: a block must carry a reason", c.name)
		}
	}
	// The safe-denial reason must be distinct from the cross-org and guest
	// reasons, so an operator can tell an error apart from a policy block.
	_, failReason := dmDecision(nil, false, true)
	_, crossReason := dmDecision([]string{"a", "b"}, false, false)
	if failReason == crossReason {
		t.Error("safe-denial reason should differ from the cross-org reason")
	}
}

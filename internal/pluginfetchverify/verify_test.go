// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginfetchverify

import "testing"

// TestIsRegistryHost pins the predicate that decides whether a fetched plugin
// carries a registry signature to check at all. It is the gate in front of the
// signature branch of Verify, so a row that flipped the wrong way would either
// skip a verifiable artifact (unverified, silently) or send a host-less identity
// to referrer discovery against a location that does not exist.
func TestIsRegistryHost(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		host string
		want bool
	}{
		{name: "dotted host is a registry", host: "ghcr.io", want: true},
		{name: "dotted host with port is a registry", host: "reg.example.com:5000", want: true},
		{name: "bare host with port is a registry", host: "localhost:5000", want: true},
		{name: "empty is not a registry", host: "", want: false},
		{name: "bare catalog name is not a registry", host: "linter", want: false},
		{name: "relative-path artifact is not a registry", host: ".", want: false},
		{name: "relative-path prefix is not a registry", host: "./plugins", want: false},
		{name: "absolute path is not a registry", host: "/opt/plugins", want: false},
		{name: "trailing dot is not a registry", host: "example.", want: false},
		{name: "leading dot is not a registry", host: ".example", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := IsRegistryHost(tc.host); got != tc.want {
				t.Errorf("IsRegistryHost(%q) = %v; want %v", tc.host, got, tc.want)
			}
		})
	}
}

// TestCanonicalStringSetsEqual pins the set semantics the host-function stamp
// comparison rests on. The derived side is canonical by construction and the
// stamped side is hand-editable, so order and duplicates must not decide a
// mismatch — while a genuinely different member must.
func TestCanonicalStringSetsEqual(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		a    []string
		b    []string
		want bool
	}{
		{name: "identical", a: []string{"aggrega_log"}, b: []string{"aggrega_log"}, want: true},
		{name: "order ignored", a: []string{"b", "a"}, b: []string{"a", "b"}, want: true},
		{name: "duplicates ignored", a: []string{"a", "a", "b"}, b: []string{"b", "a"}, want: true},
		{name: "nil equals empty", a: nil, b: []string{}, want: true},
		{name: "nil equals nil", a: nil, b: nil, want: true},
		{name: "a missing member differs", a: []string{"a"}, b: []string{"a", "b"}, want: false},
		{name: "a different member differs", a: []string{"a"}, b: []string{"b"}, want: false},
		{name: "stamped where none derived differs", a: []string{"aggrega_log"}, b: nil, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := canonicalStringSetsEqual(tc.a, tc.b); got != tc.want {
				t.Errorf("canonicalStringSetsEqual(%v, %v) = %v; want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// TestStampedHostFunctions pins the stamp read: an entity with no runtime block
// reads as no stamp rather than panicking, which is the case a plugin that
// declares no host functions produces.
func TestStampedHostFunctions(t *testing.T) {
	t.Parallel()
	if got := stampedHostFunctions(nil); got != nil {
		t.Errorf("stampedHostFunctions(nil) = %v; want nil", got)
	}
}

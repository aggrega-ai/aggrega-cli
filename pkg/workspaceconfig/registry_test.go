// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"errors"
	"testing"
)

// TestResolveRegistryAlias proves a token matching a configured entry's Name
// alias resolves to that entry's host.
func TestResolveRegistryAlias(t *testing.T) {
	t.Parallel()
	cfg := Config{Registries: []RegistryConfig{
		{Host: "hub.aggrega.ai", Name: "hub"},
		{Host: "dev.local:5000", Name: "dev"},
	}}
	got, err := cfg.ResolveRegistry("dev")
	if err != nil {
		t.Fatalf("ResolveRegistry: %v", err)
	}
	if got != "dev.local:5000" {
		t.Errorf("alias resolved to %q, want dev.local:5000", got)
	}
}

// TestResolveRegistryBareHostPassthrough proves a token that matches no alias is
// treated as a host and normalised through registryhost.Of — an unregistered
// host is a legitimate target and passes through unrejected.
func TestResolveRegistryBareHostPassthrough(t *testing.T) {
	t.Parallel()
	cfg := Config{Registries: []RegistryConfig{
		{Host: "hub.aggrega.ai", Name: "hub"},
	}}
	cases := map[string]string{
		"unregistered.example.com":      "unregistered.example.com",
		"registry.example.com:5000":     "registry.example.com:5000",
		"oci://registry.example.com/x":  "registry.example.com",
		"https://user:pw@h.io/charts/x": "h.io",
		"hub.aggrega.ai":                "hub.aggrega.ai",
	}
	for token, want := range cases {
		got, err := cfg.ResolveRegistry(token)
		if err != nil {
			t.Fatalf("ResolveRegistry(%q): %v", token, err)
		}
		if got != want {
			t.Errorf("ResolveRegistry(%q) = %q, want %q", token, got, want)
		}
	}
}

// TestResolveRegistryEmptyConfig proves resolution against a config with no
// registries always passes the token through as a host.
func TestResolveRegistryEmptyConfig(t *testing.T) {
	t.Parallel()
	cfg := Default()
	got, err := cfg.ResolveRegistry("anything.example.com")
	if err != nil {
		t.Fatalf("ResolveRegistry: %v", err)
	}
	if got != "anything.example.com" {
		t.Errorf("got %q, want anything.example.com", got)
	}
}

// TestResolveRegistryAmbiguousAlias proves a token matching the alias of two
// different hosts halts loudly rather than silently choosing one. A hand-edited
// config can carry a duplicate alias even though `registry add` refuses one.
func TestResolveRegistryAmbiguousAlias(t *testing.T) {
	t.Parallel()
	cfg := Config{Registries: []RegistryConfig{
		{Host: "a.example.com", Name: "shared"},
		{Host: "b.example.com", Name: "shared"},
	}}
	_, err := cfg.ResolveRegistry("shared")
	if err == nil {
		t.Fatal("ambiguous alias should be rejected")
	}
	if !errors.Is(err, ErrAmbiguousRegistryAlias) {
		t.Errorf("error = %v, want ErrAmbiguousRegistryAlias", err)
	}
}

// TestResolveRegistryHostTokenPreferredOverNoMatch proves a token that equals a
// configured entry's HOST (not its alias) still passes through as that host —
// resolution keys on the alias, and a host token that matches no alias is a
// plain pass-through, which yields the same host.
func TestResolveRegistryHostToken(t *testing.T) {
	t.Parallel()
	cfg := Config{Registries: []RegistryConfig{
		{Host: "hub.aggrega.ai", Name: "hub"},
	}}
	got, err := cfg.ResolveRegistry("hub.aggrega.ai")
	if err != nil {
		t.Fatalf("ResolveRegistry: %v", err)
	}
	if got != "hub.aggrega.ai" {
		t.Errorf("got %q, want hub.aggrega.ai", got)
	}
}

// TestRegistryNameRoundTrips proves the alias field survives a write through
// UpsertRegistry and a reload through schema validation.
func TestRegistryNameRoundTrips(t *testing.T) {
	t.Parallel()
	path := writeFixture(t, "workspace: {}\n")
	if err := UpsertRegistry(path, RegistryConfig{Host: "dev.local:5000", Name: "dev"}); err != nil {
		t.Fatalf("UpsertRegistry: %v", err)
	}
	cfg, err := load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	host, err := cfg.ResolveRegistry("dev")
	if err != nil {
		t.Fatalf("ResolveRegistry: %v", err)
	}
	if host != "dev.local:5000" {
		t.Errorf("reloaded alias resolved to %q, want dev.local:5000", host)
	}
}

// TestUpsertRegistryDuplicateAliasDifferentHost proves registering an alias
// already bound to a different host halts loudly.
func TestUpsertRegistryDuplicateAliasDifferentHost(t *testing.T) {
	t.Parallel()
	path := writeFixture(t, "workspace: {}\n")
	if err := UpsertRegistry(path, RegistryConfig{Host: "a.example.com", Name: "shared"}); err != nil {
		t.Fatalf("first UpsertRegistry: %v", err)
	}
	err := UpsertRegistry(path, RegistryConfig{Host: "b.example.com", Name: "shared"})
	if err == nil {
		t.Fatal("duplicate alias on a different host should be rejected")
	}
	if !errors.Is(err, ErrDuplicateRegistryAlias) {
		t.Errorf("error = %v, want ErrDuplicateRegistryAlias", err)
	}
}

// TestUpsertRegistrySameHostUpdatesAlias proves re-adding the same host with a
// new alias updates that host's alias in place rather than treating it as a
// duplicate — upsert semantics on the host-keyed entry.
func TestUpsertRegistrySameHostUpdatesAlias(t *testing.T) {
	t.Parallel()
	path := writeFixture(t, "workspace: {}\n")
	if err := UpsertRegistry(path, RegistryConfig{Host: "dev.local:5000", Name: "dev"}); err != nil {
		t.Fatalf("first UpsertRegistry: %v", err)
	}
	if err := UpsertRegistry(path, RegistryConfig{Host: "dev.local:5000", Name: "staging"}); err != nil {
		t.Fatalf("second UpsertRegistry: %v", err)
	}
	cfg, err := load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(cfg.Registries) != 1 {
		t.Fatalf("expected 1 registry after re-add, got %d: %+v", len(cfg.Registries), cfg.Registries)
	}
	host, err := cfg.ResolveRegistry("staging")
	if err != nil {
		t.Fatalf("ResolveRegistry: %v", err)
	}
	if host != "dev.local:5000" {
		t.Errorf("updated alias resolved to %q, want dev.local:5000", host)
	}
	// The old alias no longer resolves to a configured entry; it passes through
	// as a bare host token.
	if got, _ := cfg.ResolveRegistry("dev"); got != "dev" {
		t.Errorf("stale alias should pass through as a host, got %q", got)
	}
}

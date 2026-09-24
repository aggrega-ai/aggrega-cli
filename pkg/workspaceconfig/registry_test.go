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
	if !errors.Is(err, ErrAmbiguousRegistryAlias()) {
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

// TestIsConfiguredRegistry pins which hosts a configuration admits: the host its
// default registry names and the host of every entry it lists, compared by host
// authority on both sides, and nothing else.
//
// Each row carries one configuration shape and a host that shape must admit or
// refuse, so a regression names the shape it broke. The refusing rows are the
// ones a lax comparison would pass: a host listed nowhere, the same host on
// another port, the path-bearing default's own repository path, the alias
// itself, and a differently cased spelling. An alias default naming two entries
// needs a configuration of its own, so it is asserted after the table.
func TestIsConfiguredRegistry(t *testing.T) {
	t.Parallel()
	listed := []RegistryConfig{
		{Host: "registry.example.com"},
		{Host: "127.0.0.1:5000", Name: "local"},
	}
	cases := []struct {
		name string
		cfg  Config
		host string
		want bool
	}{
		{"a never-initialised configuration admits the hub", Default(), "hub.aggrega.ai", true},
		{"a never-initialised configuration refuses any other host", Default(), "registry.example.com", false},
		{"the default registry's host is admitted", Config{DefaultRegistry: "ghcr.io"}, "ghcr.io", true},
		{"a host listed only under registries is admitted", Config{DefaultRegistry: "hub.aggrega.ai", Registries: listed}, "registry.example.com", true},
		{"a host listed nowhere is refused", Config{DefaultRegistry: "hub.aggrega.ai", Registries: listed}, "unlisted.example.com", false},
		{"a listed host with its port is admitted", Config{Registries: listed}, "127.0.0.1:5000", true},
		{"a listed host on another port is refused", Config{Registries: listed}, "127.0.0.1:5001", false},
		{"a listed host without its port is refused", Config{Registries: listed}, "127.0.0.1", false},
		{"a path-bearing default admits its host", Config{DefaultRegistry: "ghcr.io/acme/catalog"}, "ghcr.io", true},
		{"a path-bearing default admits nothing named after its path", Config{DefaultRegistry: "ghcr.io/acme/catalog"}, "acme", false},
		{"an alias default admits the host its entry names", Config{DefaultRegistry: "local", Registries: listed}, "127.0.0.1:5000", true},
		{"an alias default admits nothing named after the alias", Config{DefaultRegistry: "private", Registries: []RegistryConfig{{Host: "private.example.com:8443", Name: "private"}}}, "private", false},
		{"an alias default reaching an unlisted host is resolved first", Config{DefaultRegistry: "private", Registries: []RegistryConfig{{Host: "private.example.com:8443", Name: "private"}}}, "private.example.com:8443", true},
		{"a reference is reduced to its host before it is compared", Config{DefaultRegistry: "ghcr.io"}, "oci://ghcr.io/acme/api:1.0.0", true},
		{"userinfo does not carry an unlisted host in", Config{DefaultRegistry: "ghcr.io"}, "ghcr.io@unlisted.example.com", false},
		{"a differently cased spelling is refused", Config{DefaultRegistry: "ghcr.io"}, "GHCR.io", false},
		{"an empty host is refused", Default(), "", false},
		{"an empty configuration admits nothing", Config{}, "hub.aggrega.ai", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.cfg.IsConfiguredRegistry(tc.host); got != tc.want {
				t.Errorf("IsConfiguredRegistry(%q) = %v, want %v (default %q, registries %+v)",
					tc.host, got, tc.want, tc.cfg.DefaultRegistry, tc.cfg.Registries)
			}
		})
	}

	// The ambiguous-alias row is only a refusal of the default if the alias really
	// is ambiguous and the hosts it names are really listed; asserted here so the
	// row cannot pass for the wrong reason.
	ambiguous := Config{DefaultRegistry: "shared", Registries: []RegistryConfig{
		{Host: "a.example.com", Name: "shared"},
		{Host: "b.example.com", Name: "shared"},
	}}
	if _, err := ambiguous.ResolveRegistry(ambiguous.DefaultRegistry); !errors.Is(err, ErrAmbiguousRegistryAlias()) {
		t.Fatalf("the ambiguous-alias configuration does not resolve as ambiguous: %v", err)
	}
	for _, host := range []string{"a.example.com", "b.example.com"} {
		if !ambiguous.IsConfiguredRegistry(host) {
			t.Errorf("%s is a listed entry and was refused because the default's alias is ambiguous", host)
		}
	}
	if ambiguous.IsConfiguredRegistry("shared") {
		t.Error("the ambiguous alias itself was admitted as a host")
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
	if !errors.Is(err, ErrDuplicateRegistryAlias()) {
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

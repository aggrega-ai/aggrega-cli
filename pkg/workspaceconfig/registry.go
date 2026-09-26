// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
)

// ResolveRegistry resolves a non-empty registry token to a registry host. It is
// the single name-or-host resolution primitive every `--registry` consumer
// routes through, so an alias works everywhere a registry is named:
//
//   - A token matching a configured entry's Name alias resolves to that entry's
//     Host — the alias is a convenience selector on the same entry.
//   - Any other token is treated as a host and normalised through
//     registryhost.Of. An unregistered host is a legitimate push target and is
//     returned as-is (normalised), never rejected.
//   - A token matching the alias of two different entries is ambiguous and
//     returns a wrapped ErrAmbiguousRegistryAlias rather than silently choosing
//     one host over the other.
//
// The empty-token fallback (to DefaultRegistry) and the final "no destination
// registry" error stay at the call sites; ResolveRegistry always receives a
// non-empty token. Callers that pass DefaultRegistry through here get alias
// resolution for a default set as an alias.
func (c Config) ResolveRegistry(token string) (string, error) {
	var matched []string
	for _, r := range c.Registries {
		if r.Name != "" && r.Name == token {
			matched = append(matched, r.Host)
		}
	}
	switch len(matched) {
	case 0:
		return registryhost.Of(token), nil
	case 1:
		return matched[0], nil
	default:
		return "", fmt.Errorf("workspaceconfig: registry alias %q matches %d hosts (%v); address the registry by host or remove the duplicate alias: %w",
			token, len(matched), matched, ErrAmbiguousRegistryAlias())
	}
}

// IsConfiguredRegistry reports whether host is a registry this configuration is
// configured with: the host DefaultRegistry names, or the host of any entry
// under Registries. It is the one answer to that question, so a surface that
// admits a registry by the configuration asks it here rather than composing the
// set for itself.
//
// Both sides are compared by their host authority — the scheme, any path and any
// userinfo dropped, the port kept — because a login is stored per host and a
// registry is contacted per host. So a default carrying a repository path
// ("ghcr.io/acme") admits its host, and a reference or a URL passed as host is
// reduced to its host before it is compared. The host passed in must already be
// in the spelling a registry host is kept under — registryhost.Normalize leaves
// it unchanged — and a host spelled any other way is refused rather than guessed
// at; the configured side is read through registryhost.Normalize, the one
// reading every per-host lookup shares. A configuration the resolution accepted
// holds only hosts in that spelling, so the default and each entry name their
// host exactly as written; the shapes a resolved configuration cannot carry — a
// path, or an alias as the default — are still read here the way this states,
// for a configuration built in code.
//
// A default written as an alias is resolved to its entry's host first, through
// ResolveRegistry. A default whose alias names two entries resolves to no host
// and so admits nothing on its own; the hosts that alias names are still
// admitted, as the entries they are. A host that reduces to nothing is never
// admitted, and neither is an entry or a default that does.
func (c Config) IsConfiguredRegistry(host string) bool {
	want := registryhost.Of(host)
	if want == "" || registryhost.Normalize(want) != want {
		return false
	}
	if c.DefaultRegistry != "" {
		if resolved, err := c.ResolveRegistry(c.DefaultRegistry); err == nil && registryhost.Normalize(resolved) == want {
			return true
		}
	}
	for _, r := range c.Registries {
		if registryhost.Normalize(r.Host) == want {
			return true
		}
	}
	return false
}

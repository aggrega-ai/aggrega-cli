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
			token, len(matched), matched, ErrAmbiguousRegistryAlias)
	}
}

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
// resolution for a default set as an alias — which is what a configuration built
// in code carries. A resolved configuration's DefaultRegistry has already been
// through the same alias match, once, when the configuration resolved, so it
// names a host and a caller holding one uses it as the host it is.
func (c Config) ResolveRegistry(token string) (string, error) {
	host, named, err := c.aliasHost(token)
	if err != nil {
		return "", fmt.Errorf("workspaceconfig: %w", err)
	}
	if named {
		return host, nil
	}
	return registryhost.Of(token), nil
}

// aliasHost returns the Host of the one entry whose Name is token, and whether an
// entry is named that at all. It is the one alias match: ResolveRegistry reads a
// command-line token through it and the resolution reads the default registry
// through it, so the two cannot disagree about which entry an alias names or when
// an alias is ambiguous. A token named by two entries is ambiguous and reported as
// such, naming every host that holds it, rather than resolved to either.
func (c Config) aliasHost(token string) (host string, named bool, err error) {
	var matched []string
	for _, r := range c.Registries {
		if r.Name != "" && r.Name == token {
			matched = append(matched, r.Host)
		}
	}
	switch len(matched) {
	case 0:
		return "", false, nil
	case 1:
		return matched[0], true, nil
	default:
		return "", true, fmt.Errorf("registry alias %q matches %d hosts (%v); address the registry by host or remove the duplicate alias: %w",
			token, len(matched), matched, ErrAmbiguousRegistryAlias())
	}
}

// aliasAlternative is the other thing a default registry may name, in the words
// its refusal uses.
const aliasAlternative = "the name of an entry under registries"

// resolveDefaultRegistry replaces cfg.DefaultRegistry with the registry host it
// names, once, over the composed configuration: the Host of the one entry under
// Registries whose Name it is, or else the value itself, which must then be a
// registry host in the spelling its trust policy is kept under. An empty default
// names no default registry and is left empty.
//
// A matching entry's name wins over a host spelling, as it does for a
// command-line token, so a default spelled like a host that is also an entry's
// name stands for that entry's host. A default that two entries are named after
// is refused as ambiguous rather than resolved to either; one that is neither an
// entry's name nor a registry host — a single word no entry carries, a host with
// a repository path, a host in capitals — is refused, naming the host to write
// where the value names one. layers are the resolution's contributing layers,
// and lookup its environment; both are read only to say which layer set the
// value a refusal is about.
//
// It runs once per resolution, after every file and the environment, because
// the entry an alias names may be declared by any layer and the environment may
// set the default after every file has been read — so no single layer can judge
// the value. Every reader of the resolved configuration then reads the host this
// returned, and none of them resolves the default a second time.
func resolveDefaultRegistry(cfg *Config, layers []string, lookup func(string) string) error {
	value := cfg.DefaultRegistry
	if value == "" {
		return nil
	}
	host, named, err := cfg.aliasHost(value)
	if err != nil {
		return fmt.Errorf("workspaceconfig: %sdefault_registry: %w", defaultRegistryLayer(*cfg, layers, lookup), err)
	}
	if named {
		cfg.DefaultRegistry = host
		return nil
	}
	if err := registryhost.CheckOr("default_registry", value, aliasAlternative); err != nil {
		return fmt.Errorf("workspaceconfig: %s%v: %w", defaultRegistryLayer(*cfg, layers, lookup), err, ErrConfigInvalid())
	}
	return nil
}

// defaultRegistryLayer names the layer that set the default registry, followed by
// ": ", for the opening of a refusal — a configuration file's path, or the
// environment variable behind a "$" — or returns the empty string when the
// built-in default is what applies. It asks the per-value provenance report, the
// one answer to which layer determined a setting, rather than working it out a
// second way, and it is reached only on the path that refuses, so the files it
// reads again are read only when there is something to report.
func defaultRegistryLayer(cfg Config, layers []string, lookup func(string) string) string {
	rw := &ResolvedWorkspace{config: cfg, files: layers, lookup: lookup}
	origins, err := rw.ValueOrigins()
	if err != nil {
		return ""
	}
	for _, o := range origins {
		if o.Field != "default_registry" {
			continue
		}
		switch kind, name := o.Layer(); kind {
		case OriginConfigFile:
			return name + ": "
		case OriginEnvironment:
			return "$" + name + ": "
		}
	}
	return ""
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
// ResolveRegistry. The resolution has already done that for a resolved
// configuration, with the same alias match, so its default is a host here, and
// reading it through the match again cannot change what is admitted: a host no
// entry is named after reads as itself, and a host that came from an entry is
// admitted as that entry whatever the match makes of it. A default whose alias names
// two entries resolves to no host and so admits nothing on its own; the hosts
// that alias names are still admitted, as the entries they are. A host that
// reduces to nothing is never admitted, and neither is an entry or a default
// that does.
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

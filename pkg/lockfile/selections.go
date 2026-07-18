// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lockfile

import (
	"sort"

	"github.com/aggrega-ai/aggrega-cli/pkg/ociref"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// ProviderBindings builds the capability→plugin binding map from the lockfile's
// recorded provider selections. A build (or serve render) reads provider
// bindings from this map — the LOCKFILE — never the workspace config, so
// rendered output (and the reconstructed drift-verify closure) stays invariant
// to a plugins.providers change made after the pull; the change is adopted only
// on the next pull. A federated catalog with more than one plugin serving a
// capability is disambiguated by the pinned binding.
func ProviderBindings(lf *Lockfile) map[string]string {
	bindings := make(map[string]string, len(lf.Providers))
	for _, b := range lf.Providers {
		bindings[b.Capability] = b.Plugin
	}
	return bindings
}

// MutatorAttachments reconstructs the mutator-socket attachment list a build (or
// serve render) runs, from the lockfile's recorded attachments — in the pinned
// order. Callers override the workspace config's plugins.mutator with this list
// so the mutator set / order / export function that shapes output is read from
// the LOCKFILE, never live config; a plugins.mutator change made after the pull
// is adopted only on the next pull. Returns nil for an empty pinned set, so the
// override runs no mutator regardless of live config.
//
// Each attachment's declared inputs are reconstructed onto the ref's With: the
// settings object inline as pinned, and each pinned source file as a PinnedSource
// carrying its base name and content. The socket therefore reads settings and
// source content from the lockfile pin rather than re-reading live config or the
// host filesystem, so a post-pull change to either does not move build output.
func MutatorAttachments(lf *Lockfile) []workspaceconfig.PluginRef {
	if len(lf.Mutators) == 0 {
		return nil
	}
	refs := make([]workspaceconfig.PluginRef, 0, len(lf.Mutators))
	for _, m := range lf.Mutators {
		ref := workspaceconfig.PluginRef{URI: m.Plugin, Function: m.Function}
		if len(m.Settings) > 0 || len(m.Sources) > 0 {
			with := &workspaceconfig.PluginRefWith{Settings: m.Settings}
			for _, s := range m.Sources {
				with.PinnedSources = append(with.PinnedSources, workspaceconfig.PinnedSource{
					Path:    s.Path,
					Content: s.Content,
				})
			}
			ref.With = with
		}
		refs = append(refs, ref)
	}
	return refs
}

// WorkspaceSocketEntityURIs returns the lockfile ENTITY URIs of the output-shaping
// workspace-socket plugins — the provider-rendering plugins bound through the
// provider selections and the mutator plugins attached through the mutator
// selections. The selection of these sockets is a pull-time choice recorded ONLY
// in the lockfile, so a config-invariant build (and serve render) reconstructs
// the socket entity + .wasm axes from this set rather than from live config,
// keeping the drift-verify closure honest without re-reading workspace config.
//
// Each provider binding and mutator attachment names a plugin by its bare
// coordinate (<pluginType>/<name>); this collects every pinned entity whose
// coordinate matches — one per resolved version, so a coordinate pinned at more
// than one version contributes each. The result is sorted so two runs over the
// same lockfile produce an identical set.
func WorkspaceSocketEntityURIs(lf *Lockfile) []string {
	if lf == nil {
		return nil
	}
	coords := make(map[string]struct{}, len(lf.Providers)+len(lf.Mutators))
	for _, b := range lf.Providers {
		if b.Plugin != "" {
			coords[b.Plugin] = struct{}{}
		}
	}
	for _, m := range lf.Mutators {
		if m.Plugin != "" {
			coords[m.Plugin] = struct{}{}
		}
	}
	if len(coords) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	var uris []string
	for _, e := range lf.Entities {
		base, _, _ := ociref.SplitVersionTag(e.URI)
		if _, ok := coords[base]; !ok {
			continue
		}
		if _, dup := seen[e.URI]; dup {
			continue
		}
		seen[e.URI] = struct{}{}
		uris = append(uris, e.URI)
	}
	sort.Strings(uris)
	return uris
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import "fmt"

// BuildCriticalBinding identifies one build-critical configuration binding a
// resolved workspace depends on: the config field it comes from and a stable,
// human-readable identity for the specific binding within that field. It is the
// unit the reproducibility guardrail reports so a build can name exactly which
// binding is not carried by the workspace tree.
type BuildCriticalBinding struct {
	// Field is the configuration surface the binding comes from, in its
	// authored spelling: "plugins.providers", "plugins.mutator",
	// "plugins.policy", "plugins.linter", "plugins.importer",
	// "plugin_sidecar_dir", or "workspace.catalog_dirs".
	Field string
	// Identity is a stable label for the specific binding within Field — a
	// provider capability, a socket attachment's uri/capability, or a directory
	// path — suitable for naming the binding in an operator-facing message.
	Identity string
}

// socketField pairs a build-critical plugin-socket key with its resolved
// attachment list.
type socketField struct {
	Key  string
	Refs []PluginRef
}

// buildCriticalSocketFields enumerates the build-critical plugin-socket lists of
// cfg in a fixed order. It is the single home of which plugin sockets are
// build-critical, shared by the capture writer (which freezes them into the
// workspace tree) and the reproducibility guardrail (which diffs them across
// layers), so the two surfaces can never disagree on the set.
func buildCriticalSocketFields(cfg Config) []socketField {
	return []socketField{
		{"providers", cfg.Plugins.Providers},
		{"mutator", cfg.Plugins.Mutator},
		{"policy", cfg.Plugins.Policy},
		{"linter", cfg.Plugins.Linter},
		{"importer", cfg.Plugins.Importer},
	}
}

// GlobalOnlyBuildCritical reports the build-critical bindings the resolved
// workspace depends on that are supplied only by the global configuration layer
// and are absent from the workspace tree. It resolves the workspace a second
// time with the global file layer suppressed — reusing the exact inputs of the
// original resolution (the same start directory, workspace-root, and environment
// lookup) so only the global layer differs — then set-differences the
// build-critical bindings of the full resolution against the tree-only one over
// binding identity: a binding present in the tree (whatever layer also carries
// it) is never reported; only a binding the tree lacks is global-only.
//
// The environment overlay applies identically to both passes, so an
// environment-supplied build-critical value cancels out of the difference and is
// out of scope — the guardrail speaks only to the global config file.
//
// Result order is a fixed field order and, within each field, the resolved list
// order, so the report is deterministic for a given resolution. A global-only
// build-critical binding makes the build non-reproducible on a machine without
// that global file, which `aggrega config capture` remedies by freezing the
// effective bindings into the workspace tree.
func (r *ResolvedWorkspace) GlobalOnlyBuildCritical() ([]BuildCriticalBinding, error) {
	treeOnly, err := resolve(r.start, r.flagRoot, r.lookup, true)
	if err != nil {
		return nil, err
	}
	full := r.config
	tree := treeOnly.config

	var out []BuildCriticalBinding

	// Plugin sockets share one build-critical enumeration; the diff identity
	// differs by socket. A provider capability binds exactly one plugin (a
	// duplicate halts resolution), so provider bindings are capability-keyed;
	// the four cross-cutting sockets admit multiple attachments, so their
	// identity is the whole attachment (uri, capability, function).
	fullSockets := buildCriticalSocketFields(full)
	treeSockets := buildCriticalSocketFields(tree)
	for i, sf := range fullSockets {
		key := pluginAttachmentKey
		if sf.Key == "providers" {
			key = providerBindingKey
		}
		out = append(out, globalOnlyRefs("plugins."+sf.Key, sf.Refs, treeSockets[i].Refs, key)...)
	}

	// plugin_sidecar_dir is a scalar whole-replaced per layer. The global layer
	// sits beneath the tree, so the effective value can differ from the
	// tree-only value only when the tree does not set it and the global layer
	// does — i.e. the effective value is global-only.
	if full.PluginSidecarDir != tree.PluginSidecarDir {
		out = append(out, BuildCriticalBinding{Field: "plugin_sidecar_dir", Identity: full.PluginSidecarDir})
	}

	// catalog_dirs concatenate across layers; a directory in the full list whose
	// path is absent from the tree-only list came from the global layer.
	out = append(out, globalOnlyStrings("workspace.catalog_dirs", full.Workspace.CatalogDirs, tree.Workspace.CatalogDirs)...)

	return out, nil
}

// providerBindingKey is the capability-keyed identity of a provider binding.
func providerBindingKey(ref PluginRef) string { return ref.Capability }

// pluginAttachmentKey is the whole-attachment identity of a cross-cutting socket
// plugin: its uri, capability, and function together. The NUL separator keeps
// the three fields unambiguous so no combination collides with another.
func pluginAttachmentKey(ref PluginRef) string {
	return ref.URI + "\x00" + ref.Capability + "\x00" + ref.Function
}

// pluginRefLabel renders a plugin reference for an operator message, preferring
// the capability (with the uri in parentheses when both are set) and falling
// back to the uri for the cross-cutting sockets that carry no capability.
func pluginRefLabel(ref PluginRef) string {
	switch {
	case ref.Capability != "" && ref.URI != "":
		return fmt.Sprintf("capability %q (%s)", ref.Capability, ref.URI)
	case ref.Capability != "":
		return fmt.Sprintf("capability %q", ref.Capability)
	default:
		return fmt.Sprintf("uri %q", ref.URI)
	}
}

// globalOnlyRefs returns the entries of full whose identity (per key) is absent
// from tree, in full's order. The membership map is consulted strictly by key
// and is never ranged to produce output, so the result stays deterministic.
func globalOnlyRefs(field string, full, tree []PluginRef, key func(PluginRef) string) []BuildCriticalBinding {
	present := make(map[string]struct{}, len(tree))
	for _, ref := range tree {
		present[key(ref)] = struct{}{}
	}
	var out []BuildCriticalBinding
	for _, ref := range full {
		if _, ok := present[key(ref)]; ok {
			continue
		}
		out = append(out, BuildCriticalBinding{Field: field, Identity: pluginRefLabel(ref)})
	}
	return out
}

// globalOnlyStrings returns the entries of full absent from tree, in full's
// order. Like globalOnlyRefs the membership map is lookup-only.
func globalOnlyStrings(field string, full, tree []string) []BuildCriticalBinding {
	present := make(map[string]struct{}, len(tree))
	for _, s := range tree {
		present[s] = struct{}{}
	}
	var out []BuildCriticalBinding
	for _, s := range full {
		if _, ok := present[s]; ok {
			continue
		}
		out = append(out, BuildCriticalBinding{Field: field, Identity: s})
	}
	return out
}

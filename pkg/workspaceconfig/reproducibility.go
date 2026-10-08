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
	// "plugins.policy", "plugins.linter", "plugins.importer", or
	// "workspace.catalog_dirs".
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

// buildCriticalSocketFields enumerates the plugin-socket lists of cfg a build
// must carry for reproducible output, in the fixed socket order. Membership is
// sourced from the socket-tier classification rather than a second hardcoded
// list: by default only the byte-determining sockets — those whose plugin shapes
// the rendered output bytes — are included; allPlugins widens the set to every
// plugin socket, adding the verdict gates and the orthogonal import socket.
// Shared by the capture writer (which freezes the set into the workspace tree)
// and the reproducibility guardrail (which diffs it across layers), so the two
// surfaces can never disagree on the set.
func buildCriticalSocketFields(cfg Config, allPlugins bool) []socketField {
	var out []socketField
	for _, ps := range cfg.PluginSockets() {
		refs := ps.Refs
		if !allPlugins {
			// Membership is per-attachment: for the governance socket only the
			// byte-determining (label-transform) attachments are build-critical;
			// its verdict attachments shape no output and are excluded.
			refs = ps.RefsByTier(SocketTierMutating)
			if len(refs) == 0 {
				continue
			}
		}
		key := string(ps.Socket)
		if ps.Socket == SocketProvider {
			key = "providers"
		}
		out = append(out, socketField{Key: key, Refs: refs})
	}
	return out
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
//
// The socket scope follows the same axis capture uses: by default only the
// byte-determining sockets (whose plugin shapes the rendered output) are
// examined; allPlugins widens the scope to every plugin socket, including the
// verdict gates and the import socket that are meant to vary by environment.
func (r *ResolvedWorkspace) GlobalOnlyBuildCritical(allPlugins bool) ([]BuildCriticalBinding, error) {
	treeOnly, err := resolve(r.start, r.flagRoot, r.lookup, r.machinePlatform(), true, nil)
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
	fullSockets := buildCriticalSocketFields(full, allPlugins)
	treeSockets := buildCriticalSocketFields(tree, allPlugins)
	// Pair the two layers by socket key, not by position: a socket can be present
	// in the full enumeration yet absent from the tree-only one (the governance
	// socket is enumerated only when it carries a build-critical attachment, so a
	// binding present only in the global layer leaves the tree-only side without
	// that socket). A tree side missing the key contributes no refs, so the
	// global-only diff is the full side's whole attachment set.
	treeRefsByKey := make(map[string][]PluginRef, len(treeSockets))
	for _, sf := range treeSockets {
		treeRefsByKey[sf.Key] = sf.Refs
	}
	for _, sf := range fullSockets {
		key := pluginAttachmentKey
		if sf.Key == "providers" {
			key = providerBindingKey
		}
		out = append(out, globalOnlyRefs("plugins."+sf.Key, sf.Refs, treeRefsByKey[sf.Key], key)...)
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

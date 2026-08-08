// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/ociref"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"go.yaml.in/yaml/v3"
)

// errNoConfigChange is returned by a mutate closure to signal that the document
// is unchanged and must not be rewritten. mutateConfigDocument treats it as a
// successful no-op: the file is left byte-for-byte as it was. Removal writers use
// it so a query that matches nothing never rewrites — and never reformats — the
// config, letting the calling command fail loudly on a no-match instead.
var errNoConfigChange = errors.New("workspaceconfig: no config change")

// UpsertRegistry adds or replaces the per-registry trust policy for
// reg.Host in the workspace config file at path, preserving every other
// setting in the file, and writes the result back. A file that does not
// yet exist is created carrying a `workspace: {}` marker and the one
// registry entry. An existing entry for the same host is replaced in place
// (its list position is preserved); a new host is appended.
//
// The merge operates on the document's YAML node tree, rewriting only the
// `registries` key. Other keys — and their values — are carried through
// untouched, so unrelated settings are neither dropped nor re-defaulted.
// UpsertRegistry validates the merged document against the config schema
// before writing, so a malformed result never reaches disk.
//
// This is the single config-write entry point; the loader is otherwise
// read-only. Callers that register a registry's trust baseline (e.g.
// `aggrega registry add`) route through here so the write path, schema
// gate, and ordering discipline live in one place.
func UpsertRegistry(path string, reg RegistryConfig) error {
	if reg.Host == "" {
		return fmt.Errorf("workspaceconfig: UpsertRegistry: registry host is required")
	}
	return mutateConfigDocument(path, func(mapping *yaml.Node) error {
		registries := readRegistries(mapping)
		// A non-empty alias is unique across entries: registering one already
		// bound to a different host halts loudly rather than shadowing it.
		// Re-adding the SAME host updates that host's alias in place (upsert),
		// so it is not a conflict.
		if reg.Name != "" {
			for i := range registries {
				if registries[i].Host != reg.Host && registries[i].Name == reg.Name {
					return fmt.Errorf("workspaceconfig: registry alias %q is already used by host %q; choose a different alias or address that host directly: %w",
						reg.Name, registries[i].Host, ErrDuplicateRegistryAlias())
				}
			}
		}
		replaced := false
		for i := range registries {
			if registries[i].Host == reg.Host {
				registries[i] = reg
				replaced = true
				break
			}
		}
		if !replaced {
			registries = append(registries, reg)
		}

		var regNode yaml.Node
		if err := regNode.Encode(registries); err != nil {
			return fmt.Errorf("workspaceconfig: encode registries: %w", err)
		}
		setMappingKey(mapping, "registries", &regNode)
		return nil
	})
}

// mutateConfigDocument is the single config-file write scaffold every by-key
// writer routes through. It reads the workspace config at path (schema-validating
// the existing content before it parses, or seeding a fresh `workspace: {}`
// document when no file exists yet), hands the root mapping node to mutate for
// the caller's targeted node surgery, then validates the merged document against
// the config schema before writing it back — so a malformed result never reaches
// disk and the read-guard, seed, schema-gate, and ordered-write discipline live
// in one place rather than being re-derived per writer. A missing parent
// directory is created before the write, so seeding a fresh config at a path
// whose directory does not yet exist (the global config under a not-yet-created
// home config dir) succeeds.
func mutateConfigDocument(path string, mutate func(mapping *yaml.Node) error) error {
	var root yaml.Node
	data, rerr := os.ReadFile(path)
	switch {
	case rerr == nil:
		if verr := validateBytes(data, path); verr != nil {
			return verr
		}
		if uerr := yaml.Unmarshal(data, &root); uerr != nil {
			return fmt.Errorf("workspaceconfig: parse existing %s: %w", path, uerr)
		}
	case os.IsNotExist(rerr):
		// A fresh file: a document whose root mapping carries a
		// `workspace: {}` marker so discovery anchors here.
		if err := root.Encode(map[string]any{"workspace": map[string]any{}}); err != nil {
			return fmt.Errorf("workspaceconfig: seed fresh config: %w", err)
		}
	default:
		return fmt.Errorf("workspaceconfig: read %s: %w", path, rerr)
	}

	mapping := rootMapping(&root)
	if mapping == nil {
		return fmt.Errorf("workspaceconfig: %s: top-level document is not a mapping", path)
	}

	if err := mutate(mapping); err != nil {
		if errors.Is(err, errNoConfigChange) {
			return nil
		}
		return err
	}

	out, merr := yaml.Marshal(&root)
	if merr != nil {
		return fmt.Errorf("workspaceconfig: marshal %s: %w", path, merr)
	}
	if verr := validateBytes(out, path); verr != nil {
		return fmt.Errorf("workspaceconfig: merged config failed validation: %w", verr)
	}
	if derr := os.MkdirAll(filepath.Dir(path), 0o755); derr != nil {
		return fmt.Errorf("workspaceconfig: create config dir for %s: %w", path, derr)
	}
	if werr := os.WriteFile(path, out, 0o644); werr != nil {
		return fmt.Errorf("workspaceconfig: write %s: %w", path, werr)
	}
	return nil
}

// UpsertProviderBinding adds or updates the `plugins.providers` entry for
// target in the workspace config file at path, preserving every other setting,
// and writes the result back. A file that does not yet exist is created
// carrying a `workspace: {}` marker and the one binding. An existing entry for
// the same target has its plugin URI updated in place (its list position and
// any other members are preserved); a new target is appended.
//
// Like UpsertRegistry it operates on the document's YAML node tree, rewriting
// only the `plugins.providers` list (creating the `plugins` block if absent),
// and validates the merged document against the config schema before writing,
// so a malformed result never reaches disk. This is the write path the
// interactive pull binding-helper routes through, keeping the config-write,
// schema-gate, and node-surgery discipline in one place.
func UpsertProviderBinding(path, target, plugin string) error {
	if target == "" || plugin == "" {
		return fmt.Errorf("workspaceconfig: UpsertProviderBinding: target and plugin are required")
	}
	return mutateConfigDocument(path, func(mapping *yaml.Node) error {
		pluginsNode := getOrCreateMapping(mapping, "plugins")
		providers := UpsertProviderRef(readProviderBindings(pluginsNode), target, plugin)

		var provNode yaml.Node
		if err := provNode.Encode(providers); err != nil {
			return fmt.Errorf("workspaceconfig: encode providers: %w", err)
		}
		setMappingKey(pluginsNode, "providers", &provNode)
		return nil
	})
}

// UpsertPluginSocketRef adds the config-socket plugin attachment uri to the
// `plugins.<socket>` list in the workspace config file at path, preserving every
// other setting, and writes the result back. socket is the config-socket key
// (mutator, policy, linter, importer); uri is the bare plugin entity identity
// (`<socket>/<name>:<version>`). An entry whose URI already matches is left in
// place — the add is idempotent and never clobbers an existing attachment's
// other members; a new URI is appended. A file that does not yet exist is
// created carrying a `workspace: {}` marker and the one attachment.
//
// Like UpsertProviderBinding it routes through mutateConfigDocument, rewriting
// only the `plugins.<socket>` list (creating the `plugins` block if absent) and
// validating the merged document against the config schema before writing. It is
// the single config-socket attachment write path, so the node-surgery and
// schema-gate discipline stay unified with the provider-binding writer. A socket
// key the schema does not admit is rejected by that merged-document validation
// rather than silently written.
func UpsertPluginSocketRef(path, socket, uri string, role plugintype.Role) error {
	if socket == "" || uri == "" {
		return fmt.Errorf("workspaceconfig: UpsertPluginSocketRef: socket and uri are required")
	}
	return mutateConfigDocument(path, func(mapping *yaml.Node) error {
		pluginsNode := getOrCreateMapping(mapping, "plugins")
		refs := upsertPluginRefURI(readPluginRefs(pluginsNode, socket), uri, role)

		var refsNode yaml.Node
		if err := refsNode.Encode(refs); err != nil {
			return fmt.Errorf("workspaceconfig: encode %s refs: %w", socket, err)
		}
		setMappingKey(pluginsNode, socket, &refsNode)
		return nil
	})
}

// UpsertCLIPluginRef adds or re-pins the CLI-extension plugin identified by uri
// in the `plugins.cli` list in the workspace config file at path, preserving
// every other setting, and writes the result back. A plugin holds one entry:
// an existing entry naming the same plugin — matched by version-less identity,
// so a different version of the same plugin is the same entry — has its
// {uri, digest} replaced in place, its list position preserved, so re-adding a
// plugin at any version re-pins its single entry to the newly resolved
// reference and bytes; a reference for a plugin not yet listed is appended. A
// file that does not yet exist is created carrying a `workspace: {}` marker and
// the one entry.
//
// Like UpsertPluginSocketRef it routes through mutateConfigDocument, rewriting
// only the `plugins.cli` list (creating the `plugins` block if absent) and
// validating the merged document against the config schema before writing, so
// the node-surgery and schema-gate discipline stay unified with the other
// by-key config writers.
func UpsertCLIPluginRef(path, uri, digest string) error {
	if uri == "" || digest == "" {
		return fmt.Errorf("workspaceconfig: UpsertCLIPluginRef: uri and digest are required")
	}
	return mutateConfigDocument(path, func(mapping *yaml.Node) error {
		pluginsNode := getOrCreateMapping(mapping, "plugins")
		refs := upsertCLIRef(readCLIRefs(pluginsNode), uri, digest)

		var refsNode yaml.Node
		if err := refsNode.Encode(refs); err != nil {
			return fmt.Errorf("workspaceconfig: encode cli refs: %w", err)
		}
		setMappingKey(pluginsNode, "cli", &refsNode)
		return nil
	})
}

// upsertCLIRef pins uri and digest in refs, matching an existing entry by the
// plugin's version-less identity: two references naming the same plugin at
// different versions share one entry. On a match it replaces that entry's
// {uri, digest} in place (preserving its list position), so re-adding any
// version of the same plugin re-pins the single entry to the newly resolved
// reference and bytes. When no entry shares the version-less identity a new
// {uri, digest} entry is appended. A version-less or local reference has a
// base equal to the whole string, so it matches only itself.
func upsertCLIRef(refs []CLIPluginRef, uri, digest string) []CLIPluginRef {
	incomingBase := ociref.VersionlessBase(uri)
	for i := range refs {
		if ociref.VersionlessBase(refs[i].URI) == incomingBase {
			refs[i].URI = uri
			refs[i].Digest = digest
			return refs
		}
	}
	return append(refs, CLIPluginRef{URI: uri, Digest: digest})
}

// readCLIRefs decodes the existing `cli` list from a `plugins` mapping node, or
// returns nil when the node is absent or carries no entries. It is the
// CLIPluginRef peer of readPluginRefs.
func readCLIRefs(pluginsNode *yaml.Node) []CLIPluginRef {
	if pluginsNode == nil || pluginsNode.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(pluginsNode.Content); i += 2 {
		if pluginsNode.Content[i].Value == "cli" {
			var refs []CLIPluginRef
			_ = pluginsNode.Content[i+1].Decode(&refs)
			return refs
		}
	}
	return nil
}

// RemoveCLIPluginRef removes the `plugins.cli` entry matching query from the
// workspace config file at path, preserving every other setting, and writes the
// result back. It reports whether an entry matched: false means the query hit
// nothing (or no config file exists yet) and the file is left untouched, so the
// calling command can fail loudly instead of rewriting an unchanged config. When
// the removal empties the list its `cli` key is dropped rather than left as an
// empty list.
//
// An entry matches query by an exact URI match or by the plugin name derived
// from its URI (the final path segment with any :version suffix removed), so a
// plugin added by its short name is removed by that same name. Like the Upsert
// writers it routes through mutateConfigDocument, so the node-surgery and
// schema-gate discipline stay unified across the by-key writers.
func RemoveCLIPluginRef(path, query string) (bool, error) {
	if query == "" {
		return false, fmt.Errorf("workspaceconfig: RemoveCLIPluginRef: query is required")
	}
	removed := false
	err := mutateConfigDocument(path, func(mapping *yaml.Node) error {
		pluginsNode := findMapping(mapping, "plugins")
		if pluginsNode == nil {
			return errNoConfigChange
		}
		kept, dropped := removeCLIRef(readCLIRefs(pluginsNode), query)
		if !dropped {
			return errNoConfigChange
		}
		removed = true
		return writeOrDeleteRefs(pluginsNode, "cli", kept)
	})
	if err != nil {
		return false, err
	}
	return removed, nil
}

// RemovePluginSocketRef removes the `plugins.<socket>` entry matching query from
// the workspace config file at path, preserving every other setting, and writes
// the result back. socket is the config-socket key (mutator, policy, linter,
// importer). It reports whether an entry matched: false leaves the file
// untouched so the calling command can fail loudly. When the removal empties the
// list its `<socket>` key is dropped rather than left as an empty list.
//
// An entry matches query by an exact URI match, by the plugin name derived from
// its URI, or by its bound capability, so a socket attachment added by its short
// name is removed by that same name. Like UpsertPluginSocketRef it routes through
// mutateConfigDocument, keeping the node-surgery and schema-gate discipline
// unified with the other by-key config writers.
func RemovePluginSocketRef(path, socket, query string) (bool, error) {
	if socket == "" || query == "" {
		return false, fmt.Errorf("workspaceconfig: RemovePluginSocketRef: socket and query are required")
	}
	removed := false
	err := mutateConfigDocument(path, func(mapping *yaml.Node) error {
		pluginsNode := findMapping(mapping, "plugins")
		if pluginsNode == nil {
			return errNoConfigChange
		}
		kept, dropped := removePluginRef(readPluginRefs(pluginsNode, socket), query)
		if !dropped {
			return errNoConfigChange
		}
		removed = true
		return writeOrDeleteRefs(pluginsNode, socket, kept)
	})
	if err != nil {
		return false, err
	}
	return removed, nil
}

// RemoveProviderBinding removes the `plugins.providers` entry matching query from
// the workspace config file at path, preserving every other setting, and writes
// the result back. It reports whether an entry matched: false leaves the file
// untouched so the calling command can fail loudly. When the removal empties the
// list its `providers` key is dropped rather than left as an empty list.
//
// A provider binding is keyed by the capability it serves, so query matches by
// that capability first; an exact URI match or the plugin name derived from the
// URI also matches. Like UpsertProviderBinding it routes through
// mutateConfigDocument, keeping the config-write, schema-gate, and node-surgery
// discipline in one place.
func RemoveProviderBinding(path, query string) (bool, error) {
	if query == "" {
		return false, fmt.Errorf("workspaceconfig: RemoveProviderBinding: query is required")
	}
	removed := false
	err := mutateConfigDocument(path, func(mapping *yaml.Node) error {
		pluginsNode := findMapping(mapping, "plugins")
		if pluginsNode == nil {
			return errNoConfigChange
		}
		kept, dropped := removePluginRef(readProviderBindings(pluginsNode), query)
		if !dropped {
			return errNoConfigChange
		}
		removed = true
		return writeOrDeleteRefs(pluginsNode, "providers", kept)
	})
	if err != nil {
		return false, err
	}
	return removed, nil
}

// RemoveRegistry removes the `registries` entry addressed by token from the
// workspace config file at path, preserving every other setting, and writes the
// result back. It reports whether an entry matched: false leaves the file
// untouched (a query that matches nothing, or no config file yet) so the calling
// command can fail loudly. When the removal empties the list its `registries` key
// is dropped rather than left as an empty list.
//
// token is resolved through Config.ResolveRegistry — the single alias-or-host
// primitive every registry consumer routes through — against this file's own
// registries: a token matching an entry's Name alias resolves to that entry's
// host, and any other token is treated as a host. The entry whose Host equals the
// resolved host is dropped, so a registry added by its friendly alias is removed
// by that same alias, symmetric with add. An ambiguous alias (two entries) surfaces
// its resolver error rather than removing one arbitrarily.
//
// The default_registry pointer is a separate setting and is deliberately left
// untouched: an absent entry simply reverts that host to default trust, and the
// resolver's host passthrough keeps it a valid target. Like the other by-key
// removers it routes through mutateConfigDocument, keeping the config-write,
// schema-gate, and node-surgery discipline in one place.
func RemoveRegistry(path, token string) (bool, error) {
	if token == "" {
		return false, fmt.Errorf("workspaceconfig: RemoveRegistry: token is required")
	}
	removed := false
	err := mutateConfigDocument(path, func(mapping *yaml.Node) error {
		registries := readRegistries(mapping)
		if len(registries) == 0 {
			return errNoConfigChange
		}
		host, rerr := Config{Registries: registries}.ResolveRegistry(token)
		if rerr != nil {
			return rerr
		}
		kept := make([]RegistryConfig, 0, len(registries))
		for _, r := range registries {
			if r.Host == host {
				removed = true
				continue
			}
			kept = append(kept, r)
		}
		if !removed {
			return errNoConfigChange
		}
		return writeOrDeleteRefs(mapping, "registries", kept)
	})
	if err != nil {
		return false, err
	}
	return removed, nil
}

// removeCLIRef returns refs without every entry matching query — an exact URI
// match or a match on the plugin name derived from the URI — and reports whether
// any entry was dropped. Surviving entries keep their position and members.
func removeCLIRef(refs []CLIPluginRef, query string) ([]CLIPluginRef, bool) {
	kept := make([]CLIPluginRef, 0, len(refs))
	dropped := false
	for _, r := range refs {
		if uriMatchesQuery(r.URI, query) {
			dropped = true
			continue
		}
		kept = append(kept, r)
	}
	return kept, dropped
}

// removePluginRef returns refs without every entry matching query and reports
// whether any entry was dropped. A socket attachment or provider binding matches
// by its bound capability, an exact URI match, or the plugin name derived from
// its URI. Surviving entries keep their position and members.
func removePluginRef(refs []PluginRef, query string) ([]PluginRef, bool) {
	kept := make([]PluginRef, 0, len(refs))
	dropped := false
	for _, r := range refs {
		if r.Capability == query || uriMatchesQuery(r.URI, query) {
			dropped = true
			continue
		}
		kept = append(kept, r)
	}
	return kept, dropped
}

// uriMatchesQuery reports whether a plugin reference carrying uri matches query:
// an exact URI match, or a match on the plugin name derived from the URI. It is
// the single identity-match rule the removal writers share, so a plugin added by
// its short name and one addressed by its full pinned URI are both removable.
func uriMatchesQuery(uri, query string) bool {
	if uri == "" {
		return false
	}
	return uri == query || pluginNameFromURI(uri) == query
}

// pluginNameFromURI derives a plugin's short name from a reference URI: the final
// path segment with any :version suffix removed. For
// oci://hub.aggrega.ai/plugin/cli/greet:1.2.0 it is "greet"; for the bare entity
// identity mutator/tag-injector:1.2.0 it is "tag-injector". This is the same
// short name `plugin add <type> <query>` resolves against, so remove is symmetric
// with add.
func pluginNameFromURI(uri string) string {
	s := uri
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.IndexByte(s, ':'); i >= 0 {
		s = s[:i]
	}
	return s
}

// writeOrDeleteRefs sets key in pluginsNode to the encoded refs, or removes key
// entirely when refs is empty so a fully-drained socket list leaves no empty-list
// residue behind. It is the shared terminal step of the removal writers.
func writeOrDeleteRefs[T any](pluginsNode *yaml.Node, key string, refs []T) error {
	if len(refs) == 0 {
		deleteMappingKey(pluginsNode, key)
		return nil
	}
	return setEncodedKey(pluginsNode, key, refs)
}

// CaptureBuildCritical writes the effective build-critical configuration subset
// of cfg into the workspace config file at path, preserving every other setting,
// and writes the result back. The build-critical subset is the configuration a
// build participates in: a set of plugin sockets and the catalog directories
// (workspace.catalog_dirs).
// The socket set follows the byte-determining axis: by default only the sockets
// whose plugin shapes the rendered output — the provider bindings
// (plugins.providers) and the mutator (plugins.mutator) — are frozen, since only
// those must travel with the project for byte-reproducible output; allPlugins
// widens the set to every plugin socket, adding the policy and linter verdict
// gates and the importer. Machine-wide tooling settings — the registries trust
// policy above all — are deliberately excluded: they stay in the global config
// by their nature and are not part of a project's build.
//
// This is the write path that freezes a workspace's build-participating
// configuration so the tree is self-contained: after capture, resolving the
// tree alone (no global layer beneath it) yields the same build-critical
// bindings that resolved with the global layer present. Each field is written to
// its effective value verbatim — the value is already the cascade-merged,
// path-resolved result — so a plugin socket attachment's function override and
// attachment-time inputs (its `with` block) are preserved, not flattened to a
// bare reference. Only a non-empty field is written; an empty one contributes
// nothing and is left absent rather than emitted as an empty list.
//
// Re-running capture on an unchanged effective configuration rewrites the file
// byte-for-byte: the output is a pure function of the effective values, in a
// fixed field order, encoded through the same ordered node-surgery every other
// writer uses. Like every config writer it routes through mutateConfigDocument,
// so the schema gate, seed-fresh, and merged-document validation are shared
// rather than re-derived here.
func CaptureBuildCritical(path string, cfg Config, allPlugins bool) error {
	return mutateConfigDocument(path, func(mapping *yaml.Node) error {
		var pluginsNode *yaml.Node
		for _, s := range buildCriticalSocketFields(cfg, allPlugins) {
			if len(s.Refs) == 0 {
				continue
			}
			if pluginsNode == nil {
				pluginsNode = getOrCreateMapping(mapping, "plugins")
			}
			if err := setEncodedKey(pluginsNode, s.Key, s.Refs); err != nil {
				return err
			}
		}

		if len(cfg.Workspace.CatalogDirs) > 0 {
			wsNode := getOrCreateMapping(mapping, "workspace")
			if err := setEncodedKey(wsNode, "catalog_dirs", cfg.Workspace.CatalogDirs); err != nil {
				return err
			}
		}
		return nil
	})
}

// setEncodedKey encodes v into a fresh YAML node and sets it under key in
// parent, replacing an existing value node in place or appending the key/value
// pair when key is absent. It is the shared encode-then-set primitive the
// whole-field writers use, so the encode-error wrapping and node placement live
// in one spot rather than being repeated per field.
func setEncodedKey(parent *yaml.Node, key string, v any) error {
	var node yaml.Node
	if err := node.Encode(v); err != nil {
		return fmt.Errorf("workspaceconfig: encode %s: %w", key, err)
	}
	setMappingKey(parent, key, &node)
	return nil
}

// upsertPluginRefURI appends a bare-URI PluginRef for uri when no existing entry
// in refs carries it, or returns refs unchanged when one already does. The
// idempotent no-clobber semantic mirrors UpsertProviderRef: re-adding the same
// attachment neither duplicates it nor disturbs an entry's other members.
func upsertPluginRefURI(refs []PluginRef, uri string, role plugintype.Role) []PluginRef {
	for i := range refs {
		if refs[i].URI == uri {
			return refs
		}
	}
	return append(refs, PluginRef{URI: uri, Role: role})
}

// readPluginRefs decodes the existing `<socket>` list from a `plugins` mapping
// node, or returns nil when the node is absent or carries no entries for socket.
// It is the PluginRef peer of readProviderBindings.
func readPluginRefs(pluginsNode *yaml.Node, socket string) []PluginRef {
	if pluginsNode == nil || pluginsNode.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(pluginsNode.Content); i += 2 {
		if pluginsNode.Content[i].Value == socket {
			var refs []PluginRef
			_ = pluginsNode.Content[i+1].Decode(&refs)
			return refs
		}
	}
	return nil
}

// UpsertProviderRef sets the plugin URI for target in refs, updating an
// existing entry in place (preserving its list position and any other members)
// or appending a new {target, uri} entry when target is absent. It is the
// single by-target upsert primitive shared by the config-file writer and the
// interactive pull binding-helper's in-memory update, so both apply the same
// replace-or-append semantic.
func UpsertProviderRef(refs []PluginRef, target, uri string) []PluginRef {
	for i := range refs {
		if refs[i].Capability == target {
			refs[i].URI = uri
			return refs
		}
	}
	return append(refs, PluginRef{Capability: target, URI: uri})
}

// readProviderBindings decodes the existing `providers` list from a `plugins`
// mapping node, or returns nil when the node is absent or carries no providers.
func readProviderBindings(pluginsNode *yaml.Node) []PluginRef {
	if pluginsNode == nil || pluginsNode.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(pluginsNode.Content); i += 2 {
		if pluginsNode.Content[i].Value == "providers" {
			var refs []PluginRef
			_ = pluginsNode.Content[i+1].Decode(&refs)
			return refs
		}
	}
	return nil
}

// getOrCreateMapping returns the mapping node bound to key in parent, creating
// and attaching an empty mapping when the key is absent. The returned node is
// live in parent's content tree, so mutating it (e.g. via setMappingKey)
// rewrites parent in place.
func getOrCreateMapping(parent *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			return parent.Content[i+1]
		}
	}
	node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	parent.Content = append(parent.Content, keyNode, node)
	return node
}

// findMapping returns the mapping node bound to key in parent, or nil when the
// key is absent or its value is not a mapping. It is the read-only peer of
// getOrCreateMapping: a removal must not materialize an empty block for a key
// that was not present.
func findMapping(parent *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(parent.Content); i += 2 {
		if parent.Content[i].Value == key {
			if parent.Content[i+1].Kind == yaml.MappingNode {
				return parent.Content[i+1]
			}
			return nil
		}
	}
	return nil
}

// deleteMappingKey removes the key/value pair for key from mapping, or leaves
// mapping unchanged when key is absent.
func deleteMappingKey(mapping *yaml.Node, key string) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
			return
		}
	}
}

// rootMapping returns the mapping node at the root of a parsed document,
// unwrapping the document node yaml.Unmarshal produces. Returns nil when
// the root is not a mapping.
func rootMapping(root *yaml.Node) *yaml.Node {
	n := root
	if n.Kind == yaml.DocumentNode {
		if len(n.Content) == 0 {
			return nil
		}
		n = n.Content[0]
	}
	if n.Kind != yaml.MappingNode {
		return nil
	}
	return n
}

// readRegistries decodes the existing `registries` value from a mapping
// node into typed RegistryConfig values, or returns nil when the key is
// absent.
func readRegistries(mapping *yaml.Node) []RegistryConfig {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == "registries" {
			var regs []RegistryConfig
			_ = mapping.Content[i+1].Decode(&regs)
			return regs
		}
	}
	return nil
}

// setMappingKey replaces the value node for key in mapping, or appends the
// key/value pair when key is absent.
func setMappingKey(mapping *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1] = value
			return
		}
	}
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
	mapping.Content = append(mapping.Content, keyNode, value)
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package workspaceconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/ociref"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
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
	return mutateConfigDocument(path, registryMutation(reg))
}

// RegistryDocument returns the bytes UpsertRegistry would write for reg at path,
// without writing anything and without creating a directory.
//
// It exists so a caller that reports a document instead of writing one is not
// composing its own version of the file. It routes through the same mutation and
// the same composition UpsertRegistry does, so the two cannot produce different
// bytes for the same inputs — including the schema gate, which runs here too: a
// document reported to a caller has passed the same validation a written one
// would, so no caller is handed content the loader would later reject.
//
// A registry entry that is already present and identical composes to the file's
// existing bytes, so the reported document is what the file would hold either
// way rather than an empty answer.
func RegistryDocument(path string, reg RegistryConfig) ([]byte, error) {
	if reg.Host == "" {
		return nil, fmt.Errorf("workspaceconfig: RegistryDocument: registry host is required")
	}
	out, _, err := composeConfigDocument(path, registryMutation(reg))
	return out, err
}

// registryMutation builds the node surgery that records reg in a config
// document's registries list: an existing entry for the same host is replaced in
// place so its list position is preserved, and a new host is appended.
//
// It is a factory rather than an inline closure so the composition and the write
// share one mutation instead of each carrying a copy that agrees today.
func registryMutation(reg RegistryConfig) func(mapping *yaml.Node) error {
	return func(mapping *yaml.Node) error {
		registries := readRegistries(mapping)
		// A non-empty alias is unique across entries: registering one already
		// bound to a different host halts loudly rather than shadowing it.
		// Re-adding the SAME host updates that host's alias in place (upsert),
		// so it is not a conflict.
		if reg.Name != "" {
			for i := range registries {
				if !registryhost.Same(registries[i].Host, reg.Host) && registries[i].Name == reg.Name {
					return fmt.Errorf("workspaceconfig: registry alias %q is already used by host %q; choose a different alias or address that host directly: %w",
						reg.Name, registries[i].Host, ErrDuplicateRegistryAlias())
				}
			}
		}
		replaced := false
		for i := range registries {
			if registryhost.Same(registries[i].Host, reg.Host) {
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
	}
}

// mutateConfigDocument is the single config-file write scaffold every by-key
// writer routes through. It composes the merged document — the read-guard, the
// fresh-file seed, the caller's targeted node surgery and the schema gate, all
// below in one place rather than re-derived per writer — and puts the result on
// disk, so a malformed result never reaches it. A missing parent directory is
// created before the write, so seeding a fresh config at a path whose directory
// does not yet exist (the global config under a not-yet-created home config dir)
// succeeds. A mutation that changed nothing writes nothing, leaving the file
// byte-for-byte as it was.
//
// It is the write half only. Everything that decides the bytes lives in the
// composition below, which a caller reporting a document instead of writing one
// reaches directly — so both deliveries are one computation.
func mutateConfigDocument(path string, mutate func(mapping *yaml.Node) error) error {
	out, changed, err := composeConfigDocument(path, mutate)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	if derr := os.MkdirAll(filepath.Dir(path), 0o755); derr != nil {
		return fmt.Errorf("workspaceconfig: create config dir for %s: %w", path, derr)
	}
	if werr := os.WriteFile(path, out, 0o644); werr != nil {
		return fmt.Errorf("workspaceconfig: write %s: %w", path, werr)
	}
	return nil
}

// composeConfigDocument is every part of mutateConfigDocument except putting the
// result on disk: it reads-or-seeds the document at path, hands the root mapping
// to mutate, marshals the result and schema-validates it, and returns the bytes
// together with whether the mutation changed anything.
//
// The split is what makes a reported document and a written one the same
// computation rather than two that agree. Composition has one implementation, so
// a caller asking what the write would produce and a caller performing the write
// pass through identical read-guard, seed, surgery, marshal and schema-gate
// steps; there is no second path that could drift.
//
// An unchanged document reports changed=false and returns the file's existing
// bytes, so a caller reporting the result still has the document's content and a
// caller writing it leaves the file byte-for-byte as it was.
func composeConfigDocument(path string, mutate func(mapping *yaml.Node) error) ([]byte, bool, error) {
	var root yaml.Node
	data, rerr := os.ReadFile(path)
	switch {
	case rerr == nil:
		if verr := validateBytes(data, path); verr != nil {
			return nil, false, verr
		}
		if uerr := yaml.Unmarshal(data, &root); uerr != nil {
			return nil, false, fmt.Errorf("workspaceconfig: parse existing %s: %w", path, uerr)
		}
	case os.IsNotExist(rerr):
		// A fresh file: a document whose root mapping carries a
		// `workspace: {}` marker so discovery anchors here.
		if err := root.Encode(map[string]any{"workspace": map[string]any{}}); err != nil {
			return nil, false, fmt.Errorf("workspaceconfig: seed fresh config: %w", err)
		}
	default:
		return nil, false, fmt.Errorf("workspaceconfig: read %s: %w", path, rerr)
	}

	mapping := rootMapping(&root)
	if mapping == nil {
		return nil, false, fmt.Errorf("workspaceconfig: %s: top-level document is not a mapping", path)
	}

	if err := mutate(mapping); err != nil {
		if errors.Is(err, errNoConfigChange) {
			return data, false, nil
		}
		return nil, false, err
	}

	out, merr := yaml.Marshal(&root)
	if merr != nil {
		return nil, false, fmt.Errorf("workspaceconfig: marshal %s: %w", path, merr)
	}
	if verr := validateBytes(out, path); verr != nil {
		return nil, false, fmt.Errorf("workspaceconfig: merged config failed validation: %w", verr)
	}
	return out, true, nil
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
	return mutateConfigDocument(path, providerBindingMutation(target, plugin))
}

// ProviderBindingDocument returns the bytes UpsertProviderBinding would write for
// target and plugin at path, without writing anything and without creating a
// directory.
//
// It is the RegistryDocument of the provider socket and exists for the same
// reason: a caller that reports a document instead of writing one must not
// compose its own version of the file. It routes through the same mutation and
// the same composition UpsertProviderBinding does, so the two cannot produce
// different bytes for the same inputs — the schema gate included, so a document
// reported to a caller has passed the validation a written one would.
//
// A binding that is already present and identical composes to the file's existing
// bytes, so the reported document is what the file would hold either way rather
// than an empty answer.
func ProviderBindingDocument(path, target, plugin string) ([]byte, error) {
	if target == "" || plugin == "" {
		// The condition is stated in the words of what was authored — the capability
		// a binding serves and the plugin it binds — rather than behind a package or
		// function name, neither of which a reader can see, type or act on. The
		// calling operation wraps this with the command and the file, so the
		// diagnostic still says where it came from.
		return nil, fmt.Errorf("a provider binding needs both the capability it serves and the plugin it binds")
	}
	out, _, err := composeConfigDocument(path, providerBindingMutation(target, plugin))
	return out, err
}

// providerBindingMutation builds the node surgery that records the plugin URI for
// target in a config document's plugins.providers list: an existing entry for the
// same target has its URI updated in place so its list position and other members
// are preserved, and a new target is appended.
//
// It is a factory rather than an inline closure for the reason registryMutation is
// one — the composition and the write share this one mutation instead of each
// carrying a copy that agrees today.
func providerBindingMutation(target, plugin string) func(mapping *yaml.Node) error {
	return func(mapping *yaml.Node) error {
		pluginsNode := getOrCreateMapping(mapping, "plugins")
		providers := UpsertProviderRef(readProviderBindings(pluginsNode), target, plugin)

		var provNode yaml.Node
		if err := provNode.Encode(providers); err != nil {
			return fmt.Errorf("workspaceconfig: encode providers: %w", err)
		}
		setMappingKey(pluginsNode, "providers", &provNode)
		return nil
	}
}

// UpsertPluginSocketRef records the config-socket plugin attachment uri in the
// `plugins.<socket>` list in the workspace config file at path, preserving every
// other setting, and writes the result back. socket is the config-socket key
// (governance, linter, importer); uri is the bare plugin entity identity
// (`<socket>/<name>:<version>`). An entry whose URI already matches is left in
// place — the add is idempotent and never clobbers an existing attachment's
// other members. On the importer list an entry naming the same importer at
// another version has its uri updated in place, its list position and every
// other member (its `with` included) preserved, because that list admits one
// entry per importer; on the governance and linter lists another version is a
// new URI. A new URI is appended. A file that does not yet exist is created
// carrying a `workspace: {}` marker and the one attachment.
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
	return mutateConfigDocument(path, pluginSocketRefMutation(socket, uri, role))
}

// PluginSocketRefDocument returns the bytes UpsertPluginSocketRef would write for
// uri at path, without writing anything and without creating a directory.
//
// It is the RegistryDocument of the config sockets and exists for the same
// reason: a caller that reports a document instead of writing one must not
// compose its own version of the file. It routes through the same mutation and
// the same composition UpsertPluginSocketRef does, so the two cannot produce
// different bytes for the same inputs — the schema gate included, so a socket key
// the schema does not admit is rejected here exactly as it is on the write path
// rather than reported to a caller as a usable document.
//
// An attachment already carrying this URI composes to a document whose entries
// are the file's own, matching the write's idempotent no-clobber semantic, and an
// importer already attached at another version composes to the document with
// that one entry moved to this version, as the write would leave it: the reported
// document is what the file would hold either way rather than an empty answer.
func PluginSocketRefDocument(path, socket, uri string, role plugintype.Role) ([]byte, error) {
	if socket == "" || uri == "" {
		return nil, fmt.Errorf("a plugin attachment needs both the socket it attaches to and the plugin identity to attach")
	}
	out, _, err := composeConfigDocument(path, pluginSocketRefMutation(socket, uri, role))
	return out, err
}

// pluginSocketRefMutation builds the node surgery that records uri in a config
// document's plugins.<socket> list as upsertPluginRefURI decides: an entry that
// already attaches the plugin keeps its place and its other members — on the
// importer list its uri moves to this version — and a new attachment is appended.
//
// It is a factory rather than an inline closure for the reason registryMutation is
// one — the composition and the write share this one mutation instead of each
// carrying a copy that agrees today.
func pluginSocketRefMutation(socket, uri string, role plugintype.Role) func(mapping *yaml.Node) error {
	return func(mapping *yaml.Node) error {
		pluginsNode := getOrCreateMapping(mapping, "plugins")
		refs := upsertPluginRefURI(readPluginRefs(pluginsNode, socket), socket, uri, role)

		var refsNode yaml.Node
		if err := refsNode.Encode(refs); err != nil {
			return fmt.Errorf("workspaceconfig: encode %s refs: %w", socket, err)
		}
		setMappingKey(pluginsNode, socket, &refsNode)
		return nil
	}
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
// the result back. socket is the config-socket key (governance, linter,
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
// host, and any other token is treated as a host. The entry whose Host names the
// resolved host — compared through registryhost.Same, the one reading every
// per-host lookup shares — is dropped, so a registry added by its friendly alias is removed
// by that same alias, symmetric with add. An ambiguous alias (two entries) surfaces
// its resolver error rather than removing one arbitrarily.
//
// The default_registry pointer is a separate setting and is deliberately left
// untouched: an absent entry simply reverts a default written as that host to
// default trust, and the host stays a valid target. A default written as the
// removed entry's alias names nothing afterwards, and resolution refuses it
// until it is rewritten. Like the other by-key
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
			if registryhost.Same(r.Host, host) {
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
// identity governance/tag-injector:1.2.0 it is "tag-injector". This is the same
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
// (plugins.providers) and the governance transform attachments — are frozen, since
// only those must travel with the project for byte-reproducible output; allPlugins
// widens the set to every plugin socket, adding the governance verdict
// attachments, the linter and the importer. Machine-wide tooling settings — the registries trust
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

		// The predicate is nil-ness rather than emptiness because an empty
		// `catalog_dirs` the reader authored is a statement — this workspace has no
		// local catalog — and one a capture dropped would be silently re-read as
		// the absent key, which means something else.
		if cfg.Workspace.CatalogDirs != nil {
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

// upsertPluginRefURI records uri in the plugins.<socket> entries refs: an entry
// that already attaches the plugin uri names has its uri set to uri in place —
// its list position and every other member (with, function, role) kept — and a
// bare-URI PluginRef for uri is appended when no entry does.
//
// Which entry already attaches the plugin depends on the socket. On the importer
// list it is the entry naming the same importer, as importerNamed reads both
// uris, so re-adding an importer at another version moves its one entry to that
// version: the list admits one entry per importer, and a second entry naming it
// would be refused when the configuration loads. Every other list keeps an exact
// uri match, because one plugin may be attached to it more than once, each time
// with its own with — so another version of an attached plugin is appended there
// beside the first. A uri already present is the exact match on every list, so
// re-adding the same attachment neither duplicates it nor disturbs its members.
// The in-place update mirrors UpsertProviderRef.
func upsertPluginRefURI(refs []PluginRef, socket, uri string, role plugintype.Role) []PluginRef {
	attaches := func(existing string) bool { return existing == uri }
	if Socket(socket) == SocketImporter {
		if importer, names := importerNamed(uri); names {
			attaches = func(existing string) bool {
				named, ok := importerNamed(existing)
				return ok && named == importer
			}
		}
	}
	for i := range refs {
		if attaches(refs[i].URI) {
			refs[i].URI = uri
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

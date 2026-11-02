// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
	"go.yaml.in/yaml/v3"
)

// ErrConfigInvalid wraps every refusal raised as the workspace configuration is
// read: a layer the schema rejects, a value a further check refuses, and a value
// the composed layers cannot use. Errors satisfying errors.Is(err,
// ErrConfigInvalid()) carry a wrapped message naming the file or the environment
// variable that carried the value, and what is wrong with it.
//
// Its message is a head true of every one of those refusals, rather than one
// naming the schema: several of the checks run ahead of the schema precisely
// because the schema admits the value, so a head blaming the schema would be
// false on exactly the refusals that explain themselves best.
//
// A lockfile's load registers its own sentence under a separate
// condition, and that split is deliberate and is not to be unified. A workspace
// configuration is written by an author, so a file that fails validation is
// something its author can correct — a real rule, which this condition declares
// as one no stage reaches; the lockfile's covers a file the machine writes, where
// there is no authored rule to have broken, and declares no rule at all.
func ErrConfigInvalid() error { return errConfigInvalid }

var errConfigInvalid = cerrors.Register("workspace-config-invalid", cerrors.Meta{
	ShortCode: "E0224",
	Message:   "workspace configuration is invalid",
	RulePlace: cerrors.RuleNoStageReaches("Reading the workspace configuration evaluates the rule — each layer against the schema, and " +
		"against the further shape and value checks that layer carries, and the composed layers against " +
		"the checks no single layer can answer, such as what the default registry names. The configuration " +
		"is read before any build begins, and by commands that run no build at all, so no declared stage " +
		"of a compilation holds the document being judged."),
})

// ErrDuplicateProviderTarget reports that the composed provider list binds
// one capability more than once. Errors satisfying
// errors.Is(err, ErrDuplicateProviderTarget()) carry the offending capability
// name. A capability may have exactly one provider-rendering plugin
// binding in the resolved configuration, so two entries for the same capability
// — whether within one layer or contributed across the cascade and its
// `_append` companions — halt resolution loudly rather than silently letting
// one win.
func ErrDuplicateProviderTarget() error { return errDuplicateProviderTarget }

var errDuplicateProviderTarget = cerrors.Register("duplicate-provider-target", cerrors.Meta{
	ShortCode: "E0225",
	Message:   "capability is bound to more than one provider plugin",
	RulePlace: cerrors.RuleNoStageReaches("Workspace-configuration layer resolution evaluates the rule, over the provider bindings the " +
		"cascade and its append companions compose into one list. The duplicate is a fact about the " +
		"composed list rather than about any single layer, and resolution finishes before a build starts, " +
		"so no declared stage of a compilation holds it."),
})

// ErrDuplicateImporterAttachment reports that two entries of the composed
// importer list name one importer: their uris, each with any ":<version>" split
// off, are the same identity — "importer/agent-flows:1.0.0" and
// "importer/agent-flows:2.0.0" name one importer twice. Errors satisfying
// errors.Is(err, ErrDuplicateImporterAttachment()) carry the importer named, and
// each of the two entries by its key, its position under that key, the file it
// was written in and its uri as written. The entry naming an importer is the one
// that attaches it — it chooses the version and hands the importer its
// with-config — so a second entry naming the same importer would be read by
// nothing, its with-config dropped without a word; two entries for one importer
// — whether within one layer or contributed across the cascade, its `_append`
// companions and the CI-owned overlay — halt resolution loudly instead. Only the
// importer list is judged: one plugin may be attached more than once on the
// governance and linter lists, each attachment with its own with-config.
func ErrDuplicateImporterAttachment() error { return errDuplicateImporterAttachment }

var errDuplicateImporterAttachment = cerrors.Register("duplicate-importer-attachment", cerrors.Meta{
	ShortCode: "E0338",
	Message:   "importer is attached by more than one plugins.importer entry",
	RulePlace: cerrors.RuleNoStageReaches("Workspace-configuration layer resolution evaluates the rule, over the importer entries the " +
		"cascade, its append companions and the CI-owned overlay compose into one list. The duplicate is " +
		"a fact about the composed list rather than about any single layer, and resolution finishes before " +
		"an import or a build starts, so no declared stage of a compilation holds it."),
})

// ErrCapabilityOnCrossCuttingSocket reports that a cross-cutting socket
// (governance, linter, importer) is bound by capability instead of by
// uri. Errors satisfying errors.Is(err, ErrCapabilityOnCrossCuttingSocket())
// carry the offending socket name and capability value. These sockets attach
// their plugin directly by uri: the attached plugin serves no capability, so a
// capability lookup would match nothing. A capability binding on one of them is
// therefore an attachment that can never resolve, and it halts loudly at load
// rather than surfacing later as an opaque resolve miss.
func ErrCapabilityOnCrossCuttingSocket() error { return errCapabilityOnCrossCuttingSocket }

var errCapabilityOnCrossCuttingSocket = cerrors.Register("capability-on-cross-cutting-socket", cerrors.Meta{
	ShortCode: "E0223",
	Message:   "a cross-cutting socket is bound by capability but attaches by uri",
	RulePlace: cerrors.RuleNoStageReaches("Workspace-configuration layer resolution evaluates the rule, over the socket bindings the " +
		"cascade composes, refusing a cross-cutting socket bound by capability when it attaches by uri. " +
		"Resolution finishes before a build starts, so no declared stage of a compilation holds the " +
		"composed bindings it reads."),
})

// ErrInertFieldOnProviderSocket reports that a provider-socket binding sets a
// field that has no referent on that socket — `function` or `with`. Errors
// satisfying errors.Is(err, ErrInertFieldOnProviderSocket()) carry the offending
// capability and name the inert field. A provider drives a fixed set of
// phase-keyed exports rather than one selectable entry, so `function` names
// nothing on it; and provider render configuration belongs in the Recipe, not
// the workspace provider binding, so `with` misplaces render config across the
// domain boundary. A field with no referent on a socket is rejected at
// authoring rather than silently dropped, so the operator learns the field is
// inert instead of quietly seeing it ignored.
func ErrInertFieldOnProviderSocket() error { return errInertFieldOnProviderSocket }

var errInertFieldOnProviderSocket = cerrors.Register("inert-field-on-provider-socket", cerrors.Meta{
	ShortCode: "E0228",
	Message:   "a provider socket binding sets a field with no referent on that socket",
	RulePlace: cerrors.RuleNoStageReaches("Workspace-configuration layer resolution evaluates the rule, over the provider bindings the " +
		"cascade composes, refusing a field that names nothing on the provider socket. Resolution " +
		"finishes before a build starts, so no declared stage of a compilation holds the composed " +
		"bindings it reads."),
})

// ErrColonInProviderBindingCapability reports that a provider-socket binding
// names a capability containing a colon. Errors satisfying
// errors.Is(err, ErrColonInProviderBindingCapability()) carry the offending
// capability. A Recipe's capability reference is split at its last colon into
// the capability name and a version pin, so the name a Recipe can ask for never
// contains one — and a binding is consulted only under the name a Recipe asks
// for. A binding keyed by a colon-bearing capability is therefore one no Recipe
// can ever reach, and it is refused at load rather than left to sit unread.
//
// Only the provider list is judged. The reference shape it shares also serves
// the importer list, whose entries attach by uri and whose plugins name their
// formats `import:<format>`, a spelling where the colon is part of the name.
func ErrColonInProviderBindingCapability() error { return errColonInProviderBindingCapability }

var errColonInProviderBindingCapability = cerrors.Register("colon-in-provider-binding-capability", cerrors.Meta{
	ShortCode: "E0312",
	Message:   "a provider socket binding names a capability containing a colon, which no Recipe can select",
	RulePlace: cerrors.RuleNoStageReaches("Workspace-configuration layer resolution evaluates the rule, over the provider bindings the " +
		"cascade composes, refusing a capability no Recipe's capability reference can name. Resolution " +
		"finishes before a build starts, so no declared stage of a compilation holds the composed " +
		"bindings it reads."),
})

// ErrGovernanceRoleInvalid reports that a governance attachment declares no
// role, or a role that is not a known governance role. Errors satisfying
// errors.Is(err, ErrGovernanceRoleInvalid()) carry the offending attachment
// identity and the invalid role value. The role — not the plugin's declared
// kind — selects which pipeline runner (and therefore which facet) an
// attachment feeds, and the dispatch reads attachments by role, so an
// attachment with a missing or unknown role would attach to no runner and be
// silently ignored. It is rejected at authoring on the composed config so a
// hand-edited role typo or omission halts loudly rather than dropping the
// plugin.
func ErrGovernanceRoleInvalid() error { return errGovernanceRoleInvalid }

var errGovernanceRoleInvalid = cerrors.Register("governance-role-invalid", cerrors.Meta{
	ShortCode: "E0227",
	Message:   "a governance attachment declares no role or an unknown role",
	RulePlace: cerrors.RuleNoStageReaches("Workspace-configuration layer resolution evaluates the rule, over the governance attachments " +
		"the cascade composes, refusing one whose role names no runner the dispatch reads attachments " +
		"by. Resolution finishes before a build starts, so no declared stage of a compilation holds the " +
		"composed attachments it reads."),
})

// ErrDuplicateRegistryAlias reports that a registry alias is already used by a
// different host entry. Errors satisfying errors.Is(err, ErrDuplicateRegistryAlias())
// carry the offending alias and the host that already holds it. A registry alias
// is a unique convenience selector across entries, so registering one already
// bound to another host halts loudly rather than shadowing the existing binding.
func ErrDuplicateRegistryAlias() error { return errDuplicateRegistryAlias }

var errDuplicateRegistryAlias = cerrors.Register("duplicate-registry-alias", cerrors.Meta{
	ShortCode: "E0226",
	Message:   "registry alias is already used by another host",
	RulePlace: cerrors.RuleNoStageReaches("A configuration write evaluates the rule, as a registry entry is composed into the document " +
		"and its alias is checked against the entries already recorded there. The write edits a file " +
		"rather than compiling anything, so no declared stage of a compilation holds the entry list the " +
		"alias is checked against."),
})

// ErrAmbiguousRegistryAlias reports that a registry token matched the alias of
// more than one entry during resolution. Errors satisfying
// errors.Is(err, ErrAmbiguousRegistryAlias()) carry the offending alias. An alias
// selects exactly one host; a hand-edited config that binds the same alias to two
// hosts makes the selection ambiguous, so resolution halts loudly rather than
// silently choosing one host over the other. The token is either one a command
// names or the default registry a configuration names, and both are matched by
// the one alias match.
func ErrAmbiguousRegistryAlias() error { return errAmbiguousRegistryAlias }

var errAmbiguousRegistryAlias = cerrors.Register("ambiguous-registry-alias", cerrors.Meta{
	ShortCode: "E0222",
	Message:   "registry alias matches more than one host",
	RulePlace: cerrors.RuleNoStageReaches("Registry-token resolution evaluates the rule when a token matches the alias of more than " +
		"one configured entry: the token a command names, once per invocation, or the default registry the " +
		"workspace configuration names, once as that configuration resolves. Both are resolved before any " +
		"build begins, so no declared stage of a compilation holds the value being resolved."),
})

// Load reads the workspace's .aggrega-config.yaml file at path,
// validates it against the aggrega-config-v1alpha1 JSON Schema, and
// returns the typed Config value.
//
// File absence is NOT an error: a workspace without a
// .aggrega-config.yaml gets Default() (the documented defaults
// — hub.aggrega.ai for the registry, .aggrega for the cache,
// no halt_on_deprecated_severity). Callers don't need to pre-check
// for the file.
//
// Validation runs BEFORE typed unmarshalling: an invalid file
// produces a wrapped ErrConfigInvalid error with the offending
// field path and message; the typed struct is returned only when
// the schema check passes. This ordering ensures error diagnostics
// come from the JSON Schema layer (with field-path precision)
// rather than from yaml.v3's looser type-mismatch messages.
//
// Overlay-on-Default semantics: after schema validation succeeds,
// the typed unmarshal target is seeded with Default() so fields the
// document omits inherit the documented non-zero defaults rather
// than the Go zero-value. yaml.v3 only writes fields present in
// the source mapping; absent keys preserve the target's
// pre-existing values.
//
// yaml.v3 explicit-empty-mapping semantics (pinned empirically by
// TestLoadExplicitEmptyMapHonoursYAMLSemantics): an explicit empty
// mapping in the document (e.g. `workspace: {}`) is treated by
// yaml.v3's recursive unmarshal the same as the key being omitted
// — defaults are preserved. The empty mapping has no keys to
// write into the target struct, so the Default()-seeded values
// survive. The test pins this behaviour; if a future yaml.v3
// upgrade changes the disposition the test catches it.
//
// Returns wrapped errors on:
//   - File read failure (other than file-not-found).
//   - YAML parse failure (malformed document structure).
//   - Schema validation failure (well-formed YAML violating the schema).
//
// Unexported: the only supported entry point is Resolve, which performs the
// whole discovery-and-cascade job. Keeping the single-file loader internal
// means no command outside this package can bypass Resolve to read a lone
// config file — the single resolution path is compiler-enforced.
func load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Default(), nil
		}
		return Config{}, fmt.Errorf("workspaceconfig: read %s: %w", path, err)
	}
	if err := validateBytes(data, path); err != nil {
		return Config{}, err
	}
	p := Default()
	if err := yaml.Unmarshal(data, &p); err != nil {
		// Defensive: schema accepted the instance but typed unmarshal
		// failed. Should not happen with a well-formed schema; surface
		// as a wrapped error rather than a panic so the call site can
		// still report something.
		return Config{}, fmt.Errorf("workspaceconfig: typed unmarshal %s: %w", path, err)
	}
	if err := validateArtifactCacheDir(p.ArtifactCacheDir); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	releases, err := anchorReleaseCatalogs(p.Workspace.Releases, path)
	if err != nil {
		return Config{}, err
	}
	p.Workspace.Releases = releases
	if err := validateSigningKinds(p.Signing.Kinds); err != nil {
		return Config{}, fmt.Errorf("%s: %w", path, err)
	}
	for _, r := range p.Registries {
		if err := validateSigningKinds(r.Signing.Kinds); err != nil {
			return Config{}, fmt.Errorf("%s: %w", path, err)
		}
	}
	return p, nil
}

// validateArtifactCacheDir rejects an artifact_cache_dir that is absolute, or
// that names the workspace root itself, wrapping ErrConfigInvalid so the failure
// is category-consistent with every other config-load rejection. The refusal
// says what is wrong with the value and not where it came from: the caller names
// the layer that carried it — a config file, or an environment variable — once,
// in front of it.
//
// The setting is workspace-relative by contract: every consumer joins it to the
// workspace root, so an absolute value would name a location outside the
// workspace that the walk anchors cannot reach, and the cache the build reads
// would silently stop being the cache the walks step over.
//
// The workspace root itself fails the same way from the other direction: it puts
// the cache and the workspace at one location, so a catalog directory sitting at
// the workspace root — the mainstream layout — is the cache, and the catalog walk
// steps over the entire tree and ingests nothing while reporting no error. It is
// also incoherent on its own terms, scattering fetched artifacts across the
// workspace root. The predicate is filepath.Clean rather than a string compare so
// every spelling of the workspace root is caught, not just the bare dot.
//
// Refusing both at load keeps the one contract every consumer already assumes,
// and refuses once rather than letting each consumer discover it differently.
//
// It judges the value's spelling and nothing else: which side of the workspace the
// directory lies on, and what may be written, removed or read beneath it, is
// writescope.RootForCache's answer, which every writer, clearer and reader of the
// cache asks. A value spelled climbing out of the workspace root passes here, because it
// is a directory the configuration names outside the workspace.
func validateArtifactCacheDir(v string) error {
	if v == "" {
		return nil
	}
	if filepath.IsAbs(v) {
		return fmt.Errorf("artifact_cache_dir %q must be a workspace-relative path, not an absolute one; write it relative to the workspace root (for example `.aggrega/cache`): %w", v, ErrConfigInvalid())
	}
	if filepath.Clean(v) == "." {
		return fmt.Errorf("artifact_cache_dir %q names the workspace root itself; a catalog directory at the workspace root would then be stepped over as cache, leaving nothing to load — give the cache its own subdirectory (for example `.aggrega/cache`): %w", v, ErrConfigInvalid())
	}
	return nil
}

// validateSigningKinds rejects a signing.kinds entry that is neither the "all"
// wildcard nor a known pushable artifact kind, wrapping ErrConfigInvalid so the
// failure is category-consistent with every other config-load rejection. As with
// validateArtifactCacheDir, the caller names the layer that carried the value —
// the offending file, or the environment variable — once, in front of the
// refusal. The set of pushable kinds is the single one the OCI transport
// recognizes, so the membership check reuses that oracle rather than re-listing
// the kinds here.
func validateSigningKinds(kinds []string) error {
	for _, k := range kinds {
		if k == signingKindsAll {
			continue
		}
		if !oci.IsPublishableKind(k) {
			return fmt.Errorf("signing.kinds entry %q is not a signable artifact kind; use a catalog entity or plugin kind, or %q for every kind: %w", k, signingKindsAll, ErrConfigInvalid())
		}
	}
	return nil
}

// validateBytes runs the schema-validation phase against an already-
// read YAML document. Returns nil when the document is valid (or
// empty); returns a wrapped ErrConfigInvalid when the schema rejects
// it; returns a parse/build error for the underlying yaml / schema
// failures. The path argument is decorative — it appears in error
// messages to identify the offending file.
//
// Shared by Load and loadOverlayInto so each call site reads the
// file once and validates once, instead of paying the YAML parse
// cost twice when both validation and overlay-into-seed are needed.
func validateBytes(data []byte, path string) error {
	var probe any
	if err := yaml.Unmarshal(data, &probe); err != nil {
		return fmt.Errorf("workspaceconfig: parse %s: %w", path, err)
	}
	if probe == nil {
		return nil
	}
	if err := checkConfigShape(probe, path); err != nil {
		return err
	}
	if err := checkRegistryHosts(probe, path); err != nil {
		return err
	}
	if err := checkProviderBindingForms(probe, path); err != nil {
		return err
	}
	if err := checkVerdictAttachmentsNamePlugin(probe, path); err != nil {
		return err
	}
	_, err := envelope.ValidateBytes(envelope.KindAggregaConfig, data, path)
	var envErr *envelope.Error
	if errors.As(err, &envErr) {
		f := envErr.Findings[0]
		fieldPath := f.FieldPath
		if fieldPath == "" {
			fieldPath = "(root)"
		}
		return fmt.Errorf("workspaceconfig: %s: %s: %s: %w",
			path, fieldPath, f.Message, ErrConfigInvalid())
	}
	if err != nil {
		return fmt.Errorf("workspaceconfig: validate %s: %w", path, err)
	}
	return nil
}

// checkConfigShape produces fix-it diagnostics for the two v2-layout
// mistakes the bare JSON Schema can only report generically: a null
// `workspace:` marker (the schema says "expected object, got null") and a
// key that moved out of `workspace` or was renamed at the top level (the
// schema says "additional property not allowed"). It lives in the single
// shared validate path so the CLI and daemon both surface the better
// message; an independent validator (e.g. an editor extension) still gets
// the generic schema message, which is acceptable. All returns wrap
// ErrConfigInvalid for category-consistent detection.
func checkConfigShape(probe any, path string) error {
	root, ok := probe.(map[string]any)
	if !ok {
		return nil
	}
	if ws, present := root["workspace"]; present {
		if ws == nil {
			return fmt.Errorf("workspaceconfig: %s: `workspace` must be a mapping; write `workspace: {}` to mark a workspace root with no catalog directories: %w", path, ErrConfigInvalid())
		}
		if wsMap, ok := ws.(map[string]any); ok {
			for _, moved := range []string{"default_registry", "offline", "auto_pull", "cache_dir"} {
				if _, found := wsMap[moved]; found {
					hint := moved
					if moved == "cache_dir" {
						hint = "artifact_cache_dir"
					}
					return fmt.Errorf("workspaceconfig: %s: `%s` moved to the top level; set top-level `%s`, not under `workspace`: %w", path, moved, hint, ErrConfigInvalid())
				}
			}
		}
	}
	if _, found := root["cache"]; found {
		return fmt.Errorf("workspaceconfig: %s: the `cache` block was renamed to `parse_cache`: %w", path, ErrConfigInvalid())
	}
	return nil
}

// checkRegistryHosts refuses a registries[].host that is not a registry host in
// the spelling its trust policy is kept under — the one reading
// registryhost.Check words — naming the key, the file that carried it, and the
// spelling to write where the value names a host at all.
//
// It runs ahead of the schema, whose pattern on the key states the same shape,
// so the refusal a reader meets names the fix rather than quoting a pattern; an
// independent validator reading the schema alone still refuses the same values.
// A value of another type is left to the schema, which reports the type.
//
// default_registry is not judged here. It may name a registries entry, which
// another layer may declare, so no single file can tell a name from a mistake;
// it is judged once, where the configuration resolves, over every layer — the
// files and the environment alike. The command line names a publication's
// destination rather than the default, and holds it to the same reading where
// it names one.
func checkRegistryHosts(probe any, path string) error {
	root, ok := probe.(map[string]any)
	if !ok {
		return nil
	}
	entries, _ := root["registries"].([]any)
	for i, item := range entries {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if v, ok := entry["host"].(string); ok {
			if err := registryhost.Check(fmt.Sprintf("registries[%d].host", i), v); err != nil {
				return fmt.Errorf("%s: %v: %w", path, err, ErrConfigInvalid())
			}
		}
	}
	return nil
}

// ociScheme is the scheme a registry location is written with. A provider
// binding written with it names where a plugin is published rather than which
// plugin it is.
const ociScheme = "oci://"

// checkProviderBindingForms refuses a plugins.providers or providers_append entry
// whose uri is written with the oci:// scheme, naming the file, the entry, the
// capability it binds, the uri as written and the form to write.
//
// A provider binding names its plugin by the plugin's identity — the
// provider/<name> coordinate, optionally followed by :<version> — because that is
// what capability resolution compares a binding against and what the lockfile
// records. A registry location is a different thing: it says where a plugin is
// published, and no plugin's identity is spelled that way, so a binding written
// as one could never select anything. Refusing it as the file is read names the
// fix where it was written, rather than leaving a later resolution to fail on a
// value it was never meant to receive.
//
// It keys on the explicit scheme and on nothing broader. LooksLikeOCIURI answers
// whether a value is a registry reference rather than a path to anchor, and it
// answers true for any value carrying a slash and a colon — which a legitimate
// provider/<name>:<version> binding does. Using it here would refuse the very
// form this check names as the one to write.
//
// Like checkRegistryHosts it runs ahead of the schema, which admits any string
// as a uri, and it leaves an entry of another shape to the schema to report.
func checkProviderBindingForms(probe any, path string) error {
	root, ok := probe.(map[string]any)
	if !ok {
		return nil
	}
	plugins, ok := root["plugins"].(map[string]any)
	if !ok {
		return nil
	}
	for _, key := range []string{"providers", "providers_append"} {
		entries, _ := plugins[key].([]any)
		for i, item := range entries {
			entry, ok := item.(map[string]any)
			if !ok {
				continue
			}
			uri, ok := entry["uri"].(string)
			if !ok || !strings.HasPrefix(uri, ociScheme) {
				continue
			}
			capability, _ := entry["capability"].(string)
			return fmt.Errorf("%s: plugins.%s[%d] binds capability %q to %q, which is where a plugin is published rather than which plugin it is; a provider binding names its plugin by its identity — write `uri: provider/<name>`, with an optional `:<version>`: %w",
				path, key, i, capability, uri, ErrConfigInvalid())
		}
	}
	return nil
}

// entryFunctionSockets are the sockets whose attachments may name an entry
// function — the lists the schema shapes as PluginRef. Each is authored under
// its socket's own name and under that name's `_append` companion.
var entryFunctionSockets = []Socket{SocketGovernance, SocketLinter}

// checkVerdictAttachmentsNamePlugin refuses an attachment at the verdict tier
// that names an entry function with `function:` and no plugin `uri:`, naming the
// file, the list and the entry's position in it, the function as written, and
// the fix.
//
// The verdict tier is read per attachment through Socket.RefTier, so it is the
// set the tier classification places there — every linter entry, and a
// governance entry whose role is a verdict role — and a role later classified as
// a verdict role is judged without an edit here. A cross-cutting socket selects
// its plugin by uri alone, so such an entry attaches no plugin and the function
// it names is never called: the lint goes on as though the entry were not there,
// and a governance verdict attachment fails only when the build comes to resolve
// a plugin the entry never named. An author who named a function expects it to
// run, so the entry is refused where it was written.
//
// It reads every authored list of one layer, the `_append` companions included,
// so the refusal names the file whichever list carried the entry and whichever
// layer — a tree file, the global file or a CI-owned overlay — it sits in. Left
// to other checks: an entry carrying a capability, which the cross-cutting socket
// check refuses on the composed list; a governance entry with no role or an
// unknown one, which is no verdict attachment and which the role check refuses;
// and a value of another type, which the schema reports. An entry naming neither
// a function nor a uri is not judged, and neither is a governance entry in a
// transform role, which is not at the verdict tier.
//
// Like checkProviderBindingForms it runs ahead of the schema, which admits an
// entry carrying no uri.
func checkVerdictAttachmentsNamePlugin(probe any, path string) error {
	root, ok := probe.(map[string]any)
	if !ok {
		return nil
	}
	plugins, ok := root["plugins"].(map[string]any)
	if !ok {
		return nil
	}
	for _, socket := range entryFunctionSockets {
		for _, key := range []string{string(socket), string(socket) + "_append"} {
			entries, _ := plugins[key].([]any)
			for i, item := range entries {
				entry, ok := item.(map[string]any)
				if !ok {
					continue
				}
				function, ok := entry["function"].(string)
				if !ok || function == "" {
					continue
				}
				if uri, present := entry["uri"]; present && uri != "" {
					continue
				}
				if capability, present := entry["capability"]; present && capability != "" {
					continue
				}
				role, _ := entry["role"].(string)
				if socket.RefTier(PluginRef{Role: plugintype.Role(role)}) != SocketTierVerdict {
					continue
				}
				return fmt.Errorf("workspaceconfig: %s: plugins.%s[%d] names the entry function %q but no plugin `uri`; an attachment on this list selects its plugin by `uri`, so this one attaches no plugin and %q is never called — add the `uri` of the plugin that exports it: %w",
					path, key, i, function, function, ErrConfigInvalid())
			}
		}
	}
	return nil
}

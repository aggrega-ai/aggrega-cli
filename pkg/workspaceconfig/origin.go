// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
	"go.yaml.in/yaml/v3"
)

// originDefaultsLabel is the source label a setting carries when no config file,
// environment variable or command-line flag sets it, so the built-in default is
// what applies.
const originDefaultsLabel = "(built-in defaults)"

// originEnvPrefix marks a source label as an environment override rather than a
// file path, and carries the variable's own name after it.
const originEnvPrefix = "env:"

// EnvironmentVariableWords names the environment variable name in a sentence a
// reader is shown: "the NAME environment variable".
//
// It writes no shell's variable syntax — neither a leading "$" nor a pair of
// "%" — because what the reader sets is the name itself, and the reader may be at
// a Windows prompt or a Unix one, where each spelling is wrong at the other. A
// refusal that names the environment variable which carried a value names it
// here, so those refusals read alike.
func EnvironmentVariableWords(name string) string {
	return "the " + name + " environment variable"
}

// originLayerLabel renders one entry of the resolved cascade as the source label
// a setting attributed to that layer carries.
//
// The cascade records an environment override as a "$" followed by the variable
// name and a config file as its absolute path. A source label spells the first
// as the variable name behind an "env:" prefix and the second as the path
// unchanged, so the two vocabularies meet here and nowhere else: a caller
// joining a setting's source against the cascade compares strings that were
// produced by this one function on both sides.
func originLayerLabel(entry string) string {
	if strings.HasPrefix(entry, "$") {
		return originEnvPrefix + entry[1:]
	}
	return entry
}

// ContributingLayers returns the layers that contributed to the merge, lowest
// precedence first, each spelled as the source label ValueOrigins attributes a
// setting to.
//
// It is ContributingFiles read in the provenance vocabulary rather than the
// cascade's own, which is what lets a caller join a setting's Source against
// this list directly. The built-in defaults are deliberately absent: they are
// not a layer that contributed but what applies where no layer spoke, and
// ValueOrigins already reports them per setting.
func (r *ResolvedWorkspace) ContributingLayers() []string {
	out := make([]string, 0, len(r.files))
	for _, entry := range r.files {
		out = append(out, originLayerLabel(entry))
	}
	// The command-line layer sits above every file and every variable, so its
	// labels come last. They are produced by running the overlay against a
	// throwaway config rather than by a second enumeration of what the layer
	// can carry: the labels reported here and the ones a setting is attributed
	// to have to be one list, and a table walked twice is one list while two
	// walks written separately are two.
	var scratch Config
	return append(out, applyFlagOverlay(&scratch, r.flags)...)
}

// ValueOrigin pairs one effective configuration setting with the layer that
// determined it. Field is the setting's dotted config path, Value is its
// effective value rendered as a stable string, and Source is the origin layer:
// a config file's absolute path, an "env:AGGREGA_X" environment override, a
// command-line flag as the reader typed it, or the built-in-defaults label.
// Position locates the setting inside that config file.
type ValueOrigin struct {
	Field  string
	Value  string
	Source string

	// Position is the span of the key in Source that supplied the value, read
	// from the parsed document rather than by searching its text, in that
	// document's own coordinates: 1-based line and 1-based UTF-8 byte column.
	//
	// It is nil — never a zero span — whenever no document key answers. A
	// command-line flag, an environment override and the built-in defaults have
	// no file at all, and a file layer may have set the setting through a merge
	// key, which leaves no key of the setting's own for the walk to find. A
	// consumer therefore separates "no position" from "position at the very
	// start of the file" by the nil check alone: the first key of a document
	// sits at line 1, column 1, which no absent position resembles.
	Position *yamlpos.FieldSpan
}

// OriginKind classifies the layer a ValueOrigin.Source names.
//
// It exists so a consumer that has to say something different per layer — a
// refusal telling a reader which of the four to change — reads the
// classification off the one function that produces the labels, rather than
// re-deriving it from the label's own text. A second reading of those spellings
// would agree until either moved.
type OriginKind int

const (
	// OriginBuiltInDefault reports a setting no layer sets, so the value
	// Default() seeds is what applies. Such a layer has no name.
	OriginBuiltInDefault OriginKind = iota
	// OriginConfigFile reports a setting a config file in the cascade
	// determined. The layer's name is that file's absolute path.
	OriginConfigFile
	// OriginEnvironment reports a setting an environment override determined.
	// The layer's name is the variable, without the "$".
	OriginEnvironment
	// OriginCommandLineFlag reports a setting the invocation's own command
	// line determined. The layer's name is the flag as a reader types it,
	// leading dashes included, because that is the only spelling of the layer
	// a reader can act on.
	OriginCommandLineFlag
)

// Layer classifies v.Source and returns the name of the layer within that
// class. It is the inverse of the labelling the report performs, and it lives
// beside that labelling so the two cannot drift apart.
func (v ValueOrigin) Layer() (OriginKind, string) {
	switch {
	case v.Source == originDefaultsLabel:
		return OriginBuiltInDefault, ""
	case strings.HasPrefix(v.Source, originEnvPrefix):
		return OriginEnvironment, strings.TrimPrefix(v.Source, originEnvPrefix)
	case strings.HasPrefix(v.Source, flagLayerPrefix):
		return OriginCommandLineFlag, v.Source
	default:
		return OriginConfigFile, v.Source
	}
}

// originField is one row of the fixed provenance enumeration: the setting's
// dotted name, whether it is a scalar leaf or a list or map field, and the
// function that renders its effective value for display.
//
// Both kinds are attributed by one rule, and they differ only in which document
// keys set one: a list field is also set through its merge-time `_append`
// companion, so a layer that contributes only through the companion is the
// layer named. See originDocumentPaths.
type originField struct {
	name    string
	scalar  bool
	display func(Config) string
}

// ValueOrigins attributes each effective configuration setting to the layer
// that determined it, for a per-value provenance view.
//
// A setting is attributed to the highest-precedence layer that sets it,
// whatever value that layer sets. A layer writing the value already in force —
// the built-in default, or what a layer beneath it says — is still the layer
// named: it is the one holding the value in place, and removing it is what
// changes the answer the day the default or the layer beneath moves. A setting
// no layer sets is attributed to the built-in defaults.
//
// Whether a layer sets a setting is read the way the resolver reads that layer,
// never re-derived from values. A config file sets a setting when its document
// carries the setting's key — for a list, the key or its `_append` companion —
// and the CI-owned additive overlay only through a key it is read for, which is
// every key ciNonAdditiveKeysIn does not report it ignoring. An environment
// variable sets the setting its binding mirrors when the cascade applied it,
// which it does only for a variable exported with a non-empty value, so the
// cascade's own record answers and the environment is not read again. A flag
// sets the setting its binding writes when that binding applied. The layers are
// walked lowest precedence first — the same ordered layers ContributingFiles
// reports, then the command line — and each layer that sets a setting replaces
// the one before it, which leaves the highest-precedence one named.
//
// A list or map field is attributed as a whole; which layer contributed which
// entry is not tracked. The one setting whose effective value is not a plain
// per-layer whole-replace is signing.required, which composes monotonically
// (any layer that turns it on wins and no layer can turn it back off); it is
// attributed to the first layer that turns it on, which is the layer that made
// the effective value what it is. A layer writing `required: false` turns
// nothing on, so where no layer turns it on the setting is attributed to the
// built-in defaults.
//
// The displayed value of every setting is the effective value from the resolved
// config, so it always reflects what applies right now. The files are read when
// this is called, as they stand, so an edit since resolution can move a source
// while the value beside it stays. Result order is a fixed field order, so the
// output is byte-stable for a given resolution.
//
// Each file is read and parsed once, and that one document answers both whether
// the file sets a setting and where: a setting a config file was attributed to
// carries the position of the key that set it, and a setting attributed to a
// flag, an environment override or the built-in defaults carries no position at
// all. See ValueOrigin.Position.
func (r *ResolvedWorkspace) ValueOrigins() ([]ValueOrigin, error) {
	base := r.lookup
	if base == nil {
		base = os.Getenv
	}

	fields := originFields()
	source := make(map[string]string, len(fields))
	for _, f := range fields {
		source[f.name] = originDefaultsLabel
	}

	layers := make(map[string]originLayer, len(r.files))
	for _, entry := range r.files {
		if strings.HasPrefix(entry, "$") {
			for _, b := range envBindings {
				if b.envVar == entry[1:] {
					source[b.configPath] = originLayerLabel(entry)
				}
			}
			continue
		}

		layer, err := readOriginLayer(entry)
		if err != nil {
			return nil, err
		}
		if filepath.Base(entry) == AppendCIConfigFilename {
			layer.ignored = ciNonAdditiveKeysIn(layer.values)
		}
		layers[entry] = layer
		for _, f := range fields {
			if len(layer.setPaths(f)) > 0 {
				source[f.name] = entry
			}
		}
	}

	// The command line is the top of the ladder, so it is walked after every
	// file and every variable. Each row is asked on its own, so a setting is
	// attributed to the flag that set it rather than to whichever flag the table
	// happens to list first.
	for _, b := range flagBindings {
		var scratch Config
		if b.apply(&scratch, r.flags) {
			source[b.configPath] = b.label
		}
	}

	// signing.required composes monotonically rather than whole-replacing per
	// layer, so its origin is the first layer that turns it on, not the last
	// layer that wrote it. This override keeps the reported source consistent
	// with the effective (monotonically-composed) value.
	source["signing.required"] = signingRequiredSource(r, base)

	out := make([]ValueOrigin, 0, len(fields))
	for _, f := range fields {
		out = append(out, ValueOrigin{
			Field:    f.name,
			Value:    f.display(r.config),
			Source:   source[f.name],
			Position: originPosition(layers, f, source[f.name]),
		})
	}
	return out, nil
}

// originAppendSuffix is the sibling-key suffix a list field's additive
// companion carries — `catalog_dirs` alongside `catalog_dirs_append` — which is
// the convention the Config type documents. A field's companion path is derived
// from it rather than spelled out a second time here.
const originAppendSuffix = "_append"

// originLayer is one config document, parsed once, as the provenance report
// reads it: the values the document carries, decoded the way the resolver
// decodes a config file, and the node tree and line index a key's position is
// read from. present is false for a layer with no document at all — one that has
// gone missing, or one that is empty — and such a layer sets nothing.
type originLayer struct {
	values  map[string]any
	doc     *yaml.Node
	index   yamlpos.LineIndex
	present bool

	// ignored lists the document paths the resolver does not read from this
	// layer, each standing for itself and everything beneath it. It is empty
	// for every layer except the CI-owned additive overlay, which is read only
	// for the keys that add to a list or to the signing policy.
	ignored []string
}

// setPaths returns the document paths through which the layer sets f, in the
// order originDocumentPaths tries them: each path the document carries and the
// layer is read for. An empty result means the layer does not set f.
//
// Carrying a path means holding a key at every one of its segments, whatever
// the value at the end — a key written with the value already in force sets the
// setting as surely as one written with any other.
func (l originLayer) setPaths(f originField) []string {
	var out []string
	for _, path := range originDocumentPaths(f) {
		if l.readsPath(path) && documentCarries(l.values, path) {
			out = append(out, path)
		}
	}
	return out
}

// readsPath reports whether the resolver reads the document path from this
// layer at all.
func (l originLayer) readsPath(path string) bool {
	for _, ignored := range l.ignored {
		if path == ignored || strings.HasPrefix(path, ignored+".") {
			return false
		}
	}
	return true
}

// documentCarries reports whether values holds a key at every segment of the
// dotted path.
func documentCarries(values map[string]any, path string) bool {
	var node any = values
	for _, segment := range strings.Split(path, ".") {
		mapping, ok := node.(map[string]any)
		if !ok {
			return false
		}
		next, found := mapping[segment]
		if !found {
			return false
		}
		node = next
	}
	return true
}

// originPosition returns the position of the key that set f in the config file
// source names, read from the document the attribution itself read.
//
// It is nil when source names no document — a command-line flag, an environment
// override, the built-in defaults — which is what makes a nil Position
// unambiguous rather than a coordinate to interpret. Those three are told apart
// from a file by membership in the layers the walk read rather than by how
// their labels are spelled: a label that is not a path was never read, so there
// is nothing of its to look up.
func originPosition(layers map[string]originLayer, f originField, source string) *yamlpos.FieldSpan {
	layer, read := layers[source]
	if !read || !layer.present {
		return nil
	}
	return originPositionIn(layer, layer.setPaths(f))
}

// readOriginLayer reads and parses one config file so the report can read which
// settings it sets and where each key sits.
//
// A file that has gone missing between the cascade's read and this one yields a
// layer that sets nothing rather than an error, because the cascade's own
// single-file load treats absence as the documented defaults and the report is
// read against the files as they stand: an attribution must not be the thing
// that turns a working provenance view into a failing one. Every other read
// failure, and any parse failure, is reported.
func readOriginLayer(path string) (originLayer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return originLayer{}, nil
		}
		return originLayer{}, fmt.Errorf("read %s to attribute its settings: %w", path, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return originLayer{}, fmt.Errorf("parse %s to attribute its settings: %w", path, err)
	}
	if len(doc.Content) == 0 {
		return originLayer{}, nil
	}
	// The values are decoded from the node tree already parsed rather than from
	// the bytes again. The decode resolves aliases and merge keys the way the
	// resolver's own decode does, so a setting supplied through a merge key is
	// read as set even though the node walk that finds positions cannot address
	// the key that supplied it.
	var values map[string]any
	if err := doc.Decode(&values); err != nil {
		return originLayer{}, fmt.Errorf("decode %s to attribute its settings: %w", path, err)
	}
	return originLayer{
		values: values,
		doc:    &doc,
		// UTF-8 pins a span to the document's own coordinates — 1-based line
		// and 1-based byte column, not the parser's own character column —
		// so no editor-protocol encoding is baked into a configuration value.
		// A consumer wanting protocol coordinates converts through this same
		// index rather than reinterpreting the numbers.
		index:   yamlpos.BuildLineIndexWithEncoding(data, yamlpos.PositionEncodingUTF8),
		present: true,
	}, nil
}

// originDocumentPaths lists, in the order they are tried, the document key
// paths through which a layer sets f.
//
// A scalar leaf has exactly one. A list field additionally has its `_append`
// companion: that companion is how a layer contributes to a list it does not
// whole-replace, and it is why such a layer can be attributed a field whose
// base key it never writes.
//
// The base key is tried first on every layer, including the CI-owned additive
// overlay. That overlay folds a base list and its companion into one additive
// contribution with the base's entries ahead of the companion's, so the base
// key is what supplied the value there too whenever the document carries it.
func originDocumentPaths(f originField) []string {
	if f.scalar {
		return []string{f.name}
	}
	return []string{f.name, f.name + originAppendSuffix}
}

// originPositionIn returns the span of the first path the layer's document
// carries in full, or nil when it carries none of them.
//
// A partial resolution is rejected rather than reported. The shared field-path
// descent returns the deepest key it reached when a document runs out before
// the path does — the right answer for positioning a finding about a key that
// is absent, and the wrong one here, because an origin position names the key
// that supplied the value. A full match returns the path's last segment as the
// key, so comparing the two separates the cases. That test is exact for every
// attributed setting because no attributed path repeats a segment; were one
// ever to, the worst it could cost is an enclosing key's position reported
// where none should be.
func originPositionIn(layer originLayer, paths []string) *yamlpos.FieldSpan {
	for _, path := range paths {
		node := yamlpos.FieldNode(layer.doc, path)
		if node == nil {
			continue
		}
		leaf := path
		if i := strings.LastIndexByte(path, '.'); i >= 0 {
			leaf = path[i+1:]
		}
		if node.Value != leaf {
			continue
		}
		span, ok := yamlpos.FieldRange(layer.doc, layer.index, path)
		if !ok {
			continue
		}
		return &span
	}
	return nil
}

// signingRequiredSource returns the origin of the effective signing.required
// stance: the first cascade layer (in resolution order, so lowest precedence
// first) whose raw signing block turns the requirement on, or the built-in
// defaults label when no layer does. Because the requirement composes
// monotonically, the first layer to turn it on is the one that makes the
// effective value true.
func signingRequiredSource(r *ResolvedWorkspace, base func(string) string) string {
	for _, entry := range r.files {
		if strings.HasPrefix(entry, "$") {
			if entry[1:] == "AGGREGA_SIGNING_REQUIRED" {
				var on bool
				if applyBool(base(entry[1:]), &on) == nil && on {
					return originLayerLabel(entry)
				}
			}
			continue
		}
		cfg, err := load(entry)
		if err != nil {
			continue
		}
		if cfg.Signing.Required {
			return entry
		}
	}
	return originDefaultsLabel
}

// originFields enumerates every attributed setting in a fixed order: the scalar
// leaves first (attributed per-value), then the list and map fields (attributed
// as a unit). The order is stable so ValueOrigins produces byte-identical output
// for a given resolution.
func originFields() []originField {
	sc := func(name string, display func(Config) string) originField {
		return originField{name: name, scalar: true, display: display}
	}
	ls := func(name string, display func(Config) string) originField {
		return originField{name: name, scalar: false, display: display}
	}
	return []originField{
		sc("default_registry", func(c Config) string { return c.DefaultRegistry }),
		sc(OfflineSettingKey, func(c Config) string { return strconv.FormatBool(c.Offline) }),
		sc("auto_pull", func(c Config) string { return strconv.FormatBool(c.AutoPull) }),
		sc(PinSourcesAtPullKey, func(c Config) string { return strconv.FormatBool(c.PinSourcesAtPull) }),
		sc(PinSourcesAtPushKey, func(c Config) string { return strconv.FormatBool(c.PinSourcesAtPush) }),
		sc("artifact_cache_dir", func(c Config) string { return c.ArtifactCacheDir }),
		sc("skip_signature_verification", func(c Config) string { return strconv.FormatBool(c.SkipSignatureVerification) }),
		sc("build.authoring_release", func(c Config) string { return c.Build.AuthoringRelease }),
		sc("build.halt_on_deprecated_severity", func(c Config) string { return c.Build.HaltOnDeprecatedSeverity }),
		sc("build.yaml_indent", func(c Config) string { return strconv.Itoa(c.Build.YAMLIndent) }),
		sc("lint.publishing_discipline.allow_dynamic_refs", func(c Config) string {
			return strconv.FormatBool(c.Lint.PublishingDiscipline.AllowDynamicRefs)
		}),
		sc("lint.publishing_discipline.allow_id_overrides", func(c Config) string {
			return strconv.FormatBool(c.Lint.PublishingDiscipline.AllowIDOverrides)
		}),
		sc("lint.publishing_discipline.allow_direct_x_aggrega_extensions", func(c Config) string {
			return strconv.FormatBool(c.Lint.PublishingDiscipline.AllowDirectXAggregaExtensions)
		}),
		sc("lint.publishing_discipline.allow_aggrega_ai_prefix", func(c Config) string {
			return strconv.FormatBool(c.Lint.PublishingDiscipline.AllowAggregaAIPrefix)
		}),
		sc("lint.publishing_discipline.allow_nonstandard_names", func(c Config) string {
			return strconv.FormatBool(c.Lint.PublishingDiscipline.AllowNonstandardNames)
		}),
		sc("lint.publishing_discipline.allow_category_mismatches", func(c Config) string {
			return strconv.FormatBool(c.Lint.PublishingDiscipline.AllowCategoryMismatches)
		}),
		sc("lint.publishing_discipline.allow_undeclared_recipe_provides", func(c Config) string {
			return strconv.FormatBool(c.Lint.PublishingDiscipline.AllowUndeclaredRecipeProvides)
		}),
		sc("lint.publishing_discipline.allow_connection_credential_mismatches", func(c Config) string {
			return strconv.FormatBool(c.Lint.PublishingDiscipline.AllowConnectionCredentialMismatches)
		}),
		sc("parse_cache.max_entries", func(c Config) string { return strconv.Itoa(c.ParseCache.MaxEntries) }),
		sc("parse_cache.max_bytes", func(c Config) string { return strconv.Itoa(c.ParseCache.MaxBytes) }),
		sc("compiled_module_cache.max_bytes", func(c Config) string {
			return strconv.FormatInt(c.CompiledModuleCache.MaxBytes, 10)
		}),
		sc("concurrency.pool_size", func(c Config) string { return strconv.Itoa(c.Concurrency.PoolSize) }),
		sc("serve.build_on_inspect", func(c Config) string { return strconv.FormatBool(c.Serve.BuildOnInspect) }),
		sc("serve.lint_on_validate", func(c Config) string { return strconv.FormatBool(c.Serve.LintOnValidate) }),
		sc("mcp.transport", func(c Config) string { return c.MCP.Transport }),
		sc("mcp.listen", func(c Config) string { return c.MCP.Listen }),
		sc("mcp.tls.cert_file", func(c Config) string { return c.MCP.TLS.CertFile }),
		sc("mcp.tls.key_file", func(c Config) string { return c.MCP.TLS.KeyFile }),
		sc("mcp.limits.max_body_bytes", func(c Config) string {
			return strconv.FormatInt(c.MCP.Limits.MaxBodyBytes, 10)
		}),
		sc("mcp.limits.max_concurrent_requests", func(c Config) string {
			return strconv.Itoa(c.MCP.Limits.MaxConcurrentRequests)
		}),
		sc("mcp.limits.requests_per_minute", func(c Config) string {
			return strconv.Itoa(c.MCP.Limits.RequestsPerMinute)
		}),
		sc("mcp.authorization.issuer", func(c Config) string { return c.MCP.Authorization.Issuer }),
		sc("mcp.authorization.resource", func(c Config) string { return c.MCP.Authorization.Resource }),
		sc("mcp.authorization.jwks_file", func(c Config) string { return c.MCP.Authorization.JWKSFile }),
		sc("mcp.authorization.trusted_proxy.principal_header", func(c Config) string {
			return c.MCP.Authorization.TrustedProxy.PrincipalHeader
		}),
		sc("mcp.authorization.trusted_proxy.scopes_header", func(c Config) string {
			return c.MCP.Authorization.TrustedProxy.ScopesHeader
		}),
		sc("mcp.health.listen", func(c Config) string { return c.MCP.Health.Listen }),
		sc("signing.fulcio_url", func(c Config) string { return c.Signing.FulcioURL }),
		sc("signing.rekor_url", func(c Config) string { return c.Signing.RekorURL }),
		sc("signing.timestamp_url", func(c Config) string { return c.Signing.TimestampURL }),
		sc("signing.key_path", func(c Config) string { return c.Signing.KeyPath }),
		sc("signing.required", func(c Config) string { return strconv.FormatBool(c.Signing.Required) }),
		ls("mcp.allowed_origins", func(c Config) string { return joinStrings(c.MCP.AllowedOrigins) }),
		ls("mcp.deny_reach", func(c Config) string { return joinStrings(c.MCP.DenyReach) }),
		ls("mcp.announce_tools", func(c Config) string { return joinStrings(c.MCP.AnnounceTools) }),
		ls("signing.kinds", func(c Config) string { return joinStrings(c.Signing.Kinds) }),
		ls("workspace.catalog_dirs", func(c Config) string { return joinStrings(c.Workspace.CatalogDirs) }),
		ls(ReleaseCatalogsSetting, func(c Config) string { return joinReleaseCatalogs(c.Workspace.Releases) }),
		ls("plugins.governance", func(c Config) string { return joinPluginRefs(c.Plugins.Governance) }),
		ls("plugins.linter", func(c Config) string { return joinPluginRefs(c.Plugins.Linter) }),
		ls("plugins.importer", func(c Config) string { return joinPluginRefs(c.Plugins.Importer) }),
		ls("plugins.providers", func(c Config) string { return joinPluginRefs(c.Plugins.Providers) }),
		ls("plugins.cli", func(c Config) string { return joinCLIRefs(c.Plugins.CLI) }),
		ls("registries", func(c Config) string { return joinRegistries(c.Registries) }),
	}
}

// joinStrings renders a string slice as a stable bracketed list.
func joinStrings(v []string) string {
	if len(v) == 0 {
		return "[]"
	}
	return "[" + strings.Join(v, ", ") + "]"
}

// joinReleaseCatalogs renders the Release entries in the order the cascade
// composed them, each as its Release file and the directories the entry
// states: its own list, or `inherited` where it keeps the workspace's, followed
// by what it adds.
func joinReleaseCatalogs(entries types.OrderedMap[ReleaseCatalogConfig]) string {
	if entries.Len() == 0 {
		return "[]"
	}
	parts := make([]string, 0, entries.Len())
	for file, entry := range entries.All() {
		dirs := "inherited"
		if entry.CatalogDirs != nil {
			dirs = joinStrings(entry.CatalogDirs)
		}
		if len(entry.CatalogDirsAppend) > 0 {
			dirs += "+" + joinStrings(entry.CatalogDirsAppend)
		}
		parts = append(parts, file+"="+dirs)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// joinPluginRefs renders a plugin-attachment list by each entry's attachment
// identity (capability, uri, or both), preserving list order.
func joinPluginRefs(refs []PluginRef) string {
	if len(refs) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(refs))
	for _, r := range refs {
		switch {
		case r.Capability != "" && r.URI != "":
			parts = append(parts, r.Capability+"="+r.URI)
		case r.Capability != "":
			parts = append(parts, r.Capability)
		default:
			parts = append(parts, r.URI)
		}
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// joinCLIRefs renders a CLI-extension attachment list by each entry's uri.
func joinCLIRefs(refs []CLIPluginRef) string {
	if len(refs) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(refs))
	for _, r := range refs {
		parts = append(parts, r.URI)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// joinRegistries renders a registry list by host, preserving list order.
func joinRegistries(regs []RegistryConfig) string {
	if len(regs) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(regs))
	for _, r := range regs {
		parts = append(parts, r.Host)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

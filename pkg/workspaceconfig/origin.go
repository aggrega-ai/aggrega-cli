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

	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
	"go.yaml.in/yaml/v3"
)

// originDefaultsLabel is the source label a value carries when no config file or
// environment override changed it away from the built-in default.
const originDefaultsLabel = "(built-in defaults)"

// originAppendSep separates the base and append halves of a list field's
// change-detection fingerprint. It is a control character so it can never
// collide with a value a config file legitimately carries.
const originAppendSep = "\x1f"

// originEnvPrefix marks a source label as an environment override rather than a
// file path, and carries the variable's own name after it.
const originEnvPrefix = "env:"

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
// a config file's absolute path, an "env:AGGREGA_X" environment override, or the
// built-in-defaults label. Position locates the setting inside that config file.
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
	// no file at all, and a file layer may have contributed through a key the
	// setting's own path does not name. A consumer therefore separates "no
	// position" from "position at the very start of the file" by the nil check
	// alone: the first key of a document sits at line 1, column 1, which no
	// absent position resembles.
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
	// OriginBuiltInDefault reports a setting no layer changed away from the
	// value Default() seeds. Such a layer has no name.
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
// dotted name, whether it is a scalar leaf (attributed per-value) or a
// list/map field (attributed as a unit), the function that renders its
// effective value for display, and the function that fingerprints a per-layer
// overlay for change detection. For scalar leaves display and track read the
// same field; for list fields track additionally folds in the field's
// merge-time `_append` companion, so a layer that contributes only through the
// companion is still detected as a contributor.
type originField struct {
	name    string
	scalar  bool
	display func(Config) string
	track   func(Config) string
}

// ValueOrigins attributes each effective configuration setting to the layer
// that determined it, for a per-value provenance view. It replays the resolved
// cascade one layer at a time — the same ordered layers ContributingFiles
// reports, beneath them the built-in defaults — and, for each setting, records
// the last layer whose overlay changed it. Scalar leaves are attributed
// per-value; list and map fields are attributed as a whole to the last layer
// that changed them (per-element provenance is not tracked).
//
// The replay reuses the resolver's own per-layer primitives — loadOverlayInto
// for config files and applyEnvOverlay for environment overrides — so a scalar's
// attribution matches how the resolver actually merged it. The one setting whose
// effective value is not a plain per-layer whole-replace is signing.required,
// which composes monotonically (any layer that turns it on wins and no layer can
// turn it back off); it is attributed to the first layer that turns it on, which
// is the layer that made the effective value what it is.
//
// The displayed value of every setting is the effective value from the resolved
// config, so it always reflects what applies right now. Result order is a fixed
// field order, so the output is byte-stable for a given resolution.
//
// Each setting a config file was attributed to also carries the position of the
// key in that file which supplied it, resolved from the parsed document; a
// setting attributed to an environment override or to the built-in defaults
// carries no position at all. See ValueOrigin.Position.
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

	prev := Default()
	for _, entry := range r.files {
		if strings.HasPrefix(entry, "$") {
			varName := entry[1:]
			curr := prev
			if _, err := applyEnvOverlay(&curr, singleVarLookup(base, varName)); err != nil {
				return nil, err
			}
			attributeChanges(fields, source, prev, curr, originLayerLabel(entry), false)
			prev = curr
			continue
		}

		// A CI-owned overlay folds additively and cannot set a scalar, so it is
		// attributed only for the list fields it carries entries in, without
		// advancing the scalar replay (which would otherwise apply its ignored
		// scalars).
		if filepath.Base(entry) == AppendCIConfigFilename {
			raw, err := load(entry)
			if err != nil {
				return nil, err
			}
			empty := Config{}
			for _, f := range fields {
				if f.scalar {
					continue
				}
				if f.track(raw) != f.track(empty) {
					source[f.name] = entry
				}
			}
			continue
		}

		curr, err := loadOverlayInto(entry, prev)
		if err != nil {
			return nil, err
		}
		attributeChanges(fields, source, prev, curr, entry, false)
		prev = curr
	}

	// The command line is the top of the ladder, so it is replayed after every
	// file and every variable: a flag the reader set on this invocation is what
	// determined the setting whatever a layer beneath said. Each row is applied
	// on its own rather than the layer as a whole, so a setting is attributed to
	// the flag that actually determined it rather than to whichever flag the
	// table happens to list first.
	for _, b := range flagBindings {
		curr := prev
		if !b.apply(&curr, r.flags) {
			continue
		}
		attributeChanges(fields, source, prev, curr, b.label, false)
		prev = curr
	}

	// signing.required composes monotonically rather than whole-replacing per
	// layer, so its origin is the first layer that turns it on, not the last
	// layer that wrote it. This override keeps the reported source consistent
	// with the effective (monotonically-composed) value.
	source["signing.required"] = signingRequiredSource(r, base)

	positions, err := originPositions(fields, source)
	if err != nil {
		return nil, err
	}

	out := make([]ValueOrigin, 0, len(fields))
	for _, f := range fields {
		out = append(out, ValueOrigin{
			Field:    f.name,
			Value:    f.display(r.config),
			Source:   source[f.name],
			Position: positions[f.name],
		})
	}
	return out, nil
}

// originAppendSuffix is the sibling-key suffix a list field's additive
// companion carries — `catalog_dirs` alongside `catalog_dirs_append` — which is
// the convention the Config type documents. A field's companion path is derived
// from it rather than spelled out a second time here.
const originAppendSuffix = "_append"

// originLayer is one config document parsed for position resolution: the node
// tree, and the line index whose coordinate space its spans are read in.
// present is false for a layer with no document to resolve against — one that
// has gone missing, or one that is empty.
type originLayer struct {
	doc     *yaml.Node
	index   yamlpos.LineIndex
	present bool
}

// originPositions resolves, for every setting a config file was attributed to,
// the position of the key in that file which supplied the value. A setting
// attributed to a command-line flag, to an environment override or to the
// built-in defaults has no document to resolve against and is absent from the
// result, which is what makes a nil Position unambiguous rather than a
// coordinate to interpret. Each contributing file is read and parsed at most
// once however many settings it carries.
//
// The three document-less layers are skipped by name rather than left to fail
// the read they would otherwise attempt. A label that is not a path does reach
// the missing-file branch and does yield nothing, so the result would be the
// same either way — but only by accident, and an accident that holds is the
// kind that stops holding when the tolerance it leans on is tightened.
func originPositions(fields []originField, source map[string]string) (map[string]*yamlpos.FieldSpan, error) {
	layers := make(map[string]originLayer)
	out := make(map[string]*yamlpos.FieldSpan, len(fields))
	for _, f := range fields {
		path := source[f.name]
		if path == originDefaultsLabel ||
			strings.HasPrefix(path, originEnvPrefix) ||
			strings.HasPrefix(path, flagLayerPrefix) {
			continue
		}
		layer, seen := layers[path]
		if !seen {
			var err error
			layer, err = readOriginLayer(path)
			if err != nil {
				return nil, err
			}
			layers[path] = layer
		}
		if !layer.present {
			continue
		}
		if span := originPositionIn(layer, originDocumentPaths(f)); span != nil {
			out[f.name] = span
		}
	}
	return out, nil
}

// readOriginLayer reads and parses one config file so its keys can be located.
//
// A file that has gone missing between the cascade's read and this one yields a
// layer with nothing to resolve against rather than an error, because the
// cascade's own single-file load treats absence as the documented defaults: a
// position lookup must not be the thing that turns a working provenance view
// into a failing one. Every other read failure, and any parse failure, is
// reported.
func readOriginLayer(path string) (originLayer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return originLayer{}, nil
		}
		return originLayer{}, fmt.Errorf("workspaceconfig: read %s for its setting positions: %w", path, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return originLayer{}, fmt.Errorf("workspaceconfig: parse %s for its setting positions: %w", path, err)
	}
	if len(doc.Content) == 0 {
		return originLayer{}, nil
	}
	return originLayer{
		doc: &doc,
		// UTF-8 pins a span to the document's own coordinates — 1-based line
		// and 1-based byte column, the pair the parser records on the node —
		// so no editor-protocol encoding is baked into a configuration value.
		// A consumer wanting protocol coordinates converts through this same
		// index rather than reinterpreting the numbers.
		index:   yamlpos.BuildLineIndexWithEncoding(data, yamlpos.PositionEncodingUTF8),
		present: true,
	}, nil
}

// originDocumentPaths lists, in the order they are tried, the document key
// paths that could have supplied f's value.
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

// attributeChanges records label as the source of every field whose per-layer
// fingerprint differs between before and after. When scalarsOnly excludes a
// field it is skipped; the caller passes false here because both the file and
// environment layers can set the fields they are called for.
func attributeChanges(fields []originField, source map[string]string, before, after Config, label string, scalarsOnly bool) {
	for _, f := range fields {
		if scalarsOnly && !f.scalar {
			continue
		}
		if f.track(before) != f.track(after) {
			source[f.name] = label
		}
	}
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

// singleVarLookup returns a lookup that answers only for name (delegating to
// base) and returns empty for every other variable, so applyEnvOverlay applies
// exactly one environment override during a per-layer replay step.
func singleVarLookup(base func(string) string, name string) func(string) string {
	return func(k string) string {
		if k == name {
			return base(k)
		}
		return ""
	}
}

// originFields enumerates every attributed setting in a fixed order: the scalar
// leaves first (attributed per-value), then the list and map fields (attributed
// as a unit). The order is stable so ValueOrigins produces byte-identical output
// for a given resolution.
func originFields() []originField {
	sc := func(name string, get func(Config) string) originField {
		return originField{name: name, scalar: true, display: get, track: get}
	}
	ls := func(name string, display, track func(Config) string) originField {
		return originField{name: name, scalar: false, display: display, track: track}
	}
	return []originField{
		sc("default_registry", func(c Config) string { return c.DefaultRegistry }),
		sc(OfflineSettingKey, func(c Config) string { return strconv.FormatBool(c.Offline) }),
		sc("auto_pull", func(c Config) string { return strconv.FormatBool(c.AutoPull) }),
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
		sc("parse_cache.max_entries", func(c Config) string { return strconv.Itoa(c.ParseCache.MaxEntries) }),
		sc("parse_cache.max_bytes", func(c Config) string { return strconv.Itoa(c.ParseCache.MaxBytes) }),
		sc("compiled_module_cache.max_bytes", func(c Config) string {
			return strconv.FormatInt(c.CompiledModuleCache.MaxBytes, 10)
		}),
		sc("concurrency.pool_size", func(c Config) string { return strconv.Itoa(c.Concurrency.PoolSize) }),
		sc("serve.build_on_inspect", func(c Config) string { return strconv.FormatBool(c.Serve.BuildOnInspect) }),
		sc("serve.lint_on_validate", func(c Config) string { return strconv.FormatBool(c.Serve.LintOnValidate) }),
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
		ls("mcp.allowed_origins",
			func(c Config) string { return joinStrings(c.MCP.AllowedOrigins) },
			func(c Config) string { return joinStrings(c.MCP.AllowedOrigins) }),
		ls("signing.kinds",
			func(c Config) string { return joinStrings(c.Signing.Kinds) },
			func(c Config) string { return joinStrings(c.Signing.Kinds) }),
		ls("workspace.catalog_dirs",
			func(c Config) string { return joinStrings(c.Workspace.CatalogDirs) },
			func(c Config) string {
				return joinStrings(c.Workspace.CatalogDirs) + originAppendSep + joinStrings(c.Workspace.CatalogDirsAppend)
			}),
		ls("plugins.governance",
			func(c Config) string { return joinPluginRefs(c.Plugins.Governance) },
			func(c Config) string {
				return joinPluginRefs(c.Plugins.Governance) + originAppendSep + joinPluginRefs(c.Plugins.GovernanceAppend)
			}),
		ls("plugins.linter",
			func(c Config) string { return joinPluginRefs(c.Plugins.Linter) },
			func(c Config) string {
				return joinPluginRefs(c.Plugins.Linter) + originAppendSep + joinPluginRefs(c.Plugins.LinterAppend)
			}),
		ls("plugins.importer",
			func(c Config) string { return joinPluginRefs(c.Plugins.Importer) },
			func(c Config) string {
				return joinPluginRefs(c.Plugins.Importer) + originAppendSep + joinPluginRefs(c.Plugins.ImporterAppend)
			}),
		ls("plugins.providers",
			func(c Config) string { return joinPluginRefs(c.Plugins.Providers) },
			func(c Config) string {
				return joinPluginRefs(c.Plugins.Providers) + originAppendSep + joinPluginRefs(c.Plugins.ProvidersAppend)
			}),
		ls("plugins.cli",
			func(c Config) string { return joinCLIRefs(c.Plugins.CLI) },
			func(c Config) string { return joinCLIRefs(c.Plugins.CLI) }),
		ls("registries",
			func(c Config) string { return joinRegistries(c.Registries) },
			func(c Config) string { return joinRegistries(c.Registries) }),
	}
}

// joinStrings renders a string slice as a stable bracketed list.
func joinStrings(v []string) string {
	if len(v) == 0 {
		return "[]"
	}
	return "[" + strings.Join(v, ", ") + "]"
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

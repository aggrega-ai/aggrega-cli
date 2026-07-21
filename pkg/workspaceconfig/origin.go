// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// originDefaultsLabel is the source label a value carries when no config file or
// environment override changed it away from the built-in default.
const originDefaultsLabel = "(built-in defaults)"

// originAppendSep separates the base and append halves of a list field's
// change-detection fingerprint. It is a control character so it can never
// collide with a value a config file legitimately carries.
const originAppendSep = "\x1f"

// ValueOrigin pairs one effective configuration setting with the layer that
// determined it. Field is the setting's dotted config path, Value is its
// effective value rendered as a stable string, and Source is the origin layer:
// a config file's absolute path, an "env:AGGREGA_X" environment override, or the
// built-in-defaults label.
type ValueOrigin struct {
	Field  string
	Value  string
	Source string
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
			attributeChanges(fields, source, prev, curr, "env:"+varName, false)
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

	// signing.required composes monotonically rather than whole-replacing per
	// layer, so its origin is the first layer that turns it on, not the last
	// layer that wrote it. This override keeps the reported source consistent
	// with the effective (monotonically-composed) value.
	source["signing.required"] = signingRequiredSource(r, base)

	out := make([]ValueOrigin, 0, len(fields))
	for _, f := range fields {
		out = append(out, ValueOrigin{
			Field:  f.name,
			Value:  f.display(r.config),
			Source: source[f.name],
		})
	}
	return out, nil
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
					return "env:" + entry[1:]
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
		sc("offline", func(c Config) string { return strconv.FormatBool(c.Offline) }),
		sc("auto_pull", func(c Config) string { return strconv.FormatBool(c.AutoPull) }),
		sc("artifact_cache_dir", func(c Config) string { return c.ArtifactCacheDir }),
		sc("skip_signature_verification", func(c Config) string { return strconv.FormatBool(c.SkipSignatureVerification) }),
		sc("build.authoring_release", func(c Config) string { return c.Build.AuthoringRelease }),
		sc("build.halt_on_deprecated", func(c Config) string { return strconv.FormatBool(c.Build.HaltOnDeprecated) }),
		sc("build.halt_on_deprecated_severity", func(c Config) string { return c.Build.HaltOnDeprecatedSeverity }),
		sc("build.yaml_indent", func(c Config) string { return strconv.Itoa(c.Build.YAMLIndent) }),
		sc("lint.publishing_discipline.enforce_monotonic_narrowing", func(c Config) string {
			return strconv.FormatBool(c.Lint.PublishingDiscipline.EnforceMonotonicNarrowing)
		}),
		sc("lint.publishing_discipline.enforce_immutability", func(c Config) string {
			return strconv.FormatBool(c.Lint.PublishingDiscipline.EnforceImmutability)
		}),
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
		sc("parse_cache.max_entries", func(c Config) string { return strconv.Itoa(c.ParseCache.MaxEntries) }),
		sc("parse_cache.max_bytes", func(c Config) string { return strconv.Itoa(c.ParseCache.MaxBytes) }),
		sc("concurrency.pool_size", func(c Config) string { return strconv.Itoa(c.Concurrency.PoolSize) }),
		sc("serve.render_diagnostics", func(c Config) string { return strconv.FormatBool(c.Serve.RenderDiagnostics) }),
		sc("signing.fulcio_url", func(c Config) string { return c.Signing.FulcioURL }),
		sc("signing.rekor_url", func(c Config) string { return c.Signing.RekorURL }),
		sc("signing.timestamp_url", func(c Config) string { return c.Signing.TimestampURL }),
		sc("signing.key_path", func(c Config) string { return c.Signing.KeyPath }),
		sc("signing.required", func(c Config) string { return strconv.FormatBool(c.Signing.Required) }),
		ls("signing.kinds",
			func(c Config) string { return joinStrings(c.Signing.Kinds) },
			func(c Config) string { return joinStrings(c.Signing.Kinds) }),
		ls("workspace.catalog_dirs",
			func(c Config) string { return joinStrings(c.Workspace.CatalogDirs) },
			func(c Config) string {
				return joinStrings(c.Workspace.CatalogDirs) + originAppendSep + joinStrings(c.Workspace.CatalogDirsAppend)
			}),
		ls("plugins.mutator",
			func(c Config) string { return joinPluginRefs(c.Plugins.Mutator) },
			func(c Config) string {
				return joinPluginRefs(c.Plugins.Mutator) + originAppendSep + joinPluginRefs(c.Plugins.MutatorAppend)
			}),
		ls("plugins.policy",
			func(c Config) string { return joinPluginRefs(c.Plugins.Policy) },
			func(c Config) string {
				return joinPluginRefs(c.Plugins.Policy) + originAppendSep + joinPluginRefs(c.Plugins.PolicyAppend)
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
		ls("lint.publishing_discipline.narrowing_exemptions",
			func(c Config) string { return joinExemptions(c.Lint.PublishingDiscipline.NarrowingExemptions) },
			func(c Config) string {
				return joinExemptions(c.Lint.PublishingDiscipline.NarrowingExemptions) + originAppendSep + joinExemptions(c.Lint.PublishingDiscipline.NarrowingExemptionsAppend)
			}),
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

// joinExemptions renders a narrowing-exemption list by each entry's entity,
// preserving list order.
func joinExemptions(ex []NarrowingExemption) string {
	if len(ex) == 0 {
		return "[]"
	}
	parts := make([]string, 0, len(ex))
	for _, e := range ex {
		parts = append(parts, e.Entity)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

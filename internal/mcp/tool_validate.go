// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/internal/validateops"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// validateArgs is the validate tool's argument shape. It mirrors the CLI verb
// 1:1: exactly one of release / entity / plugin / catalog selects the scope,
// with the same mutual-exclusion rules the CLI enforces. type is meaningful
// only alongside plugin (binary-only declared type); entity alongside plugin is
// the cross-check Plugin entity, not a single-entity target.
type validateArgs struct {
	Release        string   `json:"release"`
	Entity         string   `json:"entity"`
	Plugin         string   `json:"plugin"`
	Type           string   `json:"type"`
	Catalog        bool     `json:"catalog"`
	WorkspaceRoot  string   `json:"workspace_root"`
	CatalogDirs    []string `json:"catalog_dirs"`
	ResponseFormat string   `json:"response_format"`
}

// validate scope discriminators. Exactly one is reported per call in the
// payload's Scope field so a consumer can branch on the scope that ran.
const (
	validateScopeRelease = "release"
	validateScopeEntity  = "entity"
	validateScopePlugin  = "plugin"
	validateScopeCatalog = "catalog"
)

// pluginValidateMaxMemoryPages is the linear-memory cap applied when validating
// a .wasm's describe() self-report and a Plugin entity declares no runtime cap
// (or in binary-only mode). It matches the cap the CLI applies for the same
// pre-publish authoring check, so both front doors describe the binary under an
// identical memory budget.
const pluginValidateMaxMemoryPages uint32 = 4096

// validateResultPayload is the validate tool's structured payload, uniform
// across every scope. Scope names which scope ran; Target is the path or
// identifier validated; Valid is true exactly when Diagnostics is empty, so a
// consumer reads validity from either field consistently across scopes.
// Diagnostics is problems-only — each entry is one blocking finding in the order
// the scope's core produces them. Release is retained and populated on the
// release scope for callers that read it; other scopes omit it. Artifacts
// carries the sorted render-artifact keys from the single-entity render tier as
// a summary — the artifact bytes stay the province of the build / describe /
// inspect tools and never travel in this payload. Notices carries advisory lines
// that never fail the verdict.
type validateResultPayload struct {
	Scope       string   `json:"scope"`
	Target      string   `json:"target"`
	Release     string   `json:"release,omitempty"`
	Valid       bool     `json:"valid"`
	Diagnostics []string `json:"diagnostics"`
	Artifacts   []string `json:"artifacts,omitempty"`
	Notices     []string `json:"notices,omitempty"`
}

var validateToolSpec = toolSpec{
	name:  "validate",
	kinds: []string{"component", "feature", "featurerecipe", "plugin", "recipe", "release", "workload"},
	description: "Check correctness without rendering or writing anything, across four scopes that mirror the CLI. " +
		"Pass exactly one scope selector. `release` <path> runs the closure-scoped semantic-validation pipeline over " +
		"a Release: it resolves the Release's Manifest and Aggregate, computes the catalog closure, and runs the " +
		"closure-scoped extends and schema-pillar gates, the resolve/topology/IR pipeline, and the resolution-" +
		"completeness checks. `entity` <path> checks one catalog entity file: all kinds are schema- and shape-checked, " +
		"a Component or Feature additionally has its pillar structure checked, and a Recipe is driven through the real " +
		"compilation pipeline around a synthesized minimal deployment so you see the isolated render summary; a Plugin " +
		"entity is schema-checked only. `plugin` <path> validates a raw .wasm against its describe() self-report — pass " +
		"`type` for a binary-only check, or `entity` (a Plugin entity path) to cross-check the binary against the " +
		"entity's declared surface. `catalog` true checks the whole catalog: schema-pillar structure, library " +
		"resolution, extends chains, OCI-location collisions, and provider input-kind capabilities. Each scope " +
		"dispatches to the same core the CLI validates with and returns whether the target is valid plus each " +
		"diagnostic verbatim. The selectors are mutually exclusive: `type` needs `plugin`; `plugin` excludes `release`; " +
		"`release` excludes a single-entity `entity`; and exactly one scope must be chosen. For a diagnostic you do " +
		"not understand, pass its text to explain-error. Optional `workspace_root` and `catalog_dirs` override " +
		"workspace and catalog discovery; `response_format` chooses detailed or concise (release scope).",
	inputSchema: `{
  "type": "object",
  "properties": {
    "release": {
      "type": "string",
      "description": "Path to a Release YAML to validate over its resolved dependency closure. Mutually exclusive with entity, plugin, and catalog."
    },
    "entity": {
      "type": "string",
      "description": "Path to a single catalog entity file (Component, Feature, Recipe, FeatureRecipe, Workload, or Plugin) to schema- and shape-check. When combined with plugin, this is instead the Plugin entity cross-checked against the binary."
    },
    "plugin": {
      "type": "string",
      "description": "Path to a raw .wasm to validate against its describe() self-report. Needs type for a binary-only check, or entity (a Plugin entity path) to cross-check against the entity's declared surface. Mutually exclusive with release and catalog."
    },
    "type": {
      "type": "string",
      "description": "Plugin type for a binary-only plugin check, verified against the binary's self-reported type. Meaningful only with plugin; when a Plugin entity supplies the type, omit it (a disagreeing value halts)."
    },
    "catalog": {
      "type": "boolean",
      "description": "When true, validate the whole catalog: schema-pillar structure, library resolution, extends chains, OCI-location collisions, and provider input-kind capabilities. Mutually exclusive with the other scope selectors."
    },
    "workspace_root": {
      "type": "string",
      "description": "Optional workspace root override. Defaults to discovery from the server's working directory."
    },
    "catalog_dirs": {
      "type": "array",
      "items": { "type": "string" },
      "description": "Optional catalog directories, prepended (first-match-wins) to the workspace catalog search stack."
    },
    "response_format": {
      "type": "string",
      "enum": ["detailed", "concise"],
      "description": "detailed returns every diagnostic; concise returns the count and the first. Applies to the release scope; defaults to detailed."
    }
  }
}`,
	invoke: validateInvoke,
}

// validateInvoke selects the scope from the mutually-exclusive selectors and
// dispatches to that scope's core: type needs plugin, plugin excludes release
// and catalog, release excludes a single-entity target and catalog, catalog
// excludes a single-entity target, and exactly one scope must be chosen. When
// plugin is set, entity is the cross-check Plugin entity rather than a
// single-entity target. Argument-combination failures return codeInvalidParams;
// scope-operation failures return an error result.
func validateInvoke(s *server, ctx context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
	var a validateArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return nil, invalidToolArguments("validate", err)
		}
	}
	release := strings.TrimSpace(a.Release)
	entity := strings.TrimSpace(a.Entity)
	plugin := strings.TrimSpace(a.Plugin)
	pluginType := strings.TrimSpace(a.Type)

	// type declares the plugin type for a binary-only .wasm check and is
	// meaningful only alongside plugin; reject it on its own so it never silently
	// no-ops on another scope.
	if pluginType != "" && plugin == "" {
		return nil, &rpcError{Code: codeInvalidParams, Message: "validate 'type' applies only with 'plugin'"}
	}

	// Plugin scope. It selects neither the catalog nor a release closure, and
	// entity (when present) is the cross-check Plugin entity.
	if plugin != "" {
		if release != "" {
			return nil, &rpcError{Code: codeInvalidParams, Message: "validate takes exactly one scope: 'plugin' and 'release' are mutually exclusive"}
		}
		if a.Catalog {
			return nil, &rpcError{Code: codeInvalidParams, Message: "validate takes exactly one scope: 'plugin' and 'catalog' are mutually exclusive"}
		}
		return validatePluginScope(s, a, plugin, pluginType, entity)
	}

	// Release scope. Mutually exclusive with a single-entity target and with
	// whole-catalog validation.
	if release != "" {
		if entity != "" {
			return nil, &rpcError{Code: codeInvalidParams, Message: "validate takes exactly one scope: 'release' and 'entity' are mutually exclusive"}
		}
		if a.Catalog {
			return nil, &rpcError{Code: codeInvalidParams, Message: "validate takes exactly one scope: 'release' and 'catalog' are mutually exclusive"}
		}
		return validateReleaseScope(s, ctx, a, release)
	}

	// Whole-catalog scope.
	if a.Catalog {
		if entity != "" {
			return nil, &rpcError{Code: codeInvalidParams, Message: "validate takes exactly one scope: 'catalog' and 'entity' are mutually exclusive"}
		}
		return validateCatalogScope(s, a)
	}

	// Single-entity scope.
	if entity != "" {
		return validateEntityScope(s, a, entity)
	}

	return nil, &rpcError{Code: codeInvalidParams, Message: "validate requires exactly one of 'release', 'entity', 'plugin', or 'catalog'"}
}

// validateReleaseScope runs the release-scoped semantic-validation pipeline
// through the shared compiler-operations layer — the same core the CLI validates
// a Release with — and reports each diagnostic in gate order.
func validateReleaseScope(s *server, ctx context.Context, a validateArgs, release string) (*callToolResult, *rpcError) {
	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, a.CatalogDirs)
	if err != nil {
		return errorResult(explainToolError("Could not resolve the workspace for validation.", err, "release"))
	}
	releasePath := tw.absReleasePath(release)
	lf := loadReleaseLockfile(releasePath)
	view := tw.newView(lf)

	resolved, cerr := compilerops.ResolveValidationClosure(compilerops.ValidationClosureInputs{
		View:            view,
		ReleasePath:     releasePath,
		WorkspaceRoot:   tw.root,
		WorkspaceConfig: tw.cfg,
		Lockfile:        lf,
	})
	if cerr != nil {
		return errorResult(explainToolError(
			"Validation could not resolve the Release closure; fix the resolution failure before semantic validation runs.",
			cerr, closureStageKind(cerr.Stage)))
	}

	vr := compilerops.Validate(ctx, compilerops.ValidateInputs{
		View:            view,
		Closure:         resolved.Closure,
		Release:         resolved.Release,
		ReleasePath:     releasePath,
		Manifest:        resolved.Manifest,
		AggBytes:        resolved.AggBytes,
		AggregatePath:   resolved.AggregatePath,
		WorkspaceConfig: tw.cfg,
		CompilerVersion: s.serverVersion,
	})

	diags := validateDiagnostics(vr, resolved.ManifestConsistencyFindings)
	payload := validateResultPayload{
		Scope:       validateScopeRelease,
		Target:      releasePath,
		Release:     releasePath,
		Valid:       len(diags) == 0,
		Diagnostics: diags,
		Notices:     releaseAdvisories(vr, lf),
	}
	text := renderValidateText(payload, parseResponseFormat(a.ResponseFormat))
	return textResult(text, payload)
}

// validateEntityScope runs the single-entity scope core over one catalog entity
// file, resolving the entity path against the workspace root as the release path
// resolves. Diagnostics is problems-only: the single blocking error when the
// entity does not validate (the core halts at the first), empty when it does.
// The render-artifact keys are summarized in the payload; the bytes are not
// carried here — those belong to the build / describe / inspect tools.
func validateEntityScope(s *server, a validateArgs, entity string) (*callToolResult, *rpcError) {
	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, a.CatalogDirs)
	if err != nil {
		return errorResult(explainToolError("Could not resolve the workspace for validation.", err, "component"))
	}
	entityPath := tw.absReleasePath(entity)

	res, verr := validateops.ValidateEntity(tw.root, tw.cacheDir, tw.dirs, entityPath, false)

	var diags []string
	if verr != nil {
		diags = []string{verr.Error()}
	}
	artifactKeys := make([]string, 0, len(res.Artifacts))
	for _, art := range res.Artifacts {
		artifactKeys = append(artifactKeys, art.Key)
	}
	payload := validateResultPayload{
		Scope:       validateScopeEntity,
		Target:      entityPath,
		Valid:       len(diags) == 0,
		Diagnostics: diags,
		Artifacts:   artifactKeys,
		Notices:     res.Notes,
	}
	return textResult(renderEntityText(res, artifactKeys), payload)
}

// validatePluginScope runs the plugin-binary scope core: a raw .wasm checked
// against its own describe() self-report, and (when a Plugin entity path is
// supplied) cross-checked against the entity's declared surface. The .wasm path
// and the cross-check entity path resolve against the workspace root, so a client
// that does not know the server's working directory can pass workspace-relative
// paths as the other scopes do. Diagnostics is problems-only: the single blocking
// error when the binary does not validate, empty when it does.
func validatePluginScope(s *server, a validateArgs, plugin, pluginType, entity string) (*callToolResult, *rpcError) {
	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, a.CatalogDirs)
	if err != nil {
		return errorResult(explainToolError("Could not resolve the workspace for validation.", err, "plugin"))
	}
	pluginPath := tw.absReleasePath(plugin)
	entityPath := entity
	if entity != "" {
		entityPath = tw.absReleasePath(entity)
	}

	res, verr := validateops.ValidatePluginBinary(pluginPath, pluginType, entityPath, pluginValidateMaxMemoryPages)

	var diags []string
	if verr != nil {
		diags = []string{verr.Error()}
	}
	target := pluginPath
	if entity != "" {
		target = entityPath
	}
	payload := validateResultPayload{
		Scope:       validateScopePlugin,
		Target:      target,
		Valid:       len(diags) == 0,
		Diagnostics: diags,
		Notices:     res.Notices,
	}
	return textResult(renderPluginText(res, verr), payload)
}

// validateCatalogScope runs the whole-catalog correctness pass core. Diagnostics
// is problems-only and carries the FULL ordered category detail — one entry per
// extends error, the OCI-location collision, and one entry per input-kind
// capability error — never a collapsed count. Exactly one failing category is
// populated by the core, in its halt-at-first order. Advisory notices never fail
// the verdict and travel in Notices.
func validateCatalogScope(s *server, a validateArgs) (*callToolResult, *rpcError) {
	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, a.CatalogDirs)
	if err != nil {
		return errorResult(explainToolError("Could not resolve the workspace for catalog validation.", err, "component"))
	}
	res, cerr := validateops.ValidateCatalog(tw.root, tw.cacheDir, tw.dirs)
	if cerr != nil {
		return errorResult(explainToolError("Catalog validation could not load the catalog.", cerr, "component"))
	}

	var diags []string
	for _, e := range res.ExtendsErrs {
		diags = append(diags, "extends: "+e.Error())
	}
	if res.OCICollision != nil {
		diags = append(diags, "oci-location: "+res.OCICollision.Error())
	}
	for _, e := range res.InputKindErrs {
		diags = append(diags, "input-kind: "+e.Error())
	}
	payload := validateResultPayload{
		Scope:       validateScopeCatalog,
		Target:      tw.root,
		Valid:       len(diags) == 0,
		Diagnostics: diags,
		Notices:     res.Notices,
	}
	return textResult(renderCatalogText(payload), payload)
}

// validateDiagnostics flattens a ValidateResult into the ordered list of
// diagnostic strings, in the order the semantic-validation gates run: the
// Manifest mapping-table findings, then closure-scoped extends, then the
// schema-pillar gate, then the resolve/analyze pipeline, then the
// resolution-completeness findings, then the input-kind capability check computed
// after the pipeline. It surfaces each error verbatim — the same text the CLI
// validation produces — so a consumer reads the compiler's own message. The gate
// order matches the shared layer's documented field order, so this is the single
// mapping every front end shares.
//
// consistencyFindings comes from the closure resolution rather than from vr,
// because the Manifest is judged before this result's first gate runs. It is a
// parameter rather than a field so a caller cannot omit it and report a Manifest
// as sound without having asked; there is no value here meaning "not checked".
func validateDiagnostics(vr *compilerops.ValidateResult, consistencyFindings []error) []string {
	var out []string
	for _, f := range consistencyFindings {
		out = append(out, f.Error())
	}
	for _, e := range vr.ClosureExtendsErrors {
		out = append(out, "catalog extends: "+e.Error())
	}
	if vr.PillarError != nil {
		out = append(out, vr.PillarError.Error())
	}
	// The verdict-plugin trust gate: the workspace configuration disagreed with the
	// record of the plugin identities trusted for its verdict attachments, so no
	// gate ran and nothing past this point was validated.
	if vr.VerdictTrustError != nil {
		out = append(out, vr.VerdictTrustError.Error())
	}
	if vr.Tier4 != nil {
		for _, ve := range vr.Tier4.ResolveDiagnostics {
			out = append(out, ve.Error())
		}
		if vr.Tier4.Analysis != nil {
			for _, ae := range vr.Tier4.Analysis.Errors {
				out = append(out, ae.Error())
			}
		}
	}
	// A non-resolver pre-analyzer phase error carries no per-violation structure;
	// surface it only when the resolver produced no structured diagnostic, so a
	// resolver failure is not double-reported. Cancellation is not a diagnostic.
	if vr.RunErr != nil && !isCancellation(vr.RunErr) &&
		(vr.Tier4 == nil || len(vr.Tier4.ResolveDiagnostics) == 0) {
		out = append(out, vr.RunErr.Error())
	}
	for _, f := range vr.ResolutionFindings {
		out = append(out, f.Error())
	}
	for _, e := range vr.InputKindErrors {
		out = append(out, "input-kind: "+e.Error())
	}
	return out
}

// releaseAdvisories collects the exit-code-neutral advisory lines in the order
// the facts were decided — the deprecation report over the lockfile's recorded
// markers, then the schema-pillar overlap advisories, then the narrowing-on-extend
// advisories. These never change the verdict; they travel in the payload's Notices
// field for callers and the detailed render form.
//
// The deprecation report leads because its facts predate every gate in vr: the
// markers were recorded at the pull that wrote the lockfile. It is the same shared
// report the build reads, so this tool names the same references rather than a
// subset — and it is advisory here for the reason the build's own default records:
// a marker is registry state applied after the version was pinned, and the dial
// that turns it into a halt governs the build alone.
//
// lf is a parameter rather than something read from vr because the shared
// validation core takes no lockfile. A nil lockfile — an absent or unreadable one
// — carries no recorded markers, so it contributes nothing and needs no guard at
// the call site.
func releaseAdvisories(vr *compilerops.ValidateResult, lf *lockfile.Lockfile) []string {
	deprecations := lf.DeprecationFindings()
	if len(deprecations) == 0 && len(vr.LintWarnings) == 0 && len(vr.NarrowingWarnings) == 0 {
		return nil
	}
	out := make([]string, 0, len(deprecations)+len(vr.LintWarnings)+len(vr.NarrowingWarnings))
	for _, f := range deprecations {
		out = append(out, "deprecation: "+f.Notice())
	}
	out = append(out, vr.LintWarnings...)
	out = append(out, vr.NarrowingWarnings...)
	return out
}

// isCancellation reports whether err is a shutdown-drain cancellation, for
// which validation yields no diagnostics.
func isCancellation(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// renderValidateText renders the human-readable release-scope validate result.
// The concise form reports the count and the first diagnostic and emits no
// advisory lines; the detailed form lists every diagnostic and then the advisory
// notices. Verbosity governs output volume only — the payload's verdict (Valid)
// is identical across both forms.
func renderValidateText(p validateResultPayload, format string) string {
	if p.Valid {
		clean := fmt.Sprintf("Release %s validated clean: no diagnostics.", p.Release)
		if format == responseFormatConcise || len(p.Notices) == 0 {
			return clean
		}
		var b strings.Builder
		b.WriteString(clean)
		for _, n := range p.Notices {
			fmt.Fprintf(&b, "\n  notice: %s", n)
		}
		return b.String()
	}
	if format == responseFormatConcise {
		return fmt.Sprintf("Release %s has %d validation diagnostic(s). First: %s",
			p.Release, len(p.Diagnostics), p.Diagnostics[0])
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Release %s has %d validation diagnostic(s):\n", p.Release, len(p.Diagnostics))
	for i, d := range p.Diagnostics {
		fmt.Fprintf(&b, "  %d. %s\n", i+1, d)
	}
	for _, n := range p.Notices {
		fmt.Fprintf(&b, "  notice: %s\n", n)
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderEntityText renders the human-readable single-entity result: the core's
// ordered report lines (identity header, per-tier status, and the render-failure
// block when the isolated render did not complete), followed by a render-artifact
// key summary when the render emitted artifacts. Artifact bytes are never printed
// here — the summary names the keys only.
func renderEntityText(res validateops.EntityResult, artifactKeys []string) string {
	var b strings.Builder
	for _, line := range res.Diagnostics {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if len(artifactKeys) > 0 {
		fmt.Fprintf(&b, "Artifacts: %s\n", strings.Join(artifactKeys, ", "))
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderPluginText renders the human-readable plugin-binary result: the core's
// ordered report lines (identity header, per-check status, advisory notices),
// followed by the blocking error when the binary did not validate.
func renderPluginText(res validateops.PluginBinaryResult, verr error) string {
	var b strings.Builder
	for _, line := range res.Diagnostics {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	if verr != nil {
		b.WriteString(verr.Error())
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderCatalogText renders the human-readable whole-catalog result: the full
// ordered per-category diagnostic lines and a success/failure summary. The
// count-summary phrasing is composed here at the render seam; the payload carries
// each category error as its own diagnostic entry.
func renderCatalogText(p validateResultPayload) string {
	if p.Valid {
		return fmt.Sprintf("Catalog %s validated clean: no diagnostics.", p.Target)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Catalog %s has %d validation diagnostic(s):\n", p.Target, len(p.Diagnostics))
	for i, d := range p.Diagnostics {
		fmt.Fprintf(&b, "  %d. %s\n", i+1, d)
	}
	return strings.TrimRight(b.String(), "\n")
}

// closureStageKind maps a closure-resolution stage to the entity kind whose
// documentation a failure at that stage points at.
func closureStageKind(stage compilerops.ClosureStage) string {
	switch stage {
	case compilerops.ClosureStageResolveManifest, compilerops.ClosureStageComposeManifest,
		compilerops.ClosureStageManifestReferenceShape:
		return "manifest"
	case compilerops.ClosureStageLoadAggregate:
		return "aggregate"
	default:
		return "release"
	}
}

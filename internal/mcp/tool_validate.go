// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/internal/serve"
	"github.com/aggrega-ai/aggrega-cli/internal/validateops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// validateArgs is the validate tool's argument shape. It mirrors the CLI verb
// 1:1: exactly one of release / entity / plugin / catalog selects the scope,
// with the same mutual-exclusion rules the CLI enforces. type is meaningful
// only alongside plugin (binary-only declared type); entity alongside plugin is
// the cross-check Plugin entity, not a single-entity target; and release
// alongside entity names the Release whose terms that entity is checked in,
// not a closure to validate.
type validateArgs struct {
	Release       string   `json:"release"`
	Entity        string   `json:"entity"`
	Plugin        string   `json:"plugin"`
	Type          string   `json:"type"`
	Catalog       bool     `json:"catalog"`
	WorkspaceRoot string   `json:"workspace_root"`
	CatalogDirs   []string `json:"catalog_dirs"`

	// ResponseFormat is a pointer so that a value a caller sent is
	// distinguishable from one it left out. Every value outside the declared set
	// is refused, the blank string among them, and only an absent argument takes
	// the default — so a plain string would make a refused value and an absent one
	// the same bytes at exactly the point the two have to be told apart.
	ResponseFormat *string `json:"response_format"`
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
// release scope for callers that read it, and on the entity scope when the
// entity was checked in a Release's terms; other scopes omit it. Artifacts
// carries the sorted render-artifact keys from the single-entity render tier as
// a summary — the artifact bytes stay the province of the build / describe /
// inspect tools and never travel in this payload. Notices carries advisory lines
// that never fail the verdict.
//
// Target and Release are locations on this host, spelled as the caller may be
// told them: a location the caller is not told is absent and marked by the
// withheld flag beside it, which is omitted where nothing was kept back. The
// catalog scope's target is the workspace root itself, which is such a location
// for a caller reaching a listener.
//
// Passes is carried on the catalog scope only, and only in a workspace whose
// configuration gives its Releases catalog directories of their own, where the
// catalog is checked once for each Release over the directories that Release
// reads. Valid, Diagnostics and Notices then cover every pass together, in pass
// order, so a caller that reads only those reads the whole verdict, and Passes
// says which Releases each diagnostic was found for. Every other answer omits it.
type validateResultPayload struct {
	Scope           string  `json:"scope"`
	Target          *string `json:"target"`
	TargetWithheld  bool    `json:"target_withheld,omitempty"`
	Release         *string `json:"release,omitempty"`
	ReleaseWithheld bool    `json:"release_withheld,omitempty"`

	Valid       bool     `json:"valid"`
	Diagnostics []string `json:"diagnostics"`
	Artifacts   []string `json:"artifacts,omitempty"`
	Notices     []string `json:"notices,omitempty"`

	Passes []validateCatalogPass `json:"passes,omitempty"`
}

// validateCatalogPass is one pass of the catalog scope in a workspace that gives
// its Releases catalog directories of their own: the Releases it was run for, the
// directories it read, and its own verdict over them.
//
// Releases are spelled relative to the workspace root, and Releases reading the
// same directories share one pass. Each catalog directory is spelled as the
// caller may be told a location: one the caller is not told is null in its
// place, so every other directory keeps its position, and CatalogDirsWithheld
// marks the pass. An empty CatalogDirs is a pass whose Releases read no catalog
// directory: it checked nothing, and it is valid because nothing it read was
// wrong. Neither list is ever null.
type validateCatalogPass struct {
	Releases            []string  `json:"releases"`
	CatalogDirs         []*string `json:"catalog_dirs"`
	CatalogDirsWithheld bool      `json:"catalog_dirs_withheld,omitempty"`
	Valid               bool      `json:"valid"`
	Diagnostics         []string  `json:"diagnostics"`
	Notices             []string  `json:"notices,omitempty"`
}

var validateToolSpec = toolSpec{
	kinds:   []string{"component", "feature", "featurerecipe", "plugin", "recipe", "release", "workload"},
	summary: "Checks correctness across a workspace, a Release, or one entity.",
	description: "Check correctness without rendering or writing anything, across four scopes that mirror the CLI. " +
		"Pass exactly one scope selector. `release` <path> runs the closure-scoped semantic-validation pipeline over " +
		"a Release: it resolves the Release's Manifest and Aggregate, computes the catalog closure, and runs the " +
		"closure-scoped extends and schema-pillar gates, the resolve/topology/IR pipeline, the Recipe and FeatureRecipe " +
		"assertions evaluated against the Release's values as a build evaluates them before it renders, and the " +
		"resolution-completeness checks. `entity` <path> checks one catalog entity file: a value its kind requires that the file " +
		"leaves empty is reported first, every one at once, as a placeholder still owed with what it takes; then every " +
		"kind is schema-checked, a Component or Feature has its pillar structure checked, a Workload, FeatureRecipe or " +
		"Plugin has its shape checked, and a Recipe is driven through the real compilation pipeline around a " +
		"synthesized Aggregate, Manifest and Release so you see the isolated render summary. That synthesized build " +
		"wires no connection and attaches no Feature, so a Recipe whose render stops while its templates read anything " +
		"under .context.inject, .context.uses or .features is reported as not attempted rather than failed: valid, " +
		"with no diagnostic, and the text naming those reads and the sentence the render stopped on. What decides it is " +
		"what the Recipe reads, so a fault of its own beside such a read is set aside with it; a template that does " +
		"not parse and a reference the render refuses still fail. An assertion that calls fail, the Recipe's own or one its " +
		"Component declares, answers valid with no diagnostic and its failure in `notices`, closed by a sentence saying a " +
		"Release must satisfy it before a build can pass: the synthesized Release sets none of a real Release's values, so " +
		"the check cannot tell an assertion waiting on one from one that is broken, and a build still halts on it. " +
		"Any other kind is refused, " +
		"naming the kinds `entity` accepts and the scope that checks it instead. `entity` with `release` runs those same " +
		"checks in that Release's terms: the entity's references — the Component a Recipe implements, a library a pillar " +
		"refers to — resolve through the catalog directories the workspace configuration gives that Release rather than " +
		"the workspace's own, and no closure is validated. `plugin` <path> validates a raw .wasm against its describe() self-report — pass " +
		"`type` for a binary-only check, or `entity` (a Plugin entity path) to cross-check the binary against the " +
		"entity's declared surface. `catalog` true checks the whole catalog: schema-pillar structure, library " +
		"resolution, extends chains, OCI-location collisions, and provider input-kind capabilities — over the " +
		"workspace's catalog directories, or, in a workspace whose configuration gives its Releases catalog " +
		"directories of their own under workspace.releases, once for each Release over the directories that " +
		"Release reads, the answer's `passes` saying which Releases each pass covered and what it found. Each scope " +
		"dispatches to the same core the CLI validates with and returns whether the target is valid plus each " +
		"diagnostic verbatim. The selectors are mutually exclusive: `type` needs `plugin`; `plugin` excludes `release`; " +
		"`catalog` takes no other selector; and exactly one scope must be chosen, `entity` with `release` " +
		"being the entity scope. For a diagnostic you do not understand, pass its text to explain-error. " +
		"`response_format` chooses detailed or concise (release scope, with no `entity`).",
	workspaceSelectionNote: "Optional `workspace_root` and `catalog_dirs` override workspace and catalog discovery.",
	listenerNote: "You did not start this server, so a diagnostic naming a place on its host names it relative to " +
		"the workspace root where it lies inside the workspace, and reads " + keptBackLocationWords + " where it " +
		"does not; the catalog scope's `target` is the workspace root itself, and is absent and marked " +
		"`target_withheld`.",
	inputSchema: `{
  "type": "object",
  "properties": {
    "release": {
      "type": "string",
      "minLength": 1,
      "description": "Path to a Release YAML to validate over its resolved dependency closure. With entity, the Release whose catalog directories the entity's references resolve through instead; no closure is validated."
    },
    "entity": {
      "type": "string",
      "minLength": 1,
      "description": "Path to a single catalog entity file (Component, Feature, Recipe, FeatureRecipe, Workload, or Plugin) to schema- and shape-check. With release, it is checked in that Release's terms. When combined with plugin, this is instead the Plugin entity cross-checked against the binary."
    },
    "plugin": {
      "type": "string",
      "minLength": 1,
      "description": "Path to a raw .wasm to validate against its describe() self-report. Needs type for a binary-only check, or entity (a Plugin entity path) to cross-check against the entity's declared surface."
    },
    "type": {
      "type": "string",
      "minLength": 1,
      "description": "Plugin type for a binary-only plugin check, verified against the binary's self-reported type. When a Plugin entity supplies the type, omit it (a disagreeing value halts)."
    },
    "catalog": {
      "type": "boolean",
      "description": "When true, validate the whole catalog: schema-pillar structure, library resolution, extends chains, OCI-location collisions, and provider input-kind capabilities. A workspace that gives its Releases catalog directories of their own is validated once for each Release, over the directories that Release reads."
    },
    "workspace_root": {
      "type": "string",
      "description": "Optional workspace root override. Defaults to the workspace root the server was launched with, or, where it was launched with none, to discovery from its working directory."
    },
    "catalog_dirs": {
      "type": "array",
      "items": { "type": "string" },
      "description": "Optional catalog directories (absolute, or relative to the workspace root), prepended (first-match-wins) to the workspace catalog search stack."
    },
    "response_format": {
      "type": "string",
      "enum": ["detailed", "concise"],
      "description": "detailed returns every diagnostic; concise returns the count and the first. Applies to the release scope with no entity; defaults to detailed."
    }
  },
  "anyOf": [
    {"required":["release"],"properties":{"catalog":{"const":false}},"not":{"anyOf":[{"required":["entity"]},{"required":["plugin"]},{"required":["type"]}]}},
    {"required":["entity"],"properties":{"catalog":{"const":false}},"not":{"anyOf":[{"required":["plugin"]},{"required":["type"]}]}},
    {"required":["plugin"],"properties":{"catalog":{"const":false}},"not":{"anyOf":[{"required":["release"]},{"required":["entity"]}]}},
    {"required":["plugin","entity"],"properties":{"catalog":{"const":false}},"not":{"required":["release"]}},
    {"required":["catalog"],"properties":{"catalog":{"const":true}},"not":{"anyOf":[{"required":["release"]},{"required":["entity"]},{"required":["plugin"]},{"required":["type"]}]}}
  ]
}`,
	invoke: validateInvoke,
}

// validateInvoke selects the scope from the mutually-exclusive selectors and
// dispatches to that scope's core: type needs plugin, plugin excludes release
// and catalog, release excludes catalog, catalog excludes a single-entity
// target, and exactly one scope must be chosen. When plugin is set, entity is
// the cross-check Plugin entity rather than a single-entity target; when release
// is set beside entity, the entity is the target and release the Release whose
// terms it is checked in. The input schema declares these combinations too, so a
// caller reading it structurally does not have to make the call to learn them.
//
// Every failure here is one the caller corrects by changing the arguments, so
// each returns a tool result carrying the corrective text — a refused
// combination exactly as much as a scope operation that ran and failed.
func validateInvoke(s *server, ctx context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
	var a validateArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return invalidToolArguments("validate", err)
		}
	}
	// Read ahead of the scope, and for every scope alike. The declared set this
	// argument carries is the tool's rather than one scope's — the schema
	// qualifies which scope the value CHANGES anything on, never which scope it
	// may be sent to — so a value outside that set is one this tool does not
	// accept whichever scope the call went on to select.
	format, refusal := parseResponseFormat("validate", a.ResponseFormat, deliveryResponseFormatDefault)
	if refusal != "" {
		return errorResult(refusal)
	}
	release := strings.TrimSpace(a.Release)
	entity := strings.TrimSpace(a.Entity)
	plugin := strings.TrimSpace(a.Plugin)
	pluginType := strings.TrimSpace(a.Type)

	// type declares the plugin type for a binary-only .wasm check and is
	// meaningful only alongside plugin; reject it on its own so it never silently
	// no-ops on another scope.
	if pluginType != "" && plugin == "" {
		return errorResult("validate 'type' applies only with 'plugin'")
	}

	// Plugin scope. It selects neither the catalog nor a release closure, and
	// entity (when present) is the cross-check Plugin entity.
	if plugin != "" {
		if release != "" {
			return errorResult("validate takes exactly one scope: 'plugin' and 'release' are mutually exclusive")
		}
		if a.Catalog {
			return errorResult("validate takes exactly one scope: 'plugin' and 'catalog' are mutually exclusive")
		}
		return validatePluginScope(ctx, s, a, plugin, pluginType, entity)
	}

	// Release scope, or one entity in a Release's terms. Mutually exclusive with
	// whole-catalog validation either way.
	if release != "" {
		if a.Catalog {
			return errorResult("validate takes exactly one scope: 'release' and 'catalog' are mutually exclusive")
		}
		if entity != "" {
			return validateEntityScope(ctx, s, a, entity, release)
		}
		return validateReleaseScope(s, ctx, a, release, format)
	}

	// Whole-catalog scope.
	if a.Catalog {
		if entity != "" {
			return errorResult("validate takes exactly one scope: 'catalog' and 'entity' are mutually exclusive")
		}
		return validateCatalogScope(ctx, s, a)
	}

	// Single-entity scope, in the terms of no one Release.
	if entity != "" {
		return validateEntityScope(ctx, s, a, entity, "")
	}

	return errorResult("validate requires exactly one of 'release', 'entity', 'plugin', or 'catalog'")
}

// validateReleaseScope runs the release-scoped semantic-validation pipeline
// through the shared compiler-operations layer — the same core the CLI validates
// a Release with — and reports each diagnostic in gate order. A lockfile beside the
// Release that no longer matches what it resolves to refuses the call with the
// drift halt a build raises, before any check runs, as the command refuses it.
//
// It takes the resolved response format rather than reading the argument again:
// the value was read once, where a value outside the declared set is refused, and
// a second read here would be a second reading of one argument.
func validateReleaseScope(
	s *server, ctx context.Context, a validateArgs, release, format string,
) (*callToolResult, *rpcError) {
	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, a.CatalogDirs)
	if err != nil {
		return errorResult(explainToolError("Could not resolve the workspace for validation.", err, "release"))
	}
	releasePath, refusal := tw.workspacePath("validate", "release", release)
	if refusal != "" {
		return errorResult(refusal)
	}
	if tw, err = s.subjectWorkspace(tw, release); err != nil {
		return errorResult(explainToolError("Could not resolve the workspace for validation.", err, "release"))
	}
	lf, lockPins, refusal := tw.loadReleaseLockfile(releasePath)
	if refusal != "" {
		return errorResult(refusal)
	}
	// A directory the workspace configuration lists for this Release that is not a
	// directory is refused, as `aggrega validate --release` refuses it, rather than
	// validated past. It is asked ahead of the withdrawal report, so the refusal is
	// answered before anything the answer would report has been read.
	if _, err := tw.catalogDirsFor(releasePath); err != nil {
		return tw.answer(errorResult(catalogDirsGuidance(
			"Validation did not run: the workspace configuration lists a catalog directory this Release reads that "+
				"is not a directory, and `aggrega validate` refuses the same directory.",
			err)))
	}
	deprecations, refusal := tw.releaseDeprecations(lf, releasePath)
	if refusal != "" {
		return errorResult(refusal)
	}
	view := tw.newView(releasePath, lockPins)

	resolved, cerr := compilerops.ResolveValidationClosure(ctx, compilerops.ValidationClosureInputs{
		View:            view,
		ReleasePath:     releasePath,
		WorkspaceRoot:   tw.root,
		WorkspaceConfig: tw.cfg,
		Lockfile:        lf,
	})
	if cerr != nil {
		// The failure here is a closure resolution by construction, so the
		// refusals the view recorded are replayed with no guard: where one of them
		// declares the identity the failing reference names, it leads the answer
		// and the resolution failure moves beneath it. Without this the answer
		// names a reference in a document that is correct, and points the caller
		// at that document's authoring contract.
		annotated := annotateWithRefusals(cerr, view, tw, releasePath)
		return tw.answer(errorResult(explainToolError(
			"Validation could not resolve the Release closure; fix the resolution failure before semantic validation runs.",
			annotated, refusalKindOr(annotated, closureStageKind(cerr.Stage)))))
	}
	// The record loaded at the front door is held to what the Release resolves to,
	// by the build's drift comparison, as `aggrega validate --release` holds it: a
	// record that no longer matches refuses the call with the build's drift halt
	// rather than answering valid over a lockfile the build's drift gate refuses.
	if err := compilerops.HoldReleaseToLockfile(ctx, view, releasePath, tw.root, tw.cfg, lf); err != nil {
		return tw.answer(errorResult(explainToolError(lockfileDriftRefusalSummary("Validation did not run"), err, "release")))
	}

	// The record loaded at this tool's front door is threaded into the validation
	// as well as into the closure resolution, so the guests this pipeline dispatches
	// are held to the identities it records and the two that change the graph
	// receive the inputs it pins for them. Both come of the same omission; passing
	// it repairs both. A workspace with no record loads none and validates as
	// before.
	vr := compilerops.Validate(ctx, compilerops.ValidateInputs{
		View:            view,
		Closure:         resolved.Closure,
		Release:         resolved.Release,
		ReleasePath:     releasePath,
		Manifest:        resolved.Manifest,
		AggBytes:        resolved.AggBytes,
		Lockfile:        lf,
		AggregatePath:   resolved.AggregatePath,
		WorkspaceRoot:   tw.root,
		WorkspaceConfig: tw.cfg,
		CompilerVersion: s.serverVersion,
		Display:         s.runDisplay(),
	})

	diags := validateDiagnostics(vr, resolved.ManifestConsistencyFindings)
	payload := validateResultPayload{
		Scope:       validateScopeRelease,
		Valid:       len(diags) == 0,
		Diagnostics: tw.reportedTexts(diags),
		Notices:     tw.reportedTexts(releaseAdvisories(vr, deprecations)),
	}
	payload.Target, payload.TargetWithheld = tw.reportedLocationField(releasePath)
	payload.Release, payload.ReleaseWithheld = tw.reportedLocationField(releasePath)
	text := renderValidateText(payload, format)
	return tw.answer(textResult(text, payload))
}

// validateEntityScope runs the single-entity scope core over one catalog entity
// file, resolving the entity path against the workspace root as the release path
// resolves. Diagnostics is problems-only: the single blocking error when the
// entity does not validate (the core halts at the first), empty when it does.
// The render-artifact keys are summarized in the payload; the bytes are not
// carried here — those belong to the build / describe / inspect tools.
//
// release is the call's 'release' argument, or empty. Naming one checks the
// entity in that Release's terms: the core is handed the stack the workspace
// configuration gives that Release instead of the workspace's own, and changes
// nothing else.
//
// A directory the workspace configuration lists for that stack that is not a
// directory — nothing exists there, say — is refused before the check reads
// anything, as `aggrega validate <entity>` refuses it, with the resolver's
// refusal naming the entry and the file and line listing it.
//
// What the check reports as a warning — a failed assertion, which the core
// decides is not its verdict — follows the advisory lines in Notices, in the
// core's words, and moves Valid no more than they do. The text half carries it
// too, as the text carries a blocking error, because it is the whole of what the
// caller has to act on. Nothing here decides which findings are warnings.
func validateEntityScope(ctx context.Context, s *server, a validateArgs, entity, release string) (*callToolResult, *rpcError) {
	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, a.CatalogDirs)
	if err != nil {
		return errorResult(explainToolError("Could not resolve the workspace for validation.", err, "component"))
	}
	entityPath, refusal := tw.workspacePath("validate", "entity", entity)
	if refusal != "" {
		return errorResult(refusal)
	}
	releasePath := ""
	if release != "" {
		if releasePath, refusal = tw.workspacePath("validate", "release", release); refusal != "" {
			return errorResult(refusal)
		}
	}
	// The call is about the Release where it names one, and about the entity
	// otherwise: the Release's terms are what the entity is checked in.
	subject := release
	if subject == "" {
		subject = entity
	}
	if tw, err = s.subjectWorkspace(tw, subject); err != nil {
		return errorResult(explainToolError("Could not resolve the workspace for validation.", err, "component"))
	}
	if release != "" {
		// Asking for the Release's reach is what refuses a value naming no
		// Release file; the reach itself is set aside, since the check reads no
		// workspace document beyond the entity and the stack it is handed.
		if _, refusal = tw.releaseReach("validate", releasePath); refusal != "" {
			return errorResult(refusal)
		}
	}
	dirs, err := tw.catalogDirsFor(releasePath)
	if err != nil {
		return tw.answer(errorResult(catalogDirsGuidance(
			"Validation did not run: a catalog directory the entity is checked against is listed in the workspace "+
				"configuration and is not a directory, and `aggrega validate` refuses the same directory.",
			err)))
	}
	// The named part is refused exactly when the stack is, with the same refusal.
	named, _ := tw.namedCatalogDirsFor(releasePath)

	res, verr := compilerops.ValidateSingleEntity(ctx, tw.root, tw.cacheDir, dirs, named, entityPath, false,
		validateops.WithDisplay(s.runDisplay()))

	var diags []string
	if verr != nil {
		diags = []string{verr.Error()}
	}
	warnings := make([]string, 0, len(res.Warnings))
	for _, w := range res.Warnings {
		warnings = append(warnings, w.Error())
	}
	artifactKeys := make([]string, 0, len(res.Artifacts))
	for _, art := range res.Artifacts {
		artifactKeys = append(artifactKeys, art.Key)
	}
	payload := validateResultPayload{
		Scope:       validateScopeEntity,
		Valid:       len(diags) == 0,
		Diagnostics: tw.reportedTexts(diags),
		Artifacts:   artifactKeys,
		Notices:     tw.reportedTexts(slices.Concat(res.Notes, warnings)),
	}
	payload.Target, payload.TargetWithheld = tw.reportedLocationField(entityPath)
	if releasePath != "" {
		payload.Release, payload.ReleaseWithheld = tw.reportedLocationField(releasePath)
	}
	return tw.answer(textResult(renderEntityText(res, verr, artifactKeys), payload))
}

// validatePluginScope runs the plugin-binary scope core: a raw .wasm checked
// against its own describe() self-report, and (when a Plugin entity path is
// supplied) cross-checked against the entity's declared surface. The .wasm path
// and the cross-check entity path resolve against the workspace root, so a client
// that does not know the server's working directory can pass workspace-relative
// paths as the other scopes do. Diagnostics is problems-only: the single blocking
// error when the binary does not validate, empty when it does.
func validatePluginScope(ctx context.Context, s *server, a validateArgs, plugin, pluginType, entity string) (*callToolResult, *rpcError) {
	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, a.CatalogDirs)
	if err != nil {
		return errorResult(explainToolError("Could not resolve the workspace for validation.", err, "plugin"))
	}
	pluginPath, refusal := tw.workspacePath("validate", "plugin", plugin)
	if refusal != "" {
		return errorResult(refusal)
	}
	entityPath := entity
	if entity != "" {
		entityPath, refusal = tw.workspacePath("validate", "entity", entity)
		if refusal != "" {
			return errorResult(refusal)
		}
	}

	res, verr := validateops.ValidatePluginBinary(ctx, pluginPath, pluginType, entityPath, pluginValidateMaxMemoryPages)

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
		Valid:       len(diags) == 0,
		Diagnostics: tw.reportedTexts(diags),
		Notices:     tw.reportedTexts(res.Notices),
	}
	payload.Target, payload.TargetWithheld = tw.reportedLocationField(target)
	return tw.answer(textResult(renderPluginText(res, verr), payload))
}

// validateCatalogScope runs the whole-catalog correctness pass core over each
// pass the one decision of which passes a whole-catalog check runs returns — the
// same passes `aggrega validate` runs with no argument. Diagnostics is
// problems-only and carries the FULL ordered category detail — one entry per
// extends error, the OCI-location collision, and one entry per input-kind
// capability error — never a collapsed count. Exactly one failing category is
// populated by the core for a pass, in its halt-at-first order. Advisory notices
// never fail the verdict and travel in Notices.
//
// A workspace that gives no Release directories of its own has one pass, over
// its own stack, and is answered exactly as a single catalog is. A workspace that
// does is checked once for each Release, and the answer carries each pass beside
// the verdict over all of them; see answerCatalogPasses.
//
// ctx is the call's, carried into every pass, so a client that withdraws ends the
// reads the passes make.
func validateCatalogScope(ctx context.Context, s *server, a validateArgs) (*callToolResult, *rpcError) {
	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, a.CatalogDirs)
	if err != nil {
		return errorResult(explainToolError("Could not resolve the workspace for catalog validation.", err, "component"))
	}
	passes, err := serve.WholeCatalogPasses(tw.rw, tw.flagDirs)
	if err != nil {
		return tw.answer(errorResult(catalogDirsGuidance(
			"Catalog validation did not run: the workspace configuration lists a catalog directory that is not a "+
				"directory, and `aggrega validate` refuses the same directory.",
			err)))
	}
	if len(passes) > 0 && passes[0].NamesReleases() {
		return answerCatalogPasses(ctx, tw, passes)
	}
	// The single workspace pass is validated through the same walk, over the same
	// pass value `aggrega validate` hands it.
	var pass workspaceconfig.CatalogPass
	if len(passes) > 0 {
		pass = passes[0]
	}
	outcome := validateops.ValidateCatalogPasses(ctx, tw.root, tw.cacheDir, []workspaceconfig.CatalogPass{pass}, tw.catalogPassPins, nil)[0]
	if outcome.PinsErr != nil {
		// Answered as a pass of a workspace that names its Releases answers the same
		// refusal: the pass's own diagnostic in the structured answer, with nothing
		// validated beside it.
		payload := validateResultPayload{
			Scope:       validateScopeCatalog,
			Diagnostics: tw.reportedTexts([]string{passLockfileRefusal(outcome.PinsErr)}),
		}
		payload.Target, payload.TargetWithheld = tw.reportedLocationField(tw.root)
		return tw.answer(textResult(renderCatalogText(payload), payload))
	}
	if outcome.LoadErr != nil {
		return tw.answer(errorResult(explainToolError("Catalog validation could not load the catalog.", outcome.LoadErr, "component")))
	}

	res := outcome.Result
	diags := catalogDiagnostics(res)
	payload := validateResultPayload{
		Scope:       validateScopeCatalog,
		Valid:       len(diags) == 0,
		Diagnostics: tw.reportedTexts(diags),
		Notices:     tw.reportedTexts(res.Notices),
	}
	payload.Target, payload.TargetWithheld = tw.reportedLocationField(tw.root)
	return tw.answer(textResult(renderCatalogText(payload), payload))
}

// answerCatalogPasses answers the catalog scope in a workspace that gives its
// Releases catalog directories of their own: one pass for each Release over the
// directories that Release reads, validated through the one walk of the passes
// (validateops.ValidateCatalogPasses) and every pass answered whatever an earlier
// one found, since a broken entity in one Release's catalog variant says nothing
// about another's. A pass whose catalog cannot be loaded is answered as that
// pass's diagnostic rather than as the whole call's failure, for the same reason.
// A pass whose Releases read no directory checks nothing and is valid.
//
// The answer's own Valid, Diagnostics and Notices cover every pass together, so
// a caller reading only those keys reads the whole verdict.
func answerCatalogPasses(ctx context.Context, tw *toolWorkspace, passes []workspaceconfig.CatalogPass) (*callToolResult, *rpcError) {
	payload := validateResultPayload{Scope: validateScopeCatalog}
	outcomes := validateops.ValidateCatalogPasses(ctx, tw.root, tw.cacheDir, passes, tw.catalogPassPins, nil)
	for _, outcome := range outcomes {
		pass := outcome.Pass
		answered := validateCatalogPass{
			Releases:    append([]string{}, pass.Releases...),
			CatalogDirs: make([]*string, 0, len(pass.Dirs)),
		}
		for _, dir := range pass.Dirs {
			name, withheld := tw.reportedLocationField(dir)
			answered.CatalogDirs = append(answered.CatalogDirs, name)
			answered.CatalogDirsWithheld = answered.CatalogDirsWithheld || withheld
		}
		var diags, notices []string
		switch {
		case !outcome.Checked:
			// Nothing was read, so there is nothing to report and the pass is valid.
		case outcome.PinsErr != nil:
			diags = []string{passLockfileRefusal(outcome.PinsErr)}
		case outcome.LoadErr != nil:
			diags = []string{"Catalog validation could not load the catalog: " + outcome.LoadErr.Error()}
		default:
			diags, notices = catalogDiagnostics(outcome.Result), outcome.Result.Notices
		}
		answered.Valid = len(diags) == 0
		answered.Diagnostics = tw.reportedTexts(diags)
		answered.Notices = tw.reportedTexts(notices)
		payload.Diagnostics = append(payload.Diagnostics, answered.Diagnostics...)
		payload.Notices = append(payload.Notices, answered.Notices...)
		payload.Passes = append(payload.Passes, answered)
	}
	payload.Valid = len(payload.Diagnostics) == 0
	payload.Target, payload.TargetWithheld = tw.reportedLocationField(tw.root)
	return tw.answer(textResult(renderCatalogPassesText(payload), payload))
}

// catalogPassPins is the pins question the walk of the catalog passes asks of a
// pass, answered over the call's workspace by serve.CatalogPassPins.
func (tw *toolWorkspace) catalogPassPins(pass workspaceconfig.CatalogPass) (catalog.Pins, error) {
	return serve.CatalogPassPins(tw.rw, pass)
}

// passLockfileRefusal is the diagnostic a whole-catalog pass answers with when the
// lockfiles of its Releases could not be read, so nothing in it was validated. It
// is the one wording of that refusal, read by the single workspace pass and by
// each pass of a workspace that names its Releases.
func passLockfileRefusal(err error) string {
	return "Catalog validation could not read the lockfiles of this pass's Releases: " + err.Error()
}

// catalogDiagnostics flattens a whole-catalog result into its ordered diagnostic
// lines, each carrying the category it failed in.
//
// A refused selector is rendered with its condition's code at the head, as the
// command line renders it, so the code a reader resolves is the same on both.
func catalogDiagnostics(res validateops.CatalogResult) []string {
	var diags []string
	for _, e := range res.ExtendsErrs {
		diags = append(diags, "extends: "+e.Error())
	}
	for _, e := range res.SelectorErrs {
		diags = append(diags, "selector: "+cerrors.HeadedCauseChain(e))
	}
	if res.OCICollision != nil {
		diags = append(diags, "oci-location: "+res.OCICollision.Error())
	}
	for _, e := range res.InputKindErrs {
		diags = append(diags, "input-kind: "+e.Error())
	}
	return diags
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
	// The resolver-class findings are taken once into a local, so the phase-error
	// guard below asks its question of the same slice this loop published.
	var perViolation []*resolver.ResolverError
	if vr.Tier4 != nil {
		perViolation = vr.Tier4.ResolveDiagnostics
	}
	for _, ve := range perViolation {
		out = append(out, ve.Error())
	}
	if vr.Tier4 != nil && vr.Tier4.Analysis != nil {
		for _, ae := range vr.Tier4.Analysis.Errors {
			out = append(out, ae.Error())
		}
	}
	// A non-resolver pre-analyzer phase error carries no per-violation structure;
	// surface it only when the resolver did not already publish it finding by
	// finding, so a resolver failure is not double-reported. Cancellation is not a
	// diagnostic.
	//
	// Whether it was already published is asked of the run error itself and never of
	// whether the slice above was empty. That slice also carries the findings a rule
	// raises for an emitting run alone, which reach a surface like this one on a run
	// whose failure is elsewhere and on a run that did not fail at all — so reading
	// its length would take such a finding as evidence about the phase error and drop
	// the only line describing it.
	//
	// It is rendered as one line headed by its condition and carrying every cause
	// beneath it (cerrors.HeadedCauseChain), not by its own Error, which is a single
	// frame: a failing assertion's frame names only the stage, while the assertion,
	// the instance and the author's own sentence sit in the frames beneath it, and
	// the head is what explain-error reads the condition from.
	if vr.RunErr != nil && !isCancellation(vr.RunErr) &&
		!runErrPublishedPerViolation(vr.RunErr, perViolation) {
		out = append(out, cerrors.HeadedCauseChain(vr.RunErr))
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
// the facts were decided — the deprecation report over both stores' recorded
// markers, then the schema-pillar overlap advisories, then the narrowing-on-extend
// advisories. These never change the verdict; they travel in the payload's Notices
// field for callers and the detailed render form.
//
// The deprecation report leads because its facts predate every gate in vr: the
// markers were recorded at the pull that wrote the lockfile and the plugin trust
// record beside it. It is the same shared report the build reads, so this tool
// names the same references and plugins rather than a subset — and it is advisory
// here for the reason the build's own default records: a marker is registry state
// applied after the version was pinned, and the dial that turns it into a halt
// governs the build alone.
//
// deprecations is a parameter rather than something read from vr because the
// shared validation core produces no withdrawal report; the caller reads it at the
// tool's front door, where an unreadable record is refused.
func releaseAdvisories(vr *compilerops.ValidateResult, deprecations []compilerops.DeprecationFinding) []string {
	if len(deprecations) == 0 && len(vr.LintWarnings) == 0 && len(vr.NarrowingWarnings) == 0 {
		return nil
	}
	out := make([]string, 0, len(deprecations)+len(vr.LintWarnings)+len(vr.NarrowingWarnings))
	for _, f := range deprecations {
		out = append(out, "deprecation: "+f.Notice)
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

// runErrPublishedPerViolation reports whether the pre-render run error has
// already been published finding by finding among the resolver-class
// diagnostics, which is what decides whether the closure-level fall-back has
// anything left to say about it.
//
// It reads the ERROR's own shape rather than the published slice's length,
// because the two stopped answering the same question once a rule could raise a
// finding that refuses only a run producing something to apply: such a finding
// travels out of the pipeline on the same slice, on a run whose failure is
// somewhere else entirely and on a run that did not fail at all. The published
// slice still bounds the answer — an error whose per-violation extraction
// yielded nothing was not published however it is shaped — so it is a parameter
// rather than something re-derived here.
//
// The two shapes are the two the pipeline extracts into that slice: a group,
// which carries its violations, and a single finding, which is the one-element
// case.
//
// Every front end reading a validation result asks this same question of it, and
// the reading is written out per front end deliberately rather than shared. Its
// one structural home is the entry that fills the slice, which would mark each
// entry with whether the run error produced it and leave a consumer no reading to
// reproduce at all; short of that, a shared consumer-side copy would single-source
// the reproduction rather than the fact and hide that it is one. Each copy reads
// the same two error shapes that entry extracts, so the copies are comparable by
// inspection.
func runErrPublishedPerViolation(runErr error, published []*resolver.ResolverError) bool {
	if len(published) == 0 {
		return false
	}
	var multi *resolver.MultiResolverError
	var single *resolver.ResolverError
	return errors.As(runErr, &multi) || errors.As(runErr, &single)
}

// renderValidateText renders the human-readable release-scope validate result.
// The concise form reports the count and the first diagnostic and emits no
// advisory lines; the detailed form lists every diagnostic and then the advisory
// notices. Verbosity governs output volume only — the payload's verdict (Valid)
// is identical across both forms.
func renderValidateText(p validateResultPayload, format string) string {
	release := reportedLocationWords(p.Release)
	if p.Valid {
		clean := fmt.Sprintf("Release %s validated clean: no diagnostics.", release)
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
			release, len(p.Diagnostics), p.Diagnostics[0])
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Release %s has %d validation diagnostic(s):\n", release, len(p.Diagnostics))
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
// block when the isolated render did not complete), then each finding the core
// reported as a warning, then the blocking error when the entity did not
// validate, then a render-artifact key summary when the render emitted artifacts.
// Artifact bytes are never printed here — the summary names the keys only.
//
// The blocking error is written for the same reason the plugin scope writes its
// own: it is the whole of what the caller has to act on. Left out, the text half
// reports only which file was read and what kind it is while the structured half
// carries the condition, its short code and the constraint — one capability
// answering at two fidelities depending on which half the caller reads, and the
// half a person reads is the one that said nothing. A warning is written for the
// same reason, opened by "warning:" as the command line opens it.
func renderEntityText(res validateops.EntityResult, verr error, artifactKeys []string) string {
	var b strings.Builder
	for _, line := range res.Diagnostics {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	for _, w := range res.Warnings {
		fmt.Fprintf(&b, "warning: %s\n", w)
	}
	if verr != nil {
		b.WriteString(verr.Error())
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
		return fmt.Sprintf("%s validated clean: no diagnostics.", catalogSubject(p.Target))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s has %d validation diagnostic(s):\n", catalogSubject(p.Target), len(p.Diagnostics))
	for i, d := range p.Diagnostics {
		fmt.Fprintf(&b, "  %d. %s\n", i+1, d)
	}
	return strings.TrimRight(b.String(), "\n")
}

// renderCatalogPassesText renders the human-readable whole-catalog result of a
// workspace checked once for each Release: each pass under the line naming its
// Releases and the directories it read, a directory kept back from the caller
// reading as the kept-back phrase, then that pass's verdict and its full ordered
// diagnostic lines.
func renderCatalogPassesText(p validateResultPayload) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s is checked once for each Release, over the catalog directories that Release reads.\n",
		catalogSubject(p.Target))
	for _, pass := range p.Passes {
		dirs := make([]string, 0, len(pass.CatalogDirs))
		for _, dir := range pass.CatalogDirs {
			dirs = append(dirs, reportedLocationWords(dir))
		}
		fmt.Fprintf(&b, "\n%s\n", workspaceconfig.CatalogPassHeading(pass.Releases, dirs))
		switch {
		case len(pass.CatalogDirs) == 0:
		case pass.Valid:
			b.WriteString("  validated clean: no diagnostics.\n")
		default:
			fmt.Fprintf(&b, "  %d validation diagnostic(s):\n", len(pass.Diagnostics))
			for i, d := range pass.Diagnostics {
				fmt.Fprintf(&b, "  %d. %s\n", i+1, d)
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// catalogSubject names the catalog a whole-catalog result is about. Its target is
// the workspace root, and where the caller is not told where that sits the
// catalog is named as the served workspace's rather than by a location.
func catalogSubject(target *string) string {
	if target == nil {
		return "The catalog of " + servedWorkspaceWords
	}
	return "Catalog " + *target
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

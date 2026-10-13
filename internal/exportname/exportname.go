// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package exportname

// The provider plugin type's exports: render_node, which every provider guest
// declares, and the exports the host calls only when the guest's describe()
// declares them.
const (
	RenderNode           = "render_node"
	ValidateNodeInput    = "validate_node_input"
	AssembleBoundary     = "assemble_boundary"
	Carve                = "carve"
	Reassemble           = "reassemble"
	NameBound            = "name_bound"
	RenderedName         = "rendered_name"
	BoundaryEnvOverrides = "boundary_env_overrides"
	LinkPublish          = "link_publish"
	LinkResolve          = "link_resolve"
)

// The linter plugin type's default rule-evaluation export, called when a linter
// attachment names no entry function of its own.
const LintRules = "lint_rules"

// The pipeline-step plugin type's required export.
const Step = "step"

// The governance plugin type's exports, one default per attachment role: the
// export a role calls when its attachment names no entry function.
const (
	TransformAnnotations = "transform_annotations"
	Mutate               = "mutate"
	VerdictIR            = "verdict_ir"
	VerdictRender        = "verdict_render"
)

// The cli plugin type's required export, called with the invocation an operator
// runs at the command line.
const CLIRun = "cli_run"

// The parser plugin type's required export.
const Parse = "parse"

// The importer plugin type's required export.
const Import = "import"

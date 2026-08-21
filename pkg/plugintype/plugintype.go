// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package plugintype

// Type is the canonical string identifying a Wasm plugin type. It is the value
// a plugin declares as its plugin_type and the key the registry is indexed by.
type Type string

const (
	// Provider renders IR nodes into provider-native artifacts.
	Provider Type = "provider"
	// Linter inspects authored content and raises diagnostics.
	Linter Type = "linter"
	// PipelineStep transforms the IR within a feature recipe's pipeline.
	PipelineStep Type = "pipeline-step"
	// Governance serves the build-pipeline governance roles: it transforms
	// topology-graph node labels at a transform role, or renders an accept/reject
	// verdict over the IR graph or the rendered artifacts at a verdict role. The
	// role a plugin is attached at — not its type — fixes what it may emit.
	Governance Type = "governance"
	// CLI contributes a runtime aggrega subcommand invoked by an operator at
	// the command line rather than by the build pipeline.
	CLI Type = "cli"
	// Parser turns a foreign source text into a canonical structured tree so a
	// consumer can read the tree without carrying its own language parser.
	Parser Type = "parser"
	// Importer turns a foreign infrastructure or agent-definition source into
	// draft Aggrega authoring files, advertising the formats it handles as
	// import:<format> capabilities. It attaches at the import workspace socket
	// and declares an import export the host dispatches to.
	Importer Type = "importer"
)

// Tier classifies a plugin type by how it participates in a build's
// reproducibility contract. It is a pure function of the type — a legible,
// single-sourced classification, never a per-plugin flag.
type Tier int

const (
	// TierMutating: the type's plugin shapes the rendered output bytes, so its
	// plugin is pinned in the lockfile and drift-gated.
	TierMutating Tier = iota
	// TierVerdict: the type's plugin renders an accept/reject verdict without
	// shaping output bytes, so its plugin is cached for a hermetic build but
	// not pinned and not drift-gated.
	TierVerdict
	// TierOrthogonal: the type's plugin acts on authoring input and never
	// touches the rendered output, so it is neither cached in the build closure
	// nor pinned.
	TierOrthogonal
)

// Role is the pipeline attachment role a governance plugin is bound to. The role
// — not the plugin's declared type — fixes what a governance plugin may emit and
// the tier it participates in. It is the single home of the governance-role
// vocabulary: the config surface, the dispatch runners, and the wire schema all
// read these values.
type Role string

const (
	// RoleAnnotationTransform injects annotations onto resolved components after
	// resolve and before annotation expansion: the plugin returns a list of
	// annotation injections the host applies to existing component coordinates,
	// so a bound injected annotation expands into a Feature attachment. It is a
	// transform role and shapes output bytes.
	RoleAnnotationTransform Role = "annotation-transform"
	// RoleLabelTransform auto-tags topology-graph node labels at topology
	// construction: the
	// plugin returns JSON Patch documents the host applies to a labels-only
	// projection of the graph. It is a transform role and shapes output bytes.
	RoleLabelTransform Role = "label-transform"
	// RoleIRVerdict renders an accept/reject verdict over the IR graph at static
	// analysis;
	// a rejected rule halts the build. It is a verdict role and shapes no output.
	RoleIRVerdict Role = "ir-verdict"
	// RoleRenderVerdict renders an accept/reject verdict over the rendered
	// artifacts at rendering; a rejected rule halts the build. It is a verdict role
	// and shapes no output.
	RoleRenderVerdict Role = "render-verdict"
)

// GovernanceRoles returns every governance attachment role in fixed
// author-defined order. It is the single home of the role set, so a consumer
// enumerating valid roles reads them from here rather than re-listing them.
func GovernanceRoles() []Role {
	return []Role{RoleAnnotationTransform, RoleLabelTransform, RoleIRVerdict, RoleRenderVerdict}
}

// GovernanceRoleTier reports the reproducibility tier a governance plugin
// participates in when attached at role, and whether role is a known governance
// role. It is the single home of the role→tier classification: a transform role
// shapes output bytes (TierMutating — pinned, drift-gated); a verdict role shapes
// no output (TierVerdict — cache-not-pin). Because pull and build both read this
// one function, they can never disagree on which roles are verdict roles.
func GovernanceRoleTier(role Role) (Tier, bool) {
	switch role {
	case RoleAnnotationTransform, RoleLabelTransform:
		return TierMutating, true
	case RoleIRVerdict, RoleRenderVerdict:
		return TierVerdict, true
	default:
		return TierMutating, false
	}
}

// GovernanceRoleExport reports the default entry-function export name for a
// governance role, and whether role is a known governance role. It is the single
// home of the role→export rule: the annotation-transform role calls
// transform_annotations; the label-transform role calls mutate; a verdict role
// calls validate.
//
// The name is a default rather than a requirement, and the distinction is
// load-bearing. It applies when a governance attachment leaves its entry
// function unset; an attachment that names one dispatches that function instead.
// At dispatch the host checks one thing about that function: that the guest
// advertised it in describe(). It refuses a call to one the guest did not. But
// nothing relates the dispatched function back to the role, so an attachment may
// point a role at any function its guest declares.
func GovernanceRoleExport(role Role) (string, bool) {
	switch role {
	case RoleAnnotationTransform:
		return "transform_annotations", true
	case RoleLabelTransform:
		return "mutate", true
	case RoleIRVerdict, RoleRenderVerdict:
		return "validate", true
	default:
		return "", false
	}
}

// Mechanism names how a plugin type attaches to the host.
type Mechanism string

const (
	// MechanismWorkspaceSocket: the type attaches at a named workspace-level
	// plugin socket configured in the workspace config.
	MechanismWorkspaceSocket Mechanism = "workspace-socket"
	// MechanismCatalogPipelineStep: the type is a step in a feature recipe's
	// pipeline, declared in catalog content.
	MechanismCatalogPipelineStep Mechanism = "catalog-pipeline-step"
	// MechanismCapabilityAmbient: the type is resolved on demand by the
	// capability it advertises rather than attached at a fixed socket.
	MechanismCapabilityAmbient Mechanism = "capability-ambient"
	// MechanismRuntimeInstalled: the type is installed for the runtime CLI and
	// invoked by an operator at the command line.
	MechanismRuntimeInstalled Mechanism = "runtime-installed"
)

// LoadProfile names the host-function profile a plugin type loads under.
type LoadProfile int

const (
	// LoadProfileBuild loads under the build profile — the shared host-function
	// allowlist the compilation pipeline binds.
	LoadProfileBuild LoadProfile = iota
	// LoadProfileRuntimeTerminal loads under the runtime terminal profile —
	// the CLI allowlist that admits the streaming stdout/stderr emitters.
	LoadProfileRuntimeTerminal
)

// MinExport is a plugin type's minimum-export rule: the export name the host
// requires a plugin of this type to declare, or a name prefix at least one
// export must carry. IsPrefix distinguishes the two: false means Name is an
// exact export name; true means Name is a prefix. A type whose dispatched export
// comes from its attachment rather than from the type itself declares no rule and
// leaves both fields zero.
type MinExport struct {
	// Name is the exact required export name, or the required prefix when
	// IsPrefix is true.
	Name string
	// IsPrefix reports whether Name is a prefix (satisfied by any export whose
	// name begins with it) rather than an exact export name.
	IsPrefix bool
}

// Entry is the full metadata record for one plugin type.
type Entry struct {
	// Type is the canonical plugin-type string.
	Type Type
	// Label is the human-facing noun for the type in a minimum-export rejection
	// message. It matches Type for most types; the feature-recipe-pipeline step
	// reads longer than its terse canonical string.
	Label string
	// MinExport is the type's minimum-export rule. It is the zero value for a
	// type that declares no rule.
	MinExport MinExport
	// Socket is the workspace socket name the type attaches at, or "" when the
	// type is not a workspace-socket type.
	Socket string
	// Tier is the type's reproducibility tier.
	Tier Tier
	// Mechanism is how the type attaches to the host.
	Mechanism Mechanism
	// LoadProfile is the host-function profile the type loads under.
	LoadProfile LoadProfile
}

// entries is the registry, in fixed author-defined order. Enumeration reads a
// copy of this slice so a consumer that iterates it emits deterministic output.
var entries = []Entry{
	{
		Type:        Provider,
		Label:       "provider",
		MinExport:   MinExport{Name: "render_node"},
		Socket:      "provider",
		Tier:        TierMutating,
		Mechanism:   MechanismWorkspaceSocket,
		LoadProfile: LoadProfileBuild,
	},
	{
		Type:        Linter,
		Label:       "linter",
		MinExport:   MinExport{Name: "lint_", IsPrefix: true},
		Socket:      "linter",
		Tier:        TierVerdict,
		Mechanism:   MechanismWorkspaceSocket,
		LoadProfile: LoadProfileBuild,
	},
	{
		Type:        PipelineStep,
		Label:       "feature-recipe-pipeline",
		MinExport:   MinExport{Name: "step"},
		Socket:      "",
		Tier:        TierOrthogonal,
		Mechanism:   MechanismCatalogPipelineStep,
		LoadProfile: LoadProfileBuild,
	},
	{
		Type:  Governance,
		Label: "governance",
		// Governance has no single minimum export: which export a governance
		// attachment dispatches is the attachment's own entry function, defaulting
		// to the role's when it names none (see GovernanceRoleExport). The zero
		// MinExport rule admits any governance guest at load, and the role
		// constrains only that default -- not the export finally dispatched.
		MinExport: MinExport{},
		Socket:    "governance",
		// The registry Tier is the conservative fallback for a governance ref with
		// no resolvable role; the real tier is role-resolved via GovernanceRoleTier
		// (a transform role pins, a verdict role does not). Over-pinning is the safe
		// default — it never breaks the deterministic-output contract.
		Tier:        TierMutating,
		Mechanism:   MechanismWorkspaceSocket,
		LoadProfile: LoadProfileBuild,
	},
	{
		Type:        CLI,
		Label:       "cli",
		MinExport:   MinExport{Name: "cli_run"},
		Socket:      "",
		Tier:        TierOrthogonal,
		Mechanism:   MechanismRuntimeInstalled,
		LoadProfile: LoadProfileRuntimeTerminal,
	},
	{
		Type:        Parser,
		Label:       "parser",
		MinExport:   MinExport{Name: "parse"},
		Socket:      "",
		Tier:        TierOrthogonal,
		Mechanism:   MechanismCapabilityAmbient,
		LoadProfile: LoadProfileBuild,
	},
	{
		Type:        Importer,
		Label:       "importer",
		MinExport:   MinExport{Name: "import"},
		Socket:      "importer",
		Tier:        TierOrthogonal,
		Mechanism:   MechanismWorkspaceSocket,
		LoadProfile: LoadProfileBuild,
	},
}

// Entries returns every registry entry in fixed author-defined order. The
// returned slice is a copy, so a caller may not mutate the registry through it.
func Entries() []Entry {
	out := make([]Entry, len(entries))
	copy(out, entries)
	return out
}

// Lookup returns the registry entry for a plugin type and whether one exists.
func Lookup(t Type) (Entry, bool) {
	for _, e := range entries {
		if e.Type == t {
			return e, true
		}
	}
	return Entry{}, false
}

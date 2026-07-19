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
	// FRPStep transforms the IR within a feature recipe's pipeline.
	FRPStep Type = "frp_step"
	// Mutator auto-tags topology-graph node labels, returning JSON Patch
	// documents the host applies to a labels-only projection of the graph.
	Mutator Type = "mutator"
	// Policy evaluates the full IR graph and returns one pass/fail verdict per
	// rule; a failed rule halts the build.
	Policy Type = "policy"
	// CLI contributes a runtime aggrega subcommand invoked by an operator at
	// the command line rather than by the build pipeline.
	CLI Type = "cli"
	// Parser turns a foreign source text into a canonical structured tree so a
	// consumer can read the tree without carrying its own language parser.
	Parser Type = "parser"
	// Importer names the reserved import socket. It is a workspace socket but
	// not yet a plugin type: it has no minimum export and no dispatcher. The
	// registry carries it so its half-presence — a socket without a plugin
	// type — reads as documented intent rather than drift.
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
// exact export name; true means Name is a prefix. A reserved type has no rule
// and leaves both fields zero.
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
	// reserved type, which has no rule.
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
	// Reserved reports whether the type is reserved — a socket present without a
	// realized plugin type (no minimum export, no dispatcher).
	Reserved bool
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
		Type:        FRPStep,
		Label:       "feature-recipe-pipeline",
		MinExport:   MinExport{Name: "step"},
		Socket:      "",
		Tier:        TierOrthogonal,
		Mechanism:   MechanismCatalogPipelineStep,
		LoadProfile: LoadProfileBuild,
	},
	{
		Type:        Mutator,
		Label:       "mutator",
		MinExport:   MinExport{Name: "mutate"},
		Socket:      "mutator",
		Tier:        TierMutating,
		Mechanism:   MechanismWorkspaceSocket,
		LoadProfile: LoadProfileBuild,
	},
	{
		Type:        Policy,
		Label:       "policy",
		MinExport:   MinExport{Name: "validate"},
		Socket:      "policy",
		Tier:        TierVerdict,
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
		Label:       "",
		MinExport:   MinExport{},
		Socket:      "importer",
		Tier:        TierOrthogonal,
		Mechanism:   MechanismWorkspaceSocket,
		LoadProfile: LoadProfileBuild,
		Reserved:    true,
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

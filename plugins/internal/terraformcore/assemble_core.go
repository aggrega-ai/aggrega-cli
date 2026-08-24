// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package terraformcore

import (
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// RootConfigFileName is the output filename of the root configuration the
// terraform assembler emits at a boundary: one `module` block per component
// rendered there, each pointing at the sub-directory that component's own files
// sit in.
//
// Terraform loads every .tf file in a directory, so the name is free; it follows
// the `aggrega.<what-it-is>.tf` shape [ProvenanceFileName] uses, which marks the
// file as one the compiler writes rather than one an author edits by hand. The one
// emitted name Terraform's own contract fixes — [OverrideFileName], which is
// treated as an override only because of how it is spelled — is spelled the way
// that contract requires and is not a precedent for a name Terraform leaves open.
const RootConfigFileName = "aggrega.modules.tf"

// ModulesDirName is the directory each component's own files sit under, one
// sub-directory per component, beneath the root configuration that declares
// them. It is Terraform's own name for the place a configuration keeps the
// modules it composes: the standard module structure places nested modules in a
// `modules/` sub-directory.
//
// The assembler reports this to the host as the segment to nest per-node output
// under, and composes the same value into each `module` block's source path, so
// the block and the directory it points at cannot drift.
const ModulesDirName = "modules"

// RootComponent is one component contributing to a boundary's root
// configuration: the component's identifier, the deployment wave the component's
// containing boundary declared, and the component's rendered values.
//
// The wave travels per component rather than once for the whole boundary because
// contributors from several boundaries can be composed into one root, each
// carrying its own wave — which is precisely the case in which the ordering has
// anything to express.
//
// The values travel here because the root configuration is the position the
// engine evaluates. A module's own directory is loaded as a child, and a value
// placed beside the module's files reaches it as a datum rather than as an
// expression — so a value written as an interpolation would arrive as its own
// literal text. Carried into the `module` block instead, the same value arrives
// where the engine resolves it.
type RootComponent struct {
	// ID is the component's identifier, which names both its `module` block and
	// the sub-directory that block's source points at.
	ID string
	// SyncWave is the deployment wave the component inherited from its boundary
	// (default 0; negative permitted).
	SyncWave int
	// Values holds the component's rendered values, each becoming one argument
	// of the component's `module` block. The keys arrive in the order the caller
	// fixed and are emitted in that order, so nothing here re-sorts them.
	Values types.OrderedMap[any]
}

// AssembleRoot returns the root configuration for one boundary: a single file
// declaring each contributing component as a `module` block whose source is that
// component's own sub-directory under [ModulesDirName], carrying the ordering the
// components' declared deployment waves express.
//
// The layout is what makes cross-component ordering expressible at all. A
// Terraform dependency graph is per-configuration, so components emitted into
// separate directories are separate graphs with no relationship to each other,
// and no reference or `depends_on` can span them. One root composing each
// component as a child module puts every component in one graph.
//
// Ordering. A component in a later wave declares a `depends_on` naming every
// component of the immediately-preceding populated wave, so the engine completes
// its actions on the earlier wave before it begins the later one;
// [waveDependencies] holds the rule in full, including what the ordering does and
// does not promise. A boundary with fewer than two populated waves declares no
// `depends_on` at all, so a boundary where no wave was declared emits the bytes it
// would emit if the wave expressed no ordering.
//
// Inputs. Each component's values become the arguments of that component's
// `module` block, one argument per value, encoded by [ModuleArgumentExpression].
// A value is therefore an expression the engine evaluates rather than a datum it
// reads, which is what lets a value written as an interpolation — a pointer the
// Recipe supplies and nothing resolves ahead of time — reach a position where the
// engine can resolve it. Within a block the order is `source`, then the arguments
// in the order the values arrive, then `depends_on`: an argument binds an input
// the module declares, while `depends_on` is one of the engine's own meta
// arguments, so the block reads as the module's own inputs followed by the
// ordering imposed on it from outside.
//
// components arrive in the order the host lists the boundary's contributors and
// are emitted in that order, so identical input produces byte-identical output.
// The returned files are in emission order for the same reason — a map keyed by
// path would put the guest's output order at the mercy of Go's map iteration. A
// component whose identifier does not fit Terraform's grammar for a module name
// is mapped into it by [ModuleName]; when two components map onto one name the
// result is ambiguous, so this returns an error naming both rather than emitting a
// configuration Terraform would reject. A value whose key falls outside the
// grammar for an argument name is refused instead of mapped, because an
// argument's name must match the input variable it binds — see
// [ValidModuleArgumentName].
func AssembleRoot(components []RootComponent) ([]rendercore.RenderedFile, error) {
	moduleNames := make([]string, len(components))
	claimedBy := make(map[string]string, len(components))
	for i, c := range components {
		name := ModuleName(c.ID)
		if first, taken := claimedBy[name]; taken {
			return nil, fmt.Errorf("components %q and %q both name the Terraform module %q in this boundary; a module name is unique within a configuration, so rename one of the components", first, c.ID, name)
		}
		claimedBy[name] = c.ID
		moduleNames[i] = name
	}
	// Encode every argument before writing any of them, so a value that cannot
	// be written refuses the whole boundary rather than leaving a half-built
	// configuration behind.
	arguments := make([][]moduleArgument, len(components))
	for i, c := range components {
		args, err := moduleArguments(c)
		if err != nil {
			return nil, err
		}
		arguments[i] = args
	}
	deps := waveDependencies(components, moduleNames)

	var buf strings.Builder
	buf.WriteString("# Root configuration for this boundary. Each component rendered here is one\n")
	buf.WriteString("# module block below, and the directory that block points at holds that\n")
	buf.WriteString("# component's own files. One configuration means one dependency graph, which is\n")
	buf.WriteString("# what lets a reference or a depends_on reach from one component to another.\n")

	for i, c := range components {
		fmt.Fprintf(&buf, "\nmodule %q {\n  source = \"./%s/%s\"\n", moduleNames[i], ModulesDirName, c.ID)
		// The component's own inputs, one argument per value, before any meta
		// argument. Names are padded to a common width, which is how the engine's
		// own formatter lays out a run of single-line arguments, so a reader
		// diffing the output sees only the values that changed. The blank line
		// above keeps the source path out of that run, which is where the
		// formatter puts it too.
		if len(arguments[i]) > 0 {
			buf.WriteString("\n")
			width := 0
			for _, a := range arguments[i] {
				if len(a.name) > width {
					width = len(a.name)
				}
			}
			for _, a := range arguments[i] {
				fmt.Fprintf(&buf, "  %-*s = %s\n", width, a.name, a.expression)
			}
		}
		// An empty deps means the boundary expresses no ordering at all; an empty
		// entry means this component's wave is the lowest populated one.
		if len(deps) > 0 && len(deps[i]) > 0 {
			fmt.Fprintf(&buf, "\n  %s = [", dependsOnMetaArgument)
			for j, name := range deps[i] {
				if j > 0 {
					buf.WriteString(", ")
				}
				fmt.Fprintf(&buf, "module.%s", name)
			}
			buf.WriteString("]\n")
		}
		buf.WriteString("}\n")
	}

	// Format is left unset: the provider's suffix-to-format classification has one
	// home, in the guest, and every emission path routes its files through it.
	return []rendercore.RenderedFile{{
		Path:    RootConfigFileName,
		Content: []byte(buf.String()),
	}}, nil
}

// moduleArgument is one encoded argument of a `module` block: the name it is
// written under and the Terraform expression it carries.
type moduleArgument struct {
	name       string
	expression string
}

// moduleArguments encodes one component's values as the arguments of its
// `module` block, in the order the values arrive.
//
// A key that cannot be written as an argument name is refused, naming the
// component and the key, rather than mapped into the grammar the way a module
// name is: an argument's name must match the input variable it binds, so a
// mapped name would bind a variable the module never declared and the engine
// would reject the configuration for an input that does not exist. Refusing
// here points the author at the value they wrote; mapping would point them at a
// file nobody wrote. [ValidModuleArgumentName] holds the grammar.
func moduleArguments(c RootComponent) ([]moduleArgument, error) {
	if c.Values.Len() == 0 {
		return nil, nil
	}
	out := make([]moduleArgument, 0, c.Values.Len())
	for k, v := range c.Values.All() {
		if !ValidModuleArgumentName(k) {
			return nil, fmt.Errorf("component %q supplies a value named %q, which cannot be written as a Terraform module input: an input name begins with a letter or an underscore and may then carry letters, digits, underscores and dashes. An argument's name is the name of the input variable it binds, so rename the value in the Recipe that produces it", c.ID, k)
		}
		expr, err := ModuleArgumentExpression(v)
		if err != nil {
			return nil, fmt.Errorf("component %q value %q cannot be encoded as a Terraform expression: %w", c.ID, k, err)
		}
		out = append(out, moduleArgument{name: k, expression: expr})
	}
	return out, nil
}

// ModuleName maps a component identifier onto Terraform's grammar for a module
// name: it must begin with a letter or an underscore and may then carry letters,
// digits, underscores and dashes. A component identifier is a dot-joined path of
// names, each of which may hold letters, digits, underscores and dashes — so the
// two things that can fall outside the grammar are the dot that joins a nested
// component's path and a leading digit or dash.
//
// Each byte outside the grammar becomes an underscore, and a result that does not
// begin with a letter or an underscore gains one. The mapping is therefore not
// injective — a component named `a.b` and one named `a_b` arrive at the same name
// — which is why [AssembleRoot] refuses a boundary whose components collide
// rather than emitting a configuration with two modules of one name.
func ModuleName(componentID string) string {
	out := make([]byte, 0, len(componentID)+1)
	for i := 0; i < len(componentID); i++ {
		if c := componentID[i]; nameByte(c) {
			out = append(out, c)
		} else {
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "_"
	}
	if !nameLeadByte(out[0]) {
		out = append([]byte{'_'}, out...)
	}
	return string(out)
}

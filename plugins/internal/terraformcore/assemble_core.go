// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package terraformcore

import (
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
)

// RootConfigFileName is the output filename of the root configuration the
// terraform assembler emits at a boundary: one `module` block per component
// rendered there, each pointing at the sub-directory that component's own files
// sit in.
//
// Terraform loads every .tf file in a directory, so the name is free; it follows
// the `aggrega.<what-it-is>.tf` shape [ProvenanceFileName] uses, which marks the
// file as one the compiler writes rather than one an author edits by hand. The
// two names Terraform's own contracts fix — the auto-loaded tfvars overlay and
// the override — are spelled the way those contracts require and are not a
// precedent for a name Terraform leaves open.
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
// configuration: the component's identifier, and the deployment wave the
// component's containing boundary declared.
//
// The wave travels per component rather than once for the whole boundary because
// contributors from several boundaries can be composed into one root, each
// carrying its own wave — which is precisely the case in which the ordering has
// anything to express.
type RootComponent struct {
	// ID is the component's identifier, which names both its `module` block and
	// the sub-directory that block's source points at.
	ID string
	// SyncWave is the deployment wave the component inherited from its boundary
	// (default 0; negative permitted).
	SyncWave int
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
// components arrive in the order the host lists the boundary's contributors and
// are emitted in that order, so identical input produces byte-identical output.
// The returned files are in emission order for the same reason — a map keyed by
// path would put the guest's output order at the mercy of Go's map iteration. A
// component whose identifier does not fit Terraform's grammar for a module name
// is mapped into it by [ModuleName]; when two components map onto one name the
// result is ambiguous, so this returns an error naming both rather than emitting a
// configuration Terraform would reject.
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
	deps := waveDependencies(components, moduleNames)

	var buf strings.Builder
	buf.WriteString("# Root configuration for this boundary. Each component rendered here is one\n")
	buf.WriteString("# module block below, and the directory that block points at holds that\n")
	buf.WriteString("# component's own files. One configuration means one dependency graph, which is\n")
	buf.WriteString("# what lets a reference or a depends_on reach from one component to another.\n")

	for i, c := range components {
		fmt.Fprintf(&buf, "\nmodule %q {\n  source = \"./%s/%s\"\n", moduleNames[i], ModulesDirName, c.ID)
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
		c := componentID[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == '-':
			out = append(out, c)
		default:
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "_"
	}
	if c := out[0]; !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') && c != '_' {
		out = append([]byte{'_'}, out...)
	}
	return string(out)
}

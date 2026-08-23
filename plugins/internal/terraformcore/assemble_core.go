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

// AssembleRoot returns the root configuration for one boundary: a single file
// declaring each contributing component as a `module` block whose source is that
// component's own sub-directory under [ModulesDirName].
//
// The layout is what makes cross-component ordering expressible at all. A
// Terraform dependency graph is per-configuration, so components emitted into
// separate directories are separate graphs with no relationship to each other,
// and no reference or `depends_on` can span them. One root composing each
// component as a child module puts every component in one graph.
//
// componentIDs arrive in the order the host lists the boundary's contributors
// and are emitted in that order, so identical input produces byte-identical
// output. The returned files are in emission order for the same reason — a map
// keyed by path would put the guest's output order at the mercy of Go's map
// iteration. A component whose identifier does not fit Terraform's grammar for a
// module name is mapped into it by [ModuleName]; when two components map onto one
// name the result is ambiguous, so this returns an error naming both rather than
// emitting a configuration Terraform would reject.
func AssembleRoot(componentIDs []string) ([]rendercore.RenderedFile, error) {
	var buf strings.Builder
	buf.WriteString("# Root configuration for this boundary. Each component rendered here is one\n")
	buf.WriteString("# module block below, and the directory that block points at holds that\n")
	buf.WriteString("# component's own files. One configuration means one dependency graph, which is\n")
	buf.WriteString("# what lets a reference or a depends_on reach from one component to another.\n")

	claimedBy := make(map[string]string, len(componentIDs))
	for _, id := range componentIDs {
		name := ModuleName(id)
		if first, taken := claimedBy[name]; taken {
			return nil, fmt.Errorf("components %q and %q both name the Terraform module %q in this boundary; a module name is unique within a configuration, so rename one of the components", first, id, name)
		}
		claimedBy[name] = id
		fmt.Fprintf(&buf, "\nmodule %q {\n  source = \"./%s/%s\"\n}\n", name, ModulesDirName, id)
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

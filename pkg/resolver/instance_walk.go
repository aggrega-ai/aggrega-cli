// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// instancePair is this package's diagnostic-facing view of one component
// instance: the developer-chosen instance name, the authored instance, and the
// enclosing subsystem's provenance. It is the unit yielded by
// [collectComponentInstances], which is a name-sorted projection of
// [types.NormalizedAggregate.ComponentInstances] — the iteration itself lives on
// the type, so a guard in this package cannot assemble a partial walk that
// misses subsystem components.
//
// SubsystemPath is the dotted path of the enclosing subsystem ("a" for
// `subsystems.a`, "a.b" for a nested `subsystems.a.subsystems.b`).
// [componentFieldPath] renders it into the authoring field locator used in
// diagnostics, so an error against a subsystem component points at
// `.spec.subsystems.<…>.components[…]`, not a flattened `.spec.components[…]`.
//
// Synthesized is true when the enclosing subsystem is the compiler-synthesized
// default scope — i.e. the component was authored at the top-level
// `.spec.components` surface and folded into the default during normalization.
// [componentFieldPath] renders a synthesized-scope component at the root shape
// the author actually typed, so the generated scope name never surfaces in a
// diagnostic.
type instancePair struct {
	name          string
	inst          types.ComponentInstance
	subsystemPath string
	synthesized   bool
}

// componentFieldPath renders the authoring field locator for a component
// instance. A synthesized-default component renders at the root shape
// `.spec.components["name"]` — the location the author typed — regardless of the
// dotted subsystem path the normalization folded it under. Otherwise it renders
// the real location: `.spec.components["name"]` at the top level,
// `.spec.subsystems.a.components["name"]` in a declared subsystem, and
// `.spec.subsystems.a.subsystems.b.components["name"]` for nested subsystems —
// matching the dotted-aggregate-path convention used across the resolver guards.
// The key is the synthesized-provenance flag, not the path string: a subsystem a
// user declares with the name "default" still renders the qualified path.
func componentFieldPath(subsystemPath, name string, synthesized bool) string {
	if synthesized {
		subsystemPath = ""
	}
	return fmt.Sprintf("%s.components[%q]", scopeFieldPath(subsystemPath), name)
}

// scopeFieldPath renders the authoring field locator for one subsystem scope:
// `.spec` at the top level, `.spec.subsystems.a` for a declared subsystem, and
// `.spec.subsystems.a.subsystems.b` for a nested one.
//
// It is the one home for expanding a dotted subsystem chain into the authored
// key path. Two locators need that expansion — a component instance's and a
// subsystem-wide attachment's — and a second copy would be free to render the
// same chain differently, which is exactly the divergence a reader could not
// account for when comparing two diagnostics about the same document.
func scopeFieldPath(subsystemPath string) string {
	if subsystemPath == "" {
		return ".spec"
	}
	return ".spec.subsystems." + strings.ReplaceAll(subsystemPath, ".", ".subsystems.")
}

// ScopeAttachmentFieldPath renders the authoring field locator for an attachment
// declared subsystem-wide: `.spec.subsystems.a.features["name"]`, and
// `.spec.features["name"]` at the top level.
//
// Exported because the rule that needs it runs where a node's attachment set is
// complete rather than where the Aggregate is walked, and the locator it carries
// must name the scope the author declared the attachment at. Rendering that path
// at the consuming site would put a second spelling of the authored key path in
// the tree.
func ScopeAttachmentFieldPath(subsystemPath, attachment string) string {
	return fmt.Sprintf("%s.features[%q]", scopeFieldPath(subsystemPath), attachment)
}

// collectComponentInstances projects [types.NormalizedAggregate.ComponentInstances]
// into this package's instancePair and sorts by instance name.
//
// The sort is this package's own concern, not the walk's: resolver guards emit
// diagnostics in instance-name order so the same input yields byte-identical
// error output across runs. Instance names are unique within an Aggregate's
// tree, so the sort key is unambiguous. The walk itself preserves author order
// for the byte-identical-output paths that need it, which is why the sort lives
// here and not on the type.
func collectComponentInstances(na types.NormalizedAggregate) []instancePair {
	refs := na.ComponentInstances()
	if len(refs) == 0 {
		return nil
	}
	out := make([]instancePair, 0, len(refs))
	for _, r := range refs {
		out = append(out, instancePair{
			name:          r.Name,
			inst:          r.Instance,
			subsystemPath: r.SubsystemPath,
			synthesized:   r.Synthesized,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"sort"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
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
//
// Synthesized is true when the enclosing subsystem is the compiler-synthesized
// default scope — i.e. the component was authored at the top-level
// `.spec.components` surface and folded into the default during normalization.
// A locator naming the component is the constraint framework's to assemble, and
// it names such a component at the root shape the author typed, so the
// generated scope name never surfaces in a diagnostic.
type instancePair struct {
	name          string
	inst          types.ComponentInstance
	subsystemPath string
	synthesized   bool
}

// ScopeAttachmentFieldPath returns the locator of an attachment declared
// subsystem-wide in the Aggregate: `.spec.subsystems.a.features.name`, with the
// container key repeated for each level of a nested subsystem.
//
// Exported because the rule that needs it runs where a node's attachment set is
// complete rather than where the Aggregate is walked, and the locator it carries
// must name the scope the author declared the attachment at. It is assembled by
// the constraint framework, through the same site every rule over the attachment
// is positioned by, so the two cannot come to spell one key differently — and in
// the one dialect the editor's position recovery walks.
//
// An empty subsystem path names no scope the author wrote, and yields the empty
// locator, which positions a finding at its document rather than at a key the
// document does not hold.
func ScopeAttachmentFieldPath(subsystemPath, attachment string) string {
	return constraint.AttachmentSiteInBlock(
		constraint.AuthoringDocumentAggregate,
		constraint.AuthoredSite{ScopePath: subsystemPath},
		attachment, "",
	).Locator()
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

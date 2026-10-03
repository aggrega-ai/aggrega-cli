// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// moduleAttachmentKind is the kind of document a developer finding from the
// connection-slot rule is reported against at this stage. The finding is about a
// sub-module's own document rather than the Aggregate the build was asked for, so
// it names that document by the file the component was authored in, and by the
// kind alone where the component carries no file — never by the root Aggregate's
// name, whose document does not contain the path the finding sits at.
const moduleAttachmentKind = "Aggregate"

// guardConnectionAttachmentSlots asks the declared-slots rule of the connections
// this stage is the first to hold the evidence for: the connections a component a
// sub-module contributes carries an attachment on in its own dependency block,
// and every entry the Release writes against any component.
//
// The developer's half is asked here only of a sub-module's component: its type
// is known only once its module's document has been read, which is what
// flattening does. The same half was asked of every component the Aggregate
// declares while the authoring documents were read, and it skips those, so each
// fault is reported once.
//
// The operator's half is asked here of every component, because this is where
// the Release's per-edge entries are enumerated with the site each was written at
// — the boundary it sits in and the keys reaching the component — so its finding
// names the entry the operator wrote.
//
// The findings are rendered through the conversions every rule reporting to a
// resolve's callers goes through: the developer's against the sub-module document
// the component was authored in, and the operator's against the Release, because
// those are the documents whose authors wrote the names.
func guardConnectionAttachmentSlots(
	cancelled constraint.Cancellation,
	cfg BuildConfig,
	view *topologyView,
) ([]*resolver.ResolverError, error) {
	if view == nil || cfg.ResolveResult == nil {
		return nil, nil
	}
	developer, err := constraint.RunOnEdges(
		cancelled, view, constraint.TopologyView(view), resolver.ModuleConnectionAttachmentFillsADeclaredSlot)
	if err != nil {
		return nil, err
	}
	findings := resolver.FindingsAtTheirOwnDocument(developer, moduleAttachmentKind, moduleAttachmentKind)
	if cfg.ResolveResult.Release == nil {
		return findings, nil
	}
	operator, err := constraint.RunOnReleaseEdgeConfigurations(
		cancelled, view, constraint.TopologyView(view), resolver.ReleaseConnectionAttachmentFillsADeclaredSlot)
	if err != nil {
		return nil, err
	}
	return append(findings,
		resolver.FindingsFromObjections(operator, releaseEntityName(cfg.ResolveResult.Release))...), nil
}

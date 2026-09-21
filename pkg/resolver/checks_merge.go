// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
)

// attachmentTypeAgreesAcrossDocuments reports each attachment both documents
// declare for one component under one name while naming different Features.
//
// Both documents may name one attachment — the Aggregate's declaration
// carries its configuration, the Release's carries the physical sizing — and
// the two are merged into a single attachment further down. That merge only
// means anything if the two name the same Feature; naming two is a statement
// with no resolution rather than a precedence question.
//
// Three shapes are legitimate and each ends the rule. An attachment only the
// Aggregate declares is nothing to reconcile. An attachment only the
// Release declares is the supported way to add one. An attachment naming no
// Feature on either side states nothing to disagree with, so only a name
// declared on both sides with a type on both sides is a question at all.
//
// The detail is the Feature the Aggregate names and then the Feature the
// Release names, in that order, so a reader can tell which document said
// which without a second lookup.
var attachmentTypeAgreesAcrossDocuments = constraint.Check[
	constraint.NormalizedView,
	constraint.NodeAttachmentSubject,
]{
	Sentinel: ErrFeatureMergeConflict(),
	Severity: constraint.SeverityFatal,
	Run: func(
		v constraint.NormalizedView,
		s constraint.NodeAttachmentSubject,
	) []constraint.Objection {
		authored, inAggregate := v.AuthoredAttachmentType(s.NodeID(), s.AttachmentName())
		if !inAggregate || authored == "" {
			return nil
		}
		deployed, inRelease := v.ReleaseAttachmentType(s.NodeID(), s.AttachmentName())
		if !inRelease || deployed == "" || deployed == authored {
			return nil
		}
		return []constraint.Objection{
			s.Raise(ErrFeatureMergeConflict(), authored, deployed),
		}
	},
}

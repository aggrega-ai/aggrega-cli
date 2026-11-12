// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package types

// Reserved label/annotation key prefixes owned by the platform. These are the
// single string home for the plain-prefix (strings.HasPrefix) consumers — the
// Recipe output-label validator and the Manifest annotation-binding loader —
// so the reserved keyspace is spelled once.
//
// One other representation of the same keyspace exists deliberately and is NOT
// a duplicate to be folded in here: the Terraform provenance-comment formatter
// filters on the aggrega.ai/ prefix inline while building an HCL comment, a
// different consumer.
//
// Fold a new plain-prefix HasPrefix consumer onto these constants; leave the
// comment-filter form as it is.
const (
	// ReservedLabelPrefixAggregaAI is the platform-native provenance keyspace.
	// Keys under it (e.g. aggrega.ai/component-id) are stamped by the compiler's
	// fixed provenance set; recipe-authored output labels may not use it.
	ReservedLabelPrefixAggregaAI = "aggrega.ai/"

	// ReservedLabelPrefixK8sRecommended is the Kubernetes recommended-label
	// namespace the provenance app.kubernetes.io/name key lives in. Recipe
	// output labels may not use it, keeping a validated output-label set
	// disjoint from the provenance set.
	ReservedLabelPrefixK8sRecommended = "app.kubernetes.io/"
)

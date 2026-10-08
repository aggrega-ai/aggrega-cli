// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package ir

import "github.com/aggrega-ai/aggrega-cli/pkg/types"

// RealizationBinding names the FeatureRecipe the Manifest binds to one
// attachment, as "<name>:<version>", from the attachment's name and the Feature
// type it names — or the empty string where the Manifest binds none.
//
// It is handed to the graph build rather than computed in this package because
// the binding has one home: the lookup the render binds every attachment by,
// which lives in a package that imports this one. A graph finalised from a
// binding of its own would name a FeatureRecipe the render is free to disagree
// with, and the two would agree only for as long as two readings of the
// Manifest happened to.
type RealizationBinding func(instanceName, featureType string) string

// bindRealizations records on each of one node's attachments the realization
// the binding names for it, under the attachment's own name — the same key the
// render binds the attachment by, its qualified name for an attachment a
// connection carries. A nil binding records nothing.
//
// The walk ranges an ordered map and rewrites each entry in place under its own
// key, so the order the attachments are emitted in is untouched.
func bindRealizations(attachments *types.OrderedMap[types.IRFeatureEntry], realizationOf RealizationBinding) {
	if realizationOf == nil {
		return
	}
	for name, entry := range attachments.All() {
		entry.Realization = realizationOf(name, entry.Type)
		attachments.Set(name, entry)
	}
}

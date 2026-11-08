// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// buildAttachmentInject constructs the top-level TemplateContext.Inject an
// attachment FeatureRecipe reads, from the attachment's authored
// IRFeatureEntry.Inject map (mechanism → key → value). Every mechanism the
// attachment authored is carried: env, file and arg in the pipe's own fields,
// each empty where the attachment authors nothing under it, and any other
// mechanism in Additional under its own name, so a template reads
// .inject.<mechanism> for a custom mechanism exactly as it reads .inject.env.
//
// The mechanisms are walked by deliveryMappingMechanisms and placed by
// injectPipeMechanism — the walk and the placement deliverAttachmentInjectKeys
// uses to deliver the same attachment into its host's pipe — so the two cannot
// disagree about which mechanisms an attachment carries. .All is built by the
// shared helper. Each key map is copied, so the pipe never aliases the entry it
// was built from.
func buildAttachmentInject(entry types.IRFeatureEntry) *types.InjectPipe {
	pipe := &types.InjectPipe{}
	for mech, keymap := range deliveryMappingMechanisms(entry.Inject) {
		dest, keep := injectPipeMechanism(pipe, mech)
		*dest = copyOrderedStringMap(keymap)
		keep()
	}
	pipe.All = buildInjectAll(*pipe)
	return pipe
}

// copyOrderedStringMap returns a shallow copy of src. Zero or empty input
// returns an empty OrderedMap so callers can treat the result uniformly.
func copyOrderedStringMap(src types.OrderedMap[string]) types.OrderedMap[string] {
	out := types.OrderedMap[string]{}
	for k, v := range src.All() {
		out.Set(k, v)
	}
	return out
}

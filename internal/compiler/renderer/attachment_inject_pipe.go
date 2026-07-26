// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// buildAttachmentInject constructs the top-level TemplateContext.Inject an
// attachment FeatureRecipe reads, from the attachment's authored
// IRFeatureEntry.Inject map (mechanism → key → value). Guarantees non-zero
// OrderedMaps for env/file/arg and builds .All via the shared helper. Custom
// mechanisms beyond env/file/arg are not surfaced today.
func buildAttachmentInject(entry types.IRFeatureEntry) *types.InjectPipe {
	env, _ := entry.Inject.Get("env")
	file, _ := entry.Inject.Get("file")
	arg, _ := entry.Inject.Get("arg")
	pipe := &types.InjectPipe{
		Env:  copyOrderedStringMap(env),
		File: copyOrderedStringMap(file),
		Arg:  copyOrderedStringMap(arg),
	}
	all := buildInjectAll(*pipe)
	pipe.All = all
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

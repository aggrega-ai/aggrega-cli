// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// buildSecretInject constructs the top-level TemplateContext.Inject for a
// credential FeatureRecipe from an IRSecretEntry.Inject map (mechanism → key → value).
// Guarantees non-nil maps for env/file/arg and builds .All via the shared helper.
// Custom mechanisms beyond env/file/arg are not surfaced in this iteration.
func buildSecretInject(entry types.IRSecretEntry) *types.InjectPipe {
	pipe := &types.InjectPipe{
		Env:  copyStringMap(entry.Inject["env"]),
		File: copyStringMap(entry.Inject["file"]),
		Arg:  copyStringMap(entry.Inject["arg"]),
	}
	pipe.All = buildInjectAll(*pipe)
	return pipe
}

// copyStringMap returns a guaranteed non-nil shallow copy of src.
// Nil or empty input returns a fresh empty map so callers can treat the result uniformly.
func copyStringMap(src map[string]string) map[string]string {
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"encoding/json"
	"sort"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// DeepMergeInject merges an overlay flat map over the base inject pipe.
// "Last wins" semantics: overlay values overwrite base values for the same key.
// Used in Phase A to merge injectMapping outputs over .context.inject.
func DeepMergeInject(base *types.InjectPipe, overlay map[string]string) {
	if base == nil || len(overlay) == 0 {
		return
	}

	// Phase A injectMapping outputs are flat key-value maps that merge into
	// .context.inject.env. Iterate overlay in sorted order for stability since
	// it arrives as a plain map.
	keys := make([]string, 0, len(overlay))
	for k := range overlay {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		base.Env.Set(k, overlay[k])
	}

	// Rebuild .all
	base.All = buildInjectAll(*base)
}

// DeepMergeInjectStructured merges a structured inject output (with mechanism keys)
// over the base inject pipe. Used when injectMapping returns JSON with env/file/arg keys.
func DeepMergeInjectStructured(base *types.InjectPipe, overlay *types.InjectPipe) {
	if base == nil || overlay == nil {
		return
	}

	mergeMechanism(&base.Env, overlay.Env)
	mergeMechanism(&base.File, overlay.File)
	mergeMechanism(&base.Arg, overlay.Arg)

	// Rebuild .all
	base.All = buildInjectAll(*base)
}

func mergeMechanism(base *types.OrderedMap[string], overlay types.OrderedMap[string]) {
	for k, v := range overlay.All() {
		base.Set(k, v)
	}
}

// ParseInjectMappingOutput parses the injectMapping template output (JSON string)
// into a structured inject pipe for deep-merging.
func ParseInjectMappingOutput(output string) (*types.InjectPipe, error) {
	output = trimWhitespace(output)
	if output == "" || output == "{}" {
		return nil, nil
	}

	// Try structured parse (with env/file/arg keys)
	var structured struct {
		Env  types.OrderedMap[string] `json:"env"`
		File types.OrderedMap[string] `json:"file"`
		Arg  types.OrderedMap[string] `json:"arg"`
	}
	if err := json.Unmarshal([]byte(output), &structured); err == nil {
		if structured.Env.Len() > 0 || structured.File.Len() > 0 || structured.Arg.Len() > 0 {
			return &types.InjectPipe{
				Env:  structured.Env,
				File: structured.File,
				Arg:  structured.Arg,
			}, nil
		}
	}

	// Fallback: try flat map (all goes to env)
	var flat types.OrderedMap[string]
	if err := json.Unmarshal([]byte(output), &flat); err == nil && flat.Len() > 0 {
		return &types.InjectPipe{Env: flat}, nil
	}

	return nil, nil
}

func trimWhitespace(s string) string {
	// Trim leading/trailing whitespace and newlines
	start := 0
	for start < len(s) && (s[start] == ' ' || s[start] == '\t' || s[start] == '\n' || s[start] == '\r') {
		start++
	}
	end := len(s)
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t' || s[end-1] == '\n' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"encoding/json"
	"sort"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// DeepMergeInject merges an overlay inject pipe over the base inject pipe.
// "Last wins" semantics: overlay values overwrite base values for the same key.
// Used in Phase A to merge injectMapping outputs over .context.inject.
func DeepMergeInject(base *types.InjectPipe, overlay map[string]string) {
	if base == nil || len(overlay) == 0 {
		return
	}

	// Overlay is a flat map — we need to determine which mechanism each key belongs to.
	// Phase A injectMapping outputs are flat key-value maps that merge into .context.inject.env.
	// The injectMapping template is expected to output JSON with mechanism keys.
	// For simple string overlays, merge into env by default.
	for k, v := range overlay {
		if base.Env == nil {
			base.Env = make(map[string]string)
		}
		base.Env[k] = v
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

func mergeMechanism(base *map[string]string, overlay map[string]string) {
	if len(overlay) == 0 {
		return
	}
	if *base == nil {
		*base = make(map[string]string)
	}
	keys := make([]string, 0, len(overlay))
	for k := range overlay {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		(*base)[k] = overlay[k]
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
		Env  map[string]string `json:"env"`
		File map[string]string `json:"file"`
		Arg  map[string]string `json:"arg"`
	}
	if err := json.Unmarshal([]byte(output), &structured); err == nil {
		if structured.Env != nil || structured.File != nil || structured.Arg != nil {
			return &types.InjectPipe{
				Env:  structured.Env,
				File: structured.File,
				Arg:  structured.Arg,
			}, nil
		}
	}

	// Fallback: try flat map (all goes to env)
	var flat map[string]string
	if err := json.Unmarshal([]byte(output), &flat); err == nil && len(flat) > 0 {
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

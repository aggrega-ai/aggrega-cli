// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parse"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// DeepMergeInjectStructured merges a structured inject output (with mechanism
// keys) over the base inject pipe. Used when injectMapping returns YAML or
// JSON with env/file/arg/all keys plus optional custom mechanisms via
// Additional. Custom mechanisms in overlay.Additional merge into
// base.Additional last-wins per mechanism; new mechanism slots are appended
// preserving overlay's author position.
func DeepMergeInjectStructured(base *types.InjectPipe, overlay *types.InjectPipe) {
	if base == nil || overlay == nil {
		return
	}

	mergeMechanism(&base.Env, overlay.Env)
	mergeMechanism(&base.File, overlay.File)
	mergeMechanism(&base.Arg, overlay.Arg)
	mergeAdditional(&base.Additional, overlay.Additional)

	// Rebuild .all
	base.All = buildInjectAll(*base)
}

func mergeMechanism(base *types.OrderedMap[string], overlay types.OrderedMap[string]) {
	for k, v := range overlay.All() {
		base.Set(k, v)
	}
}

// mergeAdditional walks overlay.Additional in author order; for each mechanism
// present in base, merges keys last-wins into the existing inner map; for each
// mechanism absent from base, copies the overlay's inner OrderedMap (preserving
// the overlay's author key order) and appends at base's tail.
func mergeAdditional(base *types.OrderedMap[types.OrderedMap[string]], overlay types.OrderedMap[types.OrderedMap[string]]) {
	for mech, overlayInner := range overlay.All() {
		baseInner, exists := base.Get(mech)
		if !exists {
			fresh := types.OrderedMap[string]{}
			for k, v := range overlayInner.All() {
				fresh.Set(k, v)
			}
			base.Set(mech, fresh)
			continue
		}
		for k, v := range overlayInner.All() {
			baseInner.Set(k, v)
		}
		base.Set(mech, baseInner)
	}
}

// ParseInjectMappingOutput parses the injectMapping template output (YAML or
// JSON; yaml.v3 reads valid JSON natively) into a structured inject pipe for
// deep-merging.
//
// Structured parse goes through types.InjectPipe's typed unmarshal hook:
// env / file / arg / all decode into the typed fields; everything else routes
// into Additional preserving author order. Empty output and parse failures
// fall through to the flat-map fallback (every key under .env), matching the
// "Recipe author writes a flat env dictionary" idiom.
//
// It also reports the top-level mechanism names the STRUCTURED parse found, in
// the order the output wrote them: the three typed mechanisms wherever their
// field is non-zero, then every key the output routed into Additional. That
// second result is what a reader needs to say whether a mechanism the host
// component declared reached this mapping's output, and it cannot be recovered
// afterwards — the merge folds the overlay into one pipe carrying no record of
// which mapping wrote what.
//
// It is empty, rather than absent, for the two outputs that name no mechanism:
// an output that parsed as a flat delivery dictionary, and one that parsed as
// nothing at all. A flat output does not advertise structured intent — it is the
// legitimate idiom for a Recipe producing env-only deliveries — so it makes no
// claim about which mechanisms it fills, and reading it as claiming none would
// turn every such Recipe into a refusal.
func ParseInjectMappingOutput(output string) (*types.InjectPipe, []string) {
	output = trimWhitespace(output)
	if output == "" || output == "{}" {
		return nil, nil
	}

	// Structured parse via InjectPipe's typed unmarshal hook (env/file/arg/all
	// → typed fields; default → Additional).
	var structured types.InjectPipe
	if err := parse.TemplateOutput([]byte(output), &structured); err == nil {
		if !structured.Env.IsZero() || !structured.File.IsZero() || !structured.Arg.IsZero() || structured.Additional.Len() > 0 {
			return &structured, structuredMechanisms(&structured)
		}
	}

	// Fallback: try flat map (all goes to env).
	var flat types.OrderedMap[string]
	if err := parse.TemplateOutput([]byte(output), &flat); err == nil && flat.Len() > 0 {
		return &types.InjectPipe{Env: flat}, nil
	}

	return nil, nil
}

// structuredMechanisms names the top-level mechanism keys one structurally
// parsed InjectPipe carries: the three typed mechanisms wherever their field is
// non-zero, then every key routed into Additional in the order the output wrote
// them.
//
// The typed mechanisms are read from the field's zero state and the rest from
// key presence, which is the same distinction the unmarshal hook makes when it
// routes a key: a typed field exists whether or not the output wrote it, so
// emptiness is the only thing that separates the two cases there, while an
// Additional key exists only because the output named it. So a custom mechanism
// written with no deliveries under it counts as named, and a typed mechanism
// written empty does not — which is the reading the hook itself imposes and not
// a choice made here.
func structuredMechanisms(p *types.InjectPipe) []string {
	if p == nil {
		return nil
	}
	var out []string
	if !p.Env.IsZero() {
		out = append(out, "env")
	}
	if !p.File.IsZero() {
		out = append(out, "file")
	}
	if !p.Arg.IsZero() {
		out = append(out, "arg")
	}
	for mechanism := range p.Additional.All() {
		out = append(out, mechanism)
	}
	return out
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

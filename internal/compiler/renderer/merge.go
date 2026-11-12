// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
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
// into Additional preserving author order. A structured read that fails or
// fills nothing falls through to the flat-map fallback (every key under .env),
// matching the "Recipe author writes a flat env dictionary" idiom.
//
// A structured read that filled only the all view is returned rather than
// passed to the fallback, so the caller can see what it wrote there:
// [DeepMergeInjectStructured] rebuilds that view from the mechanisms rather than
// merging it, so deliveries written under it reach nothing, and
// [undeliveredInjectMappingKeys] reads them off the returned pipe. Such an output
// could not have been read as a flat dictionary either — its value under all is a
// dictionary, never one delivery — so the fallback had nothing to give it.
//
// It also reports the top-level mechanism names the STRUCTURED parse found, in
// the order the output wrote them: the three typed mechanisms wherever their
// field is non-zero, then every key the output routed into Additional. That
// second result is what a reader needs to say whether a mechanism the host
// component declared reached this mapping's output, and it cannot be recovered
// afterwards — the merge folds the overlay into one pipe carrying no record of
// which mapping wrote what.
//
// It is empty, rather than absent, for the outputs that name no mechanism: an
// output that parsed as a flat delivery dictionary, one that parsed as nothing at
// all, and one that wrote only the all view, which is no mechanism. A flat output does not advertise structured intent — it is the
// legitimate idiom for a Recipe producing env-only deliveries — so it makes no
// claim about which mechanisms it fills, and reading it as claiming none would
// turn every such Recipe into a refusal.
//
// The error is non-nil for exactly one outcome: an output that is not empty and
// reads as neither shape — the structured read and the flat read both fail. It
// is the structured read's failure, which describes the output against the
// shape a mapping documents, and its target type is [types.InjectPipe], so a
// caller withholding its text re-reads a substituted output into that type. An
// output that reads as either shape and delivers nothing is not an error: it
// parsed, and what it says is that this mapping delivers nothing. Telling the
// two apart is the point of returning the error at all — an output that does
// not parse delivers none of its keys, and a caller that could not tell that
// from an empty one would drop every delivery it carried without a word.
func ParseInjectMappingOutput(output string) (*types.InjectPipe, []string, error) {
	output = trimWhitespace(output)
	if output == "" || output == "{}" {
		return nil, nil, nil
	}

	// Structured parse via InjectPipe's typed unmarshal hook (env/file/arg/all
	// → typed fields; default → Additional).
	var structured types.InjectPipe
	structuredErr := parse.TemplateOutput([]byte(output), &structured)
	if structuredErr == nil {
		if !structured.Env.IsZero() || !structured.File.IsZero() || !structured.Arg.IsZero() ||
			structured.Additional.Len() > 0 || !structured.All.IsZero() {
			return &structured, structuredMechanisms(&structured), nil
		}
	}

	// Fallback: try flat map (all goes to env).
	var flat types.OrderedMap[string]
	flatErr := parse.TemplateOutput([]byte(output), &flat)
	if flatErr == nil && flat.Len() > 0 {
		return &types.InjectPipe{Env: flat}, nil, nil
	}

	if structuredErr != nil && flatErr != nil {
		return nil, nil, structuredErr
	}
	return nil, nil, nil
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

// injectAllViewKey is the top-level key the unmarshal hook routes into the
// pipe's all view, spelled here only to name it in a report: which slot reaches
// nothing is decided by the field below, not by this name.
const injectAllViewKey = "all"

// undeliveredInjectMappingKeys names the top-level keys one parsed injectMapping
// overlay wrote deliveries under that [DeepMergeInjectStructured] does not apply,
// each beside the FeatureRecipe the mapping was read from.
//
// The answer follows the merge rather than a list of names. The merge applies
// the three typed mechanisms and every key the unmarshal hook routed into
// Additional — a custom mechanism, delivered on the pipe under its own name — and
// rebuilds the all view from those afterwards. The all view is therefore the one
// slot an output can fill that reaches nothing, and a delivery written under it
// is lost while the output still parses.
//
// It is read from the view's entries rather than from the key's presence: a view
// written empty loses no delivery, and the typed field cannot tell an empty one
// from an absent one, which is the same distinction [structuredMechanisms] draws
// for the typed mechanisms.
func undeliveredInjectMappingKeys(p *types.InjectPipe, recipe string) []constraint.InjectMappingUndeliveredKey {
	if p == nil || p.All.IsZero() {
		return nil
	}
	return []constraint.InjectMappingUndeliveredKey{{Key: injectAllViewKey, Recipe: recipe}}
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

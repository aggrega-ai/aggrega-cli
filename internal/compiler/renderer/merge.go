// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"

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
// Dangling-reference check. When the structured parse produces non-empty
// content AND the caller passes a non-nil authored-mechanism set (the set of
// mechanisms the Developer declared on the Component or Feature attachment's
// inject block), every authored mechanism must have a corresponding top-level
// key in the parsed output. Authored mechanisms missing from the output halt
// with ErrUnmappedCustomMechanism — the binding between Developer-authored
// mechanism and Recipe-author-provided template key fails to resolve. The
// check runs before the flat-map fallback so flat-map outputs (which do not
// advertise structured intent) continue to work for callers authoring only
// inject.env. Callers passing nil authored skip the check.
func ParseInjectMappingOutput(output string, authored []string) (*types.InjectPipe, error) {
	output = trimWhitespace(output)
	if output == "" || output == "{}" {
		return nil, nil
	}

	// Structured parse via InjectPipe's typed unmarshal hook (env/file/arg/all
	// → typed fields; default → Additional).
	var structured types.InjectPipe
	if err := parse.TemplateOutput([]byte(output), &structured); err == nil {
		if !structured.Env.IsZero() || !structured.File.IsZero() || !structured.Arg.IsZero() || structured.Additional.Len() > 0 {
			for _, mech := range authored {
				if !mechanismPresent(&structured, mech) {
					return nil, fmt.Errorf("%w: mechanism %q has no matching key in injectMapping output", ErrUnmappedCustomMechanism, mech)
				}
			}
			return &structured, nil
		}
	}

	// Fallback: try flat map (all goes to env). The dangling-reference check
	// is intentionally skipped — flat output is the legitimate idiom for
	// Recipes producing env-only deliveries.
	var flat types.OrderedMap[string]
	if err := parse.TemplateOutput([]byte(output), &flat); err == nil && flat.Len() > 0 {
		return &types.InjectPipe{Env: flat}, nil
	}

	return nil, nil
}

// mechanismPresent reports whether the parsed InjectPipe carries a top-level
// key for the named mechanism. Core mechanisms (env / file / arg) check the
// typed field's zero state; everything else checks Additional.Get.
func mechanismPresent(p *types.InjectPipe, mech string) bool {
	switch mech {
	case "env":
		return !p.Env.IsZero()
	case "file":
		return !p.File.IsZero()
	case "arg":
		return !p.Arg.IsZero()
	default:
		_, ok := p.Additional.Get(mech)
		return ok
	}
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

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"io"
)

// inspectLevel selects the verbosity of a per-kind inspector's rendering.
//
//   - levelList   — identifiers only (the names a reader scans to find a thing).
//   - levelDefault — a structured overview (the interactive-use default).
//   - levelDetail  — full bodies (every field a kind carries).
//
// Each per-kind renderer reads the level and shapes its own output; the
// semantics above are uniform across kinds.
type inspectLevel int

const (
	levelDefault inspectLevel = iota
	levelList
	levelDetail
)

// resolvedMode carries the --raw / --resolved override as a tri-state. The
// zero value modeDefault means "neither flag set" — each kind then applies its
// own natural default (some kinds render as-authored, others render the
// resolved structure). The override flags force the off-default direction.
type resolvedMode int

const (
	modeDefault resolvedMode = iota
	modeForcedRaw
	modeForcedResolved
)

// outputFormat selects the serialization of a render subject. outputText is the
// per-kind pretty-print; outputYAML emits the render subject as YAML.
type outputFormat int

const (
	outputText outputFormat = iota
	outputYAML
)

// colorMode is the parsed --color value. colorAuto enables color only when the
// output sink is an interactive terminal; colorAlways and colorNever force the
// decision regardless of the sink.
type colorMode int

const (
	colorAuto colorMode = iota
	colorAlways
	colorNever
)

// inspectOptions is the resolved rendering policy threaded through dispatch to
// every per-kind renderer. It is built once in runInspect from the cobra-bound
// flag variables and passed by value; renderers are read-only consumers and
// never see the cobra flag variables directly. The out writer is the output
// sink — renderers write to it rather than os.Stdout, so tests inject a buffer
// without swapping global state.
type inspectOptions struct {
	level    inspectLevel
	mode     resolvedMode
	output   outputFormat
	color    colorMode
	out      io.Writer
	colorize *colorizer
	// showImplements opts a Recipe / FeatureRecipe inspection into resolving
	// its spec.implements reference against the catalog and rendering the bound
	// Component / Feature. When set and the catalog or the implements target is
	// unavailable, the renderer aborts with a clear error rather than degrading.
	showImplements bool
	// flat collapses a tree-shaped render (the Aggregate subsystem tree, the
	// Release boundary tree) into a flat list whose every node carries its full
	// dotted path as a prefix. It is a no-op for kinds whose default render is
	// already flat (the tabular Manifest); those renderers ignore it.
	flat bool
}

// parseColorMode maps the --color string to a colorMode, returning a clear
// error naming the accepted set on an unrecognized value. cobra has no native
// string-enum, so the validation happens post-parse here.
func parseColorMode(s string) (colorMode, error) {
	switch s {
	case "auto":
		return colorAuto, nil
	case "always":
		return colorAlways, nil
	case "never":
		return colorNever, nil
	default:
		return colorAuto, fmt.Errorf("invalid --color value %q; expected one of auto, always, never", s)
	}
}

// parseOutputFormat maps the --output string to an outputFormat, returning a
// clear error naming the accepted set on an unrecognized value.
//
// It deliberately does not delegate to parseFormat: parseFormat validates a
// value and hands it back for the caller to switch on, whereas inspect's formats
// are a typed enum threaded through the whole inspect option set, so routing
// through it would validate and then switch a second time on the same string.
// The two are a near-duplicate on purpose, not an oversight.
func parseOutputFormat(s string) (outputFormat, error) {
	switch s {
	case "text":
		return outputText, nil
	case "yaml":
		return outputYAML, nil
	default:
		return outputText, fmt.Errorf("invalid --output value %q; expected one of text, yaml", s)
	}
}

// resolveLevel collapses the mutually-exclusive --list / --detail booleans into
// an inspectLevel. Neither set yields levelDefault. cobra's
// MarkFlagsMutuallyExclusive rejects both-set before this is reached.
func resolveLevel(list, detail bool) inspectLevel {
	switch {
	case list:
		return levelList
	case detail:
		return levelDetail
	default:
		return levelDefault
	}
}

// resolveMode collapses the mutually-exclusive --raw / --resolved booleans into
// a resolvedMode tri-state. Neither set yields modeDefault (per-kind default
// applies).
func resolveMode(raw, resolved bool) resolvedMode {
	switch {
	case raw:
		return modeForcedRaw
	case resolved:
		return modeForcedResolved
	default:
		return modeDefault
	}
}

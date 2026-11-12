// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"fmt"
	"slices"
	"sort"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// parserPluginType is the plugin-type value a parser plugin self-reports and the
// catalog filters on when resolving a format to its parser. It mirrors the host
// PluginType vocabulary without importing the wasm package (which would invert
// the dependency direction); the value is the single string both sides agree on.
const parserPluginType = "parser"

// parseCapabilityPrefix is the fixed prefix a parser plugin's advertised format
// capability carries: a parser handling HCL advertises the capability
// "parse:hcl". The resolver composes the prefix with a requested format to build
// the exact capability string it matches against a plugin's declared
// capabilities. The prefix is the host-side home of the parser capability
// vocabulary — a parser that advertises a format writes "parse:<format>", and
// the resolver reads it back through this one constant rather than re-spelling
// the literal at the match site.
//
// The full capability is matched by exact membership, never split on its colon:
// a version-style split would misread "parse:hcl" as a name "parse" pinned to
// version "hcl". The format space is defined entirely by what parser plugins
// advertise; the compiler holds no enumeration of formats.
const parseCapabilityPrefix = "parse:"

// ErrNoParserForFormat fires at resolution when a requested source format has no
// parser plugin advertising it — no pluginType: parser Plugin lists the
// "parse:<format>" capability. A requested format the catalog cannot parse is an
// explicit request the compiler cannot honor, so it halts loudly rather than
// silently passing the source through unparsed. The wrapped message names the
// composed capability so the diagnostic points at the missing advertiser. The
// format space is open — a new format ships as a new parser plugin, no core
// change — so this halt reflects the advertised set, not a fixed enumeration.
func ErrNoParserForFormat() error { return errNoParserForFormat }

var errNoParserForFormat = cerrors.Register("no-parser-for-format", cerrors.Meta{
	ShortCode: "E0098",
	Message:   "no parser plugin advertises the requested format",
	RulePlace: cerrors.RuleNoStageReaches("The parser socket evaluates the rule, when a source document's declared format is routed to " +
		"the parser plugin advertising it and neither the catalog nor the parsers the compiler carries holds one. " +
		"The routing settles how the document is read at all, so it runs ahead of every declared stage and no " +
		"stage holds the advertised capability set it matches against."),
})

// ErrAmbiguousParserPlugin fires at resolution when a requested format is
// advertised by more than one distinct parser plugin and the catalog cannot pick
// one without guessing. A federated catalog where two publishers each ship a
// parser for the same format is legal and loads without error; the ambiguity
// halts only when that format is actually requested. The wrapped message names
// every candidate, sorted, so the diagnostic is byte-identical across runs. It is
// the parser-socket sibling of the provider socket's ambiguity halt, scoped to
// the parser type and phrased for the parser surface.
func ErrAmbiguousParserPlugin() error { return errAmbiguousParserPlugin }

var errAmbiguousParserPlugin = cerrors.Register("ambiguous-parser-plugin", cerrors.Meta{
	ShortCode: "E0091",
	Message:   "format is advertised by more than one parser plugin",
	RulePlace: cerrors.RuleNoStageReaches("The parser socket evaluates the rule, when a source document's declared format is routed to " +
		"the parser plugin advertising it and more than one does. The routing settles how the document is " +
		"read at all, so it runs ahead of every declared stage and no stage holds the advertised " +
		"capability set it matches against."),
})

// ParserPluginForFormat resolves a requested source format to the parser Plugin
// entity that advertises it. It composes the "parse:<format>" capability and
// matches it by exact membership against each pluginType: parser Plugin's
// declared capabilities — the same capability-membership model the provider
// socket uses, run as an independent sibling so the provider resolution path is
// untouched. A non-parser plugin whose capabilities happen to list the composed
// string is excluded by the plugin-type filter, and its own capabilities live in
// a different per-type block besides.
//
// Unlike the provider resolver's tri-state (a bare miss is a soft not-found the
// caller resolves), every parser miss is loud: a requested format with no
// advertiser is ErrNoParserForFormat, and a format advertised by more than one
// distinct parser plugin is ErrAmbiguousParserPlugin. There is no soft-miss
// fallback because a parser has no zero-config default to fall back to — the
// requested format either has an advertiser or the build cannot proceed.
//
// When exactly one parser plugin advertises the format, its version is chosen by
// the shared three-form version select (highest available), so multiple versions
// of one parser coexist and resolve deterministically. The enumeration reads the
// view's own Plugin index — every Plugin entity the view loaded, a pulled one
// included — and never the binary's embedded entities, whatever the view's
// embedded-resolution posture: a core parser the binary embeds is selected here
// only through a Plugin entity of its coordinate in the catalog, whose module the
// plugin cascade then reads from the binary. The parsers the binary carries stand
// behind this resolution rather than inside it: the parser socket's consumers
// consult them only for a format this one answers ErrNoParserForFormat for, so
// they never join the candidates counted here and never make an ambiguity.
//
// Determinism (byte-identical output): candidate names sort before entering a
// diagnostic and the version select sorts its candidates, so no outcome depends
// on map-iteration order.
func (c *Catalog) ParserPluginForFormat(format string) (types.Plugin, error) {
	capability := parseCapabilityPrefix + format

	matches := map[string]types.Plugin{}
	distinctNames := map[string]struct{}{}
	for key, p := range c.Plugins {
		if p.Metadata.PluginType != parserPluginType {
			continue
		}
		if !slices.Contains(p.DeclaredCapabilities(), capability) {
			continue
		}
		matches[key] = p
		distinctNames[p.Metadata.Name] = struct{}{}
	}

	switch len(distinctNames) {
	case 0:
		return types.Plugin{}, fmt.Errorf("%w: no parser plugin advertises %q", ErrNoParserForFormat(), capability)
	case 1:
		// Exactly one parser repository — fall through to version select.
	default:
		names := make([]string, 0, len(distinctNames))
		for n := range distinctNames {
			names = append(names, n)
		}
		sort.Strings(names)
		return types.Plugin{}, fmt.Errorf("%w: format %q is advertised by parser plugins %v", ErrAmbiguousParserPlugin(), format, names)
	}

	var base string
	for n := range distinctNames {
		base = n
	}
	key, ok, err := selectEntityVersion(matches, base, func(v types.Plugin) (string, string) {
		return v.Metadata.Name, v.Metadata.Version
	})
	if err != nil || !ok {
		// The base name matched above, so selectEntityVersion returns a version
		// only when the sole parser carries an unresolvable version shape; treat
		// that as no advertiser for the format rather than a silent pass.
		return types.Plugin{}, fmt.Errorf("%w: no parser plugin version resolves for %q", ErrNoParserForFormat(), capability)
	}
	return matches[key], nil
}

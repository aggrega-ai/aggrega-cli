// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/ociref"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// importerPluginType is the plugin-type value an importer plugin self-reports and
// the catalog filters on when resolving a format to its importer. It mirrors the
// host PluginType vocabulary without importing the wasm package (which would
// invert the dependency direction); the value is the single string both sides
// agree on.
const importerPluginType = "importer"

// importCapabilityPrefix is the fixed prefix an importer plugin's advertised
// format capability carries: an importer handling the demo format advertises the
// capability "import:demo". The resolver composes the prefix with a requested
// format to build the exact capability string it matches against a plugin's
// declared capabilities. The prefix is the host-side home of the importer
// capability vocabulary — an importer that advertises a format writes
// "import:<format>", and the resolver reads it back through this one constant
// rather than re-spelling the literal at the match site. It mirrors the parser
// socket's parse:<format> convention; the format space is defined entirely by
// what importer plugins advertise, so the compiler holds no enumeration of
// formats.
const importCapabilityPrefix = "import:"

// ErrNoImporterForFormat fires at resolution when a requested source format has
// no importer plugin advertising it — no pluginType: importer Plugin lists the
// "import:<format>" capability. A requested format the catalog cannot import is
// an explicit request the compiler cannot honor, so it halts loudly rather than
// silently passing the source through unimported. The wrapped message names the
// composed capability so the diagnostic points at the missing advertiser. The
// format space is open — a new format ships as a new importer plugin, no core
// change — so this halt reflects the advertised set, not a fixed enumeration.
func ErrNoImporterForFormat() error { return errNoImporterForFormat }

var errNoImporterForFormat = cerrors.Register("no-importer-for-format", cerrors.Meta{
	ShortCode: "E0097",
	Message:   "no importer plugin advertises the requested format",
	RulePlace: cerrors.RuleNoStageReaches("An import evaluates the rule, when the requested source format is routed to the importer " +
		"plugin advertising it and the catalog holds none. The import translates a foreign source " +
		"description into entities before there is anything to compile, so no declared stage of a " +
		"compilation holds the advertised capability set the routing matches against."),
})

// ErrAmbiguousImporterPlugin fires at resolution when a requested format is
// advertised by more than one distinct importer plugin and the catalog cannot
// pick one without guessing. A federated catalog where two publishers each ship
// an importer for the same format is legal and loads without error; the ambiguity
// halts only when that format is actually requested. The wrapped message names
// every candidate, sorted, so the diagnostic is byte-identical across runs. It is
// the importer-socket sibling of the provider socket's ambiguity halt, scoped to
// the importer type and phrased for the importer surface.
func ErrAmbiguousImporterPlugin() error { return errAmbiguousImporterPlugin }

var errAmbiguousImporterPlugin = cerrors.Register("ambiguous-importer-plugin", cerrors.Meta{
	ShortCode: "E0090",
	Message:   "format is advertised by more than one importer plugin",
	RulePlace: cerrors.RuleNoStageReaches("An import evaluates the rule, when the requested source format is routed to the importer " +
		"plugin advertising it and more than one does. The import translates a foreign source description " +
		"into entities before there is anything to compile, so no declared stage of a compilation holds " +
		"the advertised capability set the routing matches against."),
})

// importerCapabilityDiag is the importer socket's diagnostic set for the shared
// capability select.
//
// Two of its halts are reachable on the importer path. The ambiguity halt fires
// when more than one importer advertises the requested format and no attachment
// names one of them, and ImporterPluginForFormat raises it itself, with the same
// closure, when attachments name more than one. The version-unsatisfiable slot
// fires when an attachment naming an advertiser carries a version the catalog
// does not hold, or a range no version it holds satisfies; it reuses the
// no-serving-importer sentinel. The binding-unsatisfied slot is never entered:
// ImporterPluginForFormat binds the select only to an importer it has already
// found advertising the format, so a bound select always has a candidate, and
// the slot reuses the same sentinel rather than one the path never emits.
var importerCapabilityDiag = capabilityDiag{
	noun:                 "importer plugin",
	bindingUnsatisfied:   ErrNoImporterForFormat(),
	versionUnsatisfiable: ErrNoImporterForFormat(),
	ambiguous: func(capability string, candidates []string) error {
		return fmt.Errorf("%w: format %q is advertised by importer plugins %v", ErrAmbiguousImporterPlugin(), strings.TrimPrefix(capability, importCapabilityPrefix), candidates)
	},
}

// ImporterPluginForFormat resolves a requested source format to the importer
// Plugin entity that serves it, and reports which of the workspace's importer
// attachments attaches that importer.
//
// attached is the uri of each plugins.importer attachment, in list order, and the
// returned index is the position in it of the attachment that attaches the
// returned importer, or -1 when none does. It is the one statement of which
// attachment that is, so the attachment that selects an importer and the
// attachment whose with-config that importer is handed cannot come apart.
//
// An attachment names an importer by its identity: its uri with any trailing
// ":<version>" split off is the importer's coordinate, <pluginType>/<name>. Only
// an attachment naming an importer that advertises the requested format takes
// part in this resolution. One naming any other plugin, or nothing this catalog
// holds, attaches nothing to this import and selects nothing.
//
//   - No attachment names an advertiser: the format is routed through the shared
//     capability select the provider socket uses — the one filter → disambiguate
//     → ambiguity-halt → version-select core — filtered to pluginType: importer
//     plugins whose declared capabilities list "import:<format>" by exact
//     membership, and the highest version of the one importer advertising it is
//     chosen. The index is -1.
//   - Attachments name one advertiser: the first of them binds the shared select
//     to that importer through the select's binding branch, so it is chosen
//     however many importers advertise the format, and a version on its uri pins
//     the version through the select's version branch, in the three forms any
//     version pin takes. The index is that attachment's.
//   - Attachments name more than one advertiser: ErrAmbiguousImporterPlugin,
//     naming the advertisers the attachments name, sorted.
//
// Every miss is loud: a requested format with no advertiser is
// ErrNoImporterForFormat (mapping the shared select's bare not-found), as is a
// well-formed version on an attachment that no advertised version of its importer
// satisfies — a malformed one is reported as the malformation, as the shared
// select reports any malformed pin — and a format advertised by more than one
// distinct importer plugin with no
// attachment naming one of them is ErrAmbiguousImporterPlugin. There is no
// soft-miss fallback because an importer has no zero-config default to fall back
// to — the requested format either has an advertiser or the import cannot
// proceed. A non-importer plugin whose capabilities happen to list the composed
// string is excluded by the plugin-type filter, and its own capabilities live in
// a different per-type block besides.
//
// Resolution routes through the same candidate set and select as the provider
// socket, so an importer is resolved by exactly the mechanism a provider is — the
// vocabulary and sentinels differ, the resolution core does not.
func (c *Catalog) ImporterPluginForFormat(format string, attached []string) (types.Plugin, int, error) {
	capability := importCapabilityPrefix + format

	at := -1
	var named []string
	for i, uri := range attached {
		name, advertises := c.importerAdvertising(ociref.VersionlessBase(uri), capability)
		if !advertises {
			continue
		}
		if at < 0 {
			at = i
		}
		if !slices.Contains(named, name) {
			named = append(named, name)
		}
	}
	if len(named) > 1 {
		sort.Strings(named)
		return types.Plugin{}, -1, importerCapabilityDiag.ambiguous(capability, named)
	}

	var bindings map[string]string
	if at >= 0 {
		bindings = map[string]string{capability: attached[at]}
	}
	p, ok, err := c.pluginForCapability(importerPluginType, capability, "", false, bindings, importerCapabilityDiag)
	if err != nil {
		return types.Plugin{}, -1, err
	}
	if !ok {
		return types.Plugin{}, -1, fmt.Errorf("%w: no importer plugin advertises %q", ErrNoImporterForFormat(), capability)
	}
	return p, at, nil
}

// importerAdvertising reports whether the importer at coordinate advertises
// capability, and that importer's name.
//
// It reads the set and the accessors the shared capability select filters on —
// providerResolutionPlugins, the plugin type, the coordinate a binding is
// compared with, and each plugin's DeclaredCapabilities — so an attachment it
// reports as naming an advertiser is one a select bound to it finds a candidate
// for, across every version the set holds. Every version of one coordinate
// carries the one name the coordinate is built from, so the name does not
// depend on which version the walk meets first.
func (c *Catalog) importerAdvertising(coordinate, capability string) (string, bool) {
	for _, p := range c.providerResolutionPlugins() {
		if p.Metadata.PluginType != importerPluginType || p.Coordinate() != coordinate {
			continue
		}
		if slices.Contains(p.DeclaredCapabilities(), capability) {
			return p.Metadata.Name, true
		}
	}
	return "", false
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"fmt"
	"strings"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
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
const ErrNoImporterForFormat cerrors.Code = "catalog/no-importer-for-format"

var _ = cerrors.Register(ErrNoImporterForFormat, cerrors.Meta{
	Message: "no importer plugin advertises the requested format",
})

// ErrAmbiguousImporterPlugin fires at resolution when a requested format is
// advertised by more than one distinct importer plugin and the catalog cannot
// pick one without guessing. A federated catalog where two publishers each ship
// an importer for the same format is legal and loads without error; the ambiguity
// halts only when that format is actually requested. The wrapped message names
// every candidate, sorted, so the diagnostic is byte-identical across runs. It is
// the importer-socket sibling of the provider socket's ambiguity halt, scoped to
// the importer type and phrased for the importer surface.
const ErrAmbiguousImporterPlugin cerrors.Code = "catalog/ambiguous-importer-plugin"

var _ = cerrors.Register(ErrAmbiguousImporterPlugin, cerrors.Meta{
	Message: "format is advertised by more than one importer plugin",
})

// importerCapabilityDiag is the importer socket's diagnostic set for the shared
// capability select. Only the ambiguity halt is reachable on the importer path:
// ImporterPluginForFormat resolves a format with no publisher binding and no
// version pin, so the shared select never enters the binding-unsatisfied or
// version-pin branches. The binding and version slots therefore reuse the
// no-serving-importer sentinel rather than introducing binding/version sentinels
// the path never emits.
var importerCapabilityDiag = capabilityDiag{
	noun:                 "importer plugin",
	bindingUnsatisfied:   ErrNoImporterForFormat,
	versionUnsatisfiable: ErrNoImporterForFormat,
	ambiguous: func(capability string, candidates []string) error {
		return fmt.Errorf("%w: format %q is advertised by importer plugins %v", ErrAmbiguousImporterPlugin, strings.TrimPrefix(capability, importCapabilityPrefix), candidates)
	},
}

// ImporterPluginForFormat resolves a requested source format to the importer
// Plugin entity that advertises it. It composes the "import:<format>" capability
// and routes it through the shared capability select the provider socket uses —
// the one filter → disambiguate → ambiguity-halt → version-select core — filtered
// to pluginType: importer plugins whose spec.capabilities list the composed
// string by exact membership. A non-importer plugin whose capabilities happen to
// list the composed string is excluded by the plugin-type filter.
//
// Every importer miss is loud: a requested format with no advertiser is
// ErrNoImporterForFormat (mapping the shared select's bare not-found), and a
// format advertised by more than one distinct importer plugin is
// ErrAmbiguousImporterPlugin. There is no soft-miss fallback because an importer
// has no zero-config default to fall back to — the requested format either has an
// advertiser or the import cannot proceed.
//
// When exactly one importer plugin advertises the format, its version is chosen
// by the shared three-form version select (highest available), so multiple
// versions of one importer coexist and resolve deterministically. Resolution
// routes through the same candidate set and select as the provider socket, so an
// importer is resolved by exactly the mechanism a provider is — the vocabulary
// and sentinels differ, the resolution core does not.
//
// This method satisfies the importer socket's ImporterResolver seam.
func (c *Catalog) ImporterPluginForFormat(format string) (types.Plugin, error) {
	capability := importCapabilityPrefix + format
	p, ok, err := c.pluginForCapability(importerPluginType, capability, "", false, nil, importerCapabilityDiag)
	if err != nil {
		return types.Plugin{}, err
	}
	if !ok {
		return types.Plugin{}, fmt.Errorf("%w: no importer plugin advertises %q", ErrNoImporterForFormat, capability)
	}
	return p, nil
}

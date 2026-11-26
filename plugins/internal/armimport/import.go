// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armimport

import (
	"fmt"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/armcore"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft/arm"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// Format is the arm import format: Azure Resource Manager templates, parsed as
// JSON with comments, drafted for the arm target. It reads a request's
// templates with [Read], maps their resources through [Table], leaves the
// substrate and the types no Recipe carries to its type rules, writes a
// pointer and a literal string as an arm template reads them, drafts a
// resource no row maps from the source as the arm-schema format drafts one,
// and keeps an Azure Verified Modules module no row maps whole, sourced from
// the Microsoft Container Registry with its telemetry turned off. Its pointer
// writer names a resource by a resourceId expression of its type and its
// name, read in the template read.
var Format = cloudimport.Format{
	Name:    formatName,
	Target:  target,
	Syntax:  syntax,
	Read:    Read,
	Table:   Table(),
	Types:   typeRules,
	Pointer: pointer,
	Text:    armcore.ArmLiteral,
	Draft:   arm.Format,
	Wrap:    wrap,
	Module:  module,
}

// Import turns one import request into the Aggregate, the Release and the
// catalog entities its Azure Resource Manager templates draft, as
// [cloudimport.Import] does for any format, through [Format].
func Import(in aggrega.ImportInput, parse cloudimport.ParseFunc) aggrega.ImportOutput {
	return cloudimport.Import(in, Format, parse)
}

// wrap drafts the Recipe sourcing a module no row maps as arm.WriteWrapped
// drafts it, publishing each output a neighbour reads. The reader reads a
// module's output o at the attribute outputs.o.value, as Azure Resource
// Manager's reference function returns it, and a member inside its value
// below that: outputs.o.value is published as the output o, which the module
// writer reads at outputs.o.value itself, and outputs.o.value.<member…> at
// that path. An attribute reading no output's value is not published, and a
// reference-not-wired warning names it and its provides key and says to remove
// the neighbour's inject entry and the drafted Component's provides key, as a
// comment above the neighbour's connection says too.
func wrap(m schemadraft.WrappedModule) (importer.RecipeDraft, []aggrega.Diagnostic) {
	var (
		outputs []schemadraft.Provide
		diags   []aggrega.Diagnostic
	)
	for _, p := range m.Outputs {
		a := p.Attribute
		if len(a) < 3 || !strings.EqualFold(a[0], "outputs") || !strings.EqualFold(a[2], "value") {
			diags = append(diags, cloudimport.Report(cloudimport.CodeReferenceNotWired, fmt.Sprintf("a neighbour "+
				"reads %s of the module %s, which is no output's value, read at outputs.<output>.value, nor a member "+
				"inside one, so the Recipe drafted for the module publishes nothing under the provides key %s, and "+
				"the build halts until it is unwired: remove the inject entry reading %s from the connection "+
				"delivering it and the provides key %s from the drafted Component, a comment above that connection "+
				"naming both, then read an output's value or write the value where the instance reading it is "+
				"configured", strings.Join(a, "."), m.URL, p.Key, p.Key, p.Key)))
			continue
		}
		if len(a) == 3 {
			p.Attribute = []string{a[1]}
		} else {
			p.Attribute = slices.Clone(a[1:])
		}
		outputs = append(outputs, p)
	}
	m.Outputs = outputs
	d, more := arm.WriteWrapped(m)
	return d, append(diags, more...)
}

// ModuleRegistry is the address under which the Microsoft Container Registry
// serves the Azure Verified Modules Bicep modules, as Bicep's br/public alias
// names it: a module's address is this followed by its path.
const ModuleRegistry = "oci://mcr.microsoft.com/bicep/"

// module locates an Azure Verified Modules module no row maps: the Bicep
// module the Microsoft Container Registry serves under its path, its
// telemetry turned off by the Recipe, as an imported module's telemetry is
// never carried. A module whose path the module index does not list is not
// located.
func module(ref cloudimport.ModuleRef) cloudimport.ModuleSource {
	if ref.Path == "" {
		return cloudimport.ModuleSource{}
	}
	return cloudimport.ModuleSource{Kind: "bicep-module-oci", URL: ModuleRegistry + ref.Path,
		Fixed: []importer.Entry{{Key: telemetryParameter, Value: "false"}}}
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armimport

import (
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/armcore"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
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
// the Microsoft Container Registry with its telemetry turned off.
//
// Its pointer writer reads the value alone, so it names only a resource the
// template reads and does not declare, by the resourceId call of literal
// arguments the reference is written as. [Import] is the complete arm import:
// the pointer to a resource the template declares names it by its type and
// its name, and a parameter naming a resource by the value the parameters
// file or its default gives it, which only the template read tells, so Import
// hands its pointer writer that template. The core writes the pointer whole in
// the Release entry of an instance drafted from the source, and names it in
// the comment of a key it leaves for the operator in a mapped instance's
// entry, where the catalog Recipe takes the resource's literal identifier.
var Format = cloudimport.Format{
	Name:    formatName,
	Target:  target,
	Syntax:  syntax,
	Read:    Read,
	Table:   Table(),
	Types:   typeRules,
	Pointer: func(v cloudimport.Value) (string, bool) { return pointer(nil, v) },
	Text:    armcore.ArmLiteral,
	Draft:   arm.Format,
	Wrap:    arm.WriteWrapped,
	Module:  module,
}

// Import turns one import request into the Aggregate, the Release and the
// catalog entities its Azure Resource Manager templates draft, as
// [cloudimport.Import] does for any format, through [Format]: its pointer
// writer reads the template read, so a pointer names a resource the template
// declares by its type and name, and a parameter by the value the parameters
// file gives it, else its literal default.
func Import(in aggrega.ImportInput, parse cloudimport.ParseFunc) aggrega.ImportOutput {
	f := Format
	var read *cloudimport.Graph
	f.Read = func(in aggrega.ImportInput, s cloudimport.Settings, parse cloudimport.ParseFunc) (*cloudimport.Graph,
		[]aggrega.Diagnostic) {
		g, diags := Format.Read(in, s, parse)
		read = g
		return g, diags
	}
	f.Pointer = func(v cloudimport.Value) (string, bool) { return pointer(read, v) }
	return cloudimport.Import(in, f, parse)
}

// moduleRegistry is the address under which the Microsoft Container Registry
// serves the Azure Verified Modules Bicep modules, as Bicep's br/public alias
// names it.
const moduleRegistry = "oci://mcr.microsoft.com/bicep/"

// module locates an Azure Verified Modules module no row maps: the Bicep
// module the Microsoft Container Registry serves under its path, its
// telemetry turned off by the Recipe, as an imported module's telemetry is
// never carried. A module whose path the module index does not list is not
// located.
func module(ref cloudimport.ModuleRef) cloudimport.ModuleSource {
	if ref.Path == "" {
		return cloudimport.ModuleSource{}
	}
	return cloudimport.ModuleSource{Kind: "bicep-module-oci", URL: moduleRegistry + ref.Path,
		Fixed: []importer.Entry{{Key: telemetryParameter, Value: "false"}}}
}

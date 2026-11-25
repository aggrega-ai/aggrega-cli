// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package armimport reads Azure Resource Manager templates into the resource
// graph the cloud importers share, and holds the mapping table by which a
// template's resources are taken to the catalog's Components and the arm
// Recipes realizing them.
//
// [Read] reads the templates and the parameters file an import is handed:
// templates of language version 1.0, whose resources are an array, and 2.0,
// whose resources are keyed by symbolic name, written with comments or
// without. Every string Azure Resource Manager reads as an expression is read
// with the armexpr package and lowered into the graph's values: a parameter,
// composed text, a reference to another resource's attribute, a credential a
// list function reads, or text the template evaluates when it deploys, kept as
// the expression it is. A reference is matched to the resource it names by
// the canonical print of its type and name, so one written through resourceId,
// a variable, concat or format names the resource alike. A template another
// deploys, held inline or linked by a relative path, is read as the resources
// it deploys, and an Azure Verified Modules resource module as one node of
// the module form, recognised by the Bicep metadata of its template and the
// telemetry it sends.
//
// The table is read from the hub's arm Recipes themselves — the resource or
// Azure Verified Modules module each writes, the Component it implements with
// that Component's labels, and what its text says literally of where a
// resource's properties go — and completed by hand with what no Recipe's text
// says; every arm Recipe and FeatureRecipe of the hub that no row reaches is
// listed with the reason.
package armimport

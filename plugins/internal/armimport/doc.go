// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package armimport holds the mapping table by which an Azure Resource Manager
// template's resources are taken to the catalog's Components and the arm
// Recipes realizing them. The table is read from the hub's arm Recipes
// themselves — the resource or Azure Verified Modules module each writes, the
// Component it implements with that Component's labels, and what its text
// says literally of where a resource's properties go — and completed by hand
// with what no Recipe's text says; every arm Recipe and FeatureRecipe of the
// hub that no row reaches is listed with the reason.
package armimport

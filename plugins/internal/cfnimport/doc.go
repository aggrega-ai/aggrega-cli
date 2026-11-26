// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package cfnimport holds the CloudFormation importer's mapping table, by which
// a template's resources are taken to the catalog's Components and the
// cloudformation Recipes realizing them.
//
// The table is read from the hub's cloudformation Recipes themselves — the
// resource each Recipe's fragment writes as the instance, the Component it
// implements with that Component's labels, and what its text says literally of
// where a resource's properties go, its own and those of the resources the
// cfnrules package reads as part of it — and completed by hand with what no
// Recipe's text says; every cloudformation Recipe and FeatureRecipe of the hub
// that no row reaches is listed with the reason. Beside the rows it lists the
// one feature type each Recipe's assertions admit at a credential slot.
package cfnimport

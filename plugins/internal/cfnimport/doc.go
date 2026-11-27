// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package cfnimport reads CloudFormation templates into the resource graph the
// cloud importers share, and holds the mapping table by which a template's
// resources are taken to the catalog's Components and the cloudformation
// Recipes realizing them.
//
// The reader reads the templates and the one parameters file an import is
// handed. Every template is read with the cfntemplate package, as
// CloudFormation reads it — YAML with short forms, or JSON — into its long
// form, so a value of a template is never taken from the host's parser; the
// host's parser reads a parameters file alone, as JSON, in any of the forms
// the AWS CLI reads from a file. The root template is chosen by the core's
// rule over the local paths by which templates link one another, a nested
// stack's TemplateURL and a serverless application's Location. Each resource
// becomes a node keyed by its logical ID, and an instance drafted from it is
// keyed by the ID's words ([Key]).
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

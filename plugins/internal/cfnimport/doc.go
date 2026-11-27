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
// A nested stack or a serverless application deploying a template the import
// reads is read as that template's resources, so the draft deploys them as
// one stack: each a node whose ID is the deploying resource's logical ID
// before its own, its parameters reading the values the deploying resource
// binds them to, and an output of it read where the template reads it. One
// deploying a template linked by a URL, or located by a value the template
// computes or by an application's ID, is reported and stays a node. A
// template the import reads under a transform that rewrites it when the
// stack deploys — AWS::Include, AWS::CodeDeployBlueGreen, a macro of the
// account, or a loop of the language extensions — is refused, naming it.
//
// [Read] then lowers each intrinsic function, dynamic reference and condition
// the template writes into the core's values: a parameter, a reference to a
// resource or its attribute, text composed of those, a secret a store holds,
// or deploy-time text written as one line of YAML short form, which the
// cloudformation target's Recipes read back as the function. Nothing the stack
// learns when it deploys is read: a pseudo parameter, an imported value and a
// dynamic reference stay text CloudFormation evaluates then, and a dynamic
// reference is never resolved.
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

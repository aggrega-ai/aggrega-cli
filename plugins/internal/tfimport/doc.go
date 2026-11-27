// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package tfimport holds the mapping table by which a Terraform or OpenTofu
// configuration's resources and module calls are taken to the catalog's
// Components and the terraform Recipes realizing them, and the rules by which
// one resource of a configuration is read as part of another.
//
// The table's generated rows are read from the hub's terraform Recipes: a
// Recipe sourcing a published module by its repository gives a row keyed by
// the module's registry address, and one writing its resources, in its
// artifacts, in a local directory it sources or in an ArtifactLibrary body it
// imports, a row keyed by the type of the one resource no other resource of
// the Recipe references. The rows the hand file writes, the Whens it writes
// for Recipes whose text does not tell their resources apart, and the Recipes
// no row reaches, each with why, sit beside them ([Table]).
//
// A read-in rule ([ReadIns]) names an attribute by which a resource references
// another it owns, such as the task definition an Amazon ECS service runs: the
// owned resource's attributes are read in place of the reference, beneath that
// attribute, so the referencing resource's row places them as its own. The
// importer reads a configuration by these rules and the table's generator
// reads the hub's Recipes by the same ones.
package tfimport

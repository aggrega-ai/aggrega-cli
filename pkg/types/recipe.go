// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package types

//go:generate go run ../../internal/tools/typegen recipe_gen.go

// Recipe's struct declarations (Recipe, RecipeMetadata, RecipeSpec) are
// generated into recipe_gen.go from the published recipe-v1beta1 schema;
// their godoc is the schema's descriptions. The content-shape and shared
// types the generated spec references stay hand-written with their
// marshalers — ValuesMapping (valuesmapping.go), WorkloadTemplate
// (workload_template.go), ArtifactEntry (artifact.go), and PipelineStep +
// Maintainer (common.go); the generator emits fields referencing them,
// never the types themselves.

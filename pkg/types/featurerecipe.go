// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package types

//go:generate go run ../../internal/tools/typegen featurerecipe_gen.go

// FeatureRecipe's struct declarations (FeatureRecipe, FeatureRecipeMetadata,
// FeatureRecipeSpec) are generated into featurerecipe_gen.go from the
// published featurerecipe-v1beta1 schema; their godoc is the schema's
// descriptions. The content-shape and shared types the generated spec
// references stay hand-written with their marshalers — ValuesMapping
// (valuesmapping.go), WorkloadTemplate (workload_template.go), ArtifactEntry
// (artifact.go), and PipelineStep + Maintainer (common.go); the generator
// emits fields referencing them, never the types themselves.

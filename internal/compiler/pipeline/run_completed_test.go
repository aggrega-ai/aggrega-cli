// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline_test

import (
	"context"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// runPipelineCompleted drives the pipeline and hands back the payload of a run
// that FINISHED, or nil beside the error when the run stopped.
//
// It exists for the tests whose subject is what a completed build produces —
// artifacts, the graph, the trace. Those tests assert on the error first and then
// read the payload, so resolving the verdict once here keeps each of them reading
// what it means to read instead of restating the same two steps.
//
// A test whose subject is the outcome ITSELF — what a run that stopped still
// reports — drives [pipeline.RunPipeline] directly. Routing such a test through
// this helper would discard the very thing it is asserting about, so the helper
// deliberately offers no way to reach a stopped run's findings.
func runPipelineCompleted(
	ctx context.Context,
	inputs pipeline.Inputs,
	catalogLookup resolver.CatalogLookup,
	recipeLookup renderer.RecipeLookup,
	subAggregateLoader topology.SubAggregateLoader,
	opts pipeline.Options,
) (*pipeline.Result, error) {
	outcome, err := pipeline.RunPipeline(ctx, inputs, catalogLookup, recipeLookup, subAggregateLoader, opts)
	return outcome.Completed(), err
}

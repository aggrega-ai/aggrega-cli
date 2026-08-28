// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

// EmitOrigin tags the input kind that produced an EmittedFile. The tag is
// load-bearing for the filename-collision rule: what separates two blocks
// meeting on one output name from one block emitting once is which blocks
// arrived, so the rule reads these tags and nothing else. Two distinct tags
// under one name produce a CATALOG_ERROR under the filename-collision rule,
// while one tag repeating is one contributor rather than a collision
// (duplicate artifacts: entry keys are refused when the document is
// decoded; engine-internal logic covers duplicate sourced or synthesized
// output).
type EmitOrigin int

const (
	// EmitOriginSource — the file originated from the Recipe's source: input
	// kind (Helm chart, Terraform module, compose file overlay).
	EmitOriginSource EmitOrigin = iota

	// EmitOriginWorkload — the file originated from the Recipe's workload:
	// input kind (inline or $import:-loaded), translated by the provider's
	// WorkloadSynthesizer.
	EmitOriginWorkload

	// EmitOriginArtifactsInline — the file originated from an artifacts: entry
	// authored with inline content. Reserved for the artifacts render path
	// (Phase B.3 is a stub today).
	EmitOriginArtifactsInline

	// EmitOriginArtifactsImport — the file originated from an artifacts:
	// entry authored with $import: (file path). Reserved for the artifacts
	// render path.
	EmitOriginArtifactsImport

	// EmitOriginPipeline — the file originated from a pipeline step's EMIT
	// side effect (a new document a Wasm guest contributed to the node's
	// own or host set). Distinct from the render-time origins above so the
	// filename-collision rule can name a pipeline EMIT that clashes with a
	// rendered filename.
	EmitOriginPipeline
)

// String returns a human-readable origin tag suitable for inclusion in
// CATALOG_ERROR messages. It is what the filename-collision rule names as
// the blocks that met on an output name.
func (o EmitOrigin) String() string {
	switch o {
	case EmitOriginSource:
		return "source"
	case EmitOriginWorkload:
		return "workload"
	case EmitOriginArtifactsInline:
		return "artifacts-inline"
	case EmitOriginArtifactsImport:
		return "artifacts-import"
	case EmitOriginPipeline:
		return "pipeline"
	default:
		return "unknown"
	}
}

// EmittedFile carries one rendered file plus the input-kind origin that
// produced it. Once a render's emission is complete it is reduced to a
// projection holding, per output name, which blocks produced it — and the
// filename-collision rule reads that projection, reporting a name more than
// one block produced under the filename-collision CATALOG_ERROR.
//
// EmittedFiles accumulate in declaration order across the render's passes
// to satisfy the byte-for-byte deterministic-output invariant. The
// projection sorts its names so the order a rule is invoked in is a
// function of the input, while the emitted-files list itself preserves the
// order they were produced.
//
// Intermediate=true marks a file as transient input to a BoundaryAssembler
// (see synthesis.go). The orchestrator collects intermediates into the
// per-(path, provider) dispatch state for the assembler to consume but does
// NOT write them at the dispatch path directly. Used by the compose and
// helm-chart providers: per-node Workload synthesis (or Render) emits one
// fragment file per node; the per-(path, provider) BoundaryAssembler at
// the contributor's evaluated path reads the fragments and emits the
// canonical assembled file (one docker-compose.yaml or one umbrella
// Chart.yaml + values.yaml) at the dispatch path, replacing every fragment.
type EmittedFile struct {
	Name         string     // dispatch-path-relative path (e.g., "deployment.yaml")
	Origin       EmitOrigin // input kind that produced this file
	Body         []byte     // file contents
	Intermediate bool       // true → consumed by BoundaryAssembler; not written to disk directly
	// Format is the producer-declared render format of this file (e.g. "hcl",
	// "json"), carried verbatim from the rendering provider guest. Empty when the
	// guest declared none, in which case a downstream consumer selects the format
	// from the file extension instead. Additive metadata: it never affects the
	// emitted bytes.
	Format string
}

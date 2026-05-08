// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

// EmitOrigin tags the input kind that produced an EmittedFile. The tag is
// load-bearing for Phase B.5 filename-collision detection — collisions
// across input kinds produce a CATALOG_ERROR per chapter 16 rule 7, while
// collisions within a single input kind are caught by other rules (rule 6
// at load time for artifacts vs artifactsFrom; engine-internal logic for
// duplicate sourced or synthesized output).
type EmitOrigin int

const (
	// EmitOriginSource — the file originated from the Recipe's source: input
	// kind (Helm chart, Terraform module, compose file overlay).
	EmitOriginSource EmitOrigin = iota

	// EmitOriginWorkload — the file originated from the Recipe's
	// workload: / workloadFrom: input kind, translated by the provider's
	// WorkloadSynthesizer.
	EmitOriginWorkload

	// EmitOriginArtifactsInline — the file originated from an entry in the
	// Recipe's artifacts: inline map. Reserved for artifacts-debug-proof
	// WI01 (Phase B.3 stub today).
	EmitOriginArtifactsInline

	// EmitOriginArtifactsFromFile — the file originated from a path in the
	// Recipe's artifactsFrom: list. Reserved for artifacts-debug-proof
	// WI01.
	EmitOriginArtifactsFromFile
)

// String returns a human-readable origin tag suitable for inclusion in
// CATALOG_ERROR messages (see Phase B.5 collision detector).
func (o EmitOrigin) String() string {
	switch o {
	case EmitOriginSource:
		return "source"
	case EmitOriginWorkload:
		return "workload"
	case EmitOriginArtifactsInline:
		return "artifacts-inline"
	case EmitOriginArtifactsFromFile:
		return "artifacts-from-file"
	default:
		return "unknown"
	}
}

// EmittedFile carries one rendered file plus the input-kind origin that
// produced it. Phase B.5's collision detector groups EmittedFiles by Name
// and reports cross-origin collisions via the rule 7 CATALOG_ERROR.
//
// EmittedFiles accumulate in declaration order across the sub-pass pipeline
// to satisfy the byte-for-byte deterministic-output invariant — the
// collision detector sorts findings alphabetically for output stability,
// but the emitted-files list itself preserves the order they were
// produced.
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
}

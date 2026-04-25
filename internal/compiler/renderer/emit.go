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
// per the contract's deterministic-output invariant — the collision detector
// sorts findings alphabetically for output stability, but the emitted-files
// list itself preserves the order they were produced.
//
// Intermediate=true marks a file as transient input to a ReleaseComposer
// (see synthesis.go). The orchestrator collects intermediates into the
// per-boundary state for ReleaseComposer to consume but does NOT write them
// under boundaries/<bp>/ in the final output. Used by the compose provider:
// per-node Workload synthesis emits one fragment file per node; the compose
// ReleaseComposer reads the fragments and emits a single docker-compose.yaml
// at the Release output root, replacing every fragment.
type EmittedFile struct {
	Name         string     // boundary-relative path (e.g., "deployment.yaml")
	Origin       EmitOrigin // input kind that produced this file
	Body         []byte     // file contents
	Intermediate bool       // true → consumed by ReleaseComposer; not written to dist
}

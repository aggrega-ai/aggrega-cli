// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package helmchartcore

import "github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"

// RenderSourcedChartCore rebuilds the sub-chart's path→bytes map from a source
// projection: each projected file becomes a map entry keyed by its chart-root-
// relative, slash-separated path with its content verbatim, except the chart's
// own Chart.yaml, whose `name` is rewritten to the name derived from identity.
// It is the filesystem-free render core — it consumes the in-memory projection
// and never reads from disk, which is what lets a provider Wasm guest, which
// reconstructs the projection from the payload, drive this one
// byte-identical-critical path. It is exported so that guest, which lives in a
// separate package, can drive this core rather than carrying its own copy of it.
// The orchestrator prefixes the returned keys with
// `boundaries/<boundary>/charts/<instance_id>/` when placing the sub-chart under
// the umbrella.
//
// The name is rewritten because a chart's name is the key Helm binds an
// umbrella's dependency to a vendored sub-chart by, and that name is written by
// whoever authored the chart rather than by whoever deploys it. Two instances
// vendoring charts that declare one name would leave both of the umbrella's
// dependencies pointing at one of them: one chart's templates would render under
// both aliases and the other's under none, with every value still arriving, so
// nothing downstream reads as wrong. Deriving the placed name from the instance
// identity instead removes the shared name rather than refusing it, which keeps
// the two legitimate cases working — the same chart vendored twice, and an
// acquired chart whose name the author cannot change.
//
// Nothing else in the chart is touched, and the rewrite is a byte-span edit of
// that one value rather than a re-serialization, so a comment, a key order or a
// quoting style an author chose survives. It aligns the file with what already
// happens at render rather than changing it: an aliased sub-chart already
// renders under its alias, so a template reading its chart's own name already
// read the alias before this rewrite and reads the same value after it.
//
// identity is the identity the placed chart is addressed by, and it is the
// CONTRIBUTOR's own. For a component that is the same identity the umbrella's
// alias is derived from, so there the placed name and the dependency entry
// cannot come to disagree. A Feature attachment is a contributor in its own
// right and carries its own composed identity here, so an attachment's chart
// and its host's declare different names rather than both declaring the host's.
// Either way the value is encoded through the one shared encoding rather than a
// second derivation. indent is the document indentation width, carried for the
// edit path and unused by a scalar replacement.
//
// A projection carrying no Chart.yaml is returned unchanged: this core does not
// require one, because the loud guardrail for a chart source genuinely missing
// its metadata is the sourced-listing validation that already runs before it.
func RenderSourcedChartCore(proj *rendercore.SourceProjection, identity string, indent int) (map[string][]byte, error) {
	out := make(map[string][]byte, len(proj.Files))
	for _, f := range proj.Files {
		out[f.Path] = f.Content
	}
	manifest, has := out[chartMetadataFileName]
	if !has {
		return out, nil
	}
	// The same decode-and-guardrail the umbrella assembly runs, so a vendored
	// Chart.yaml this core cannot own its name in halts with the message that
	// file's own defect already had rather than a second one phrased differently.
	if _, _, err := ReadChartMetaBytes(manifest, identity); err != nil {
		return nil, err
	}
	rewritten, err := rendercore.SetTopLevelScalar(manifest, "name", helmNameEncoding.Segment(identity), indent)
	if err != nil {
		return nil, err
	}
	out[chartMetadataFileName] = rewritten
	return out, nil
}

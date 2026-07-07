// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package terraform

import (
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
)

// ProvenanceFileName is the output filename of the standalone build-provenance
// file the terraform guest emits alongside a vendored module or a .tf.json
// artifact body. A JSON body (aggrega.auto.tfvars.json, or a .tf.json artifact)
// cannot carry a comment, so provenance is stamped in a separate comment-only
// .tf file the Terraform loader parses as an empty (comment-only) config.
const ProvenanceFileName = "aggrega.provenance.tf"

// PlaceArtifacts places a terraform node's pre-evaluated artifact set into the
// node's output. A terraform config body (HCL or .tf.json) is not Kubernetes
// YAML, so the build provenance the projection carries as label/annotation sets
// cannot ride in metadata; those sets are intentionally unused here (see the
// guest call site). Instead the shared terraform comment formatter renders the
// node's provenance once, and placement is format-aware, keyed on whether any
// emitted body is JSON:
//
//   - Pure-HCL node (no body name ends in .json — main.tf, *.tf, *.tftpl): the
//     provenance is prepended as a leading `#` comment block, riding at the top
//     of each body.
//   - Any JSON body present (a *.tf.json artifact): a `#` prefix would make the
//     body invalid JSON, so every body emits verbatim and the provenance travels
//     in a single companion comment-only file (ProvenanceFileName), the same
//     filename the sourced guest uses for its JSON tfvars overlay. The companion
//     is the sole provenance carrier so the block is never duplicated across
//     surfaces.
//
// The provenance identity is drawn from the node's Recipe identity via the shared
// [rendercore.FormatTerraformProvenanceComment] and [rendercore.ProvenanceRecipeURIFromNodeCtx]
// — the same formatter the terraform sourced-render path uses — so the stamped
// identity is identical across every terraform emission surface. Entries are
// placed in author order (the order the projection carries them); the companion,
// when present, is appended last.
func PlaceArtifacts(proj rendercore.ArtifactsProjection, nodeCtx rendercore.NodeRenderContext) []rendercore.RenderedFile {
	provComment := rendercore.FormatTerraformProvenanceComment(nodeCtx, rendercore.ProvenanceRecipeURIFromNodeCtx(nodeCtx))

	hasJSONBody := false
	for _, ent := range proj.Entries {
		if strings.HasSuffix(ent.Name, ".json") {
			hasJSONBody = true
			break
		}
	}

	out := make([]rendercore.RenderedFile, 0, len(proj.Entries)+1)
	for _, ent := range proj.Entries {
		body := ent.Content
		if !hasJSONBody {
			// Copy provComment per body so the shared slice is never aliased into
			// an emitted file.
			body = append(append([]byte{}, provComment...), ent.Content...)
		}
		out = append(out, rendercore.RenderedFile{Path: ent.Name, Content: body})
	}
	if hasJSONBody {
		out = append(out, rendercore.RenderedFile{Path: ProvenanceFileName, Content: provComment})
	}
	return out
}

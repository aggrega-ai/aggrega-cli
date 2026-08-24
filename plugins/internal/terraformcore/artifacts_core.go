// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package terraformcore

import (
	"encoding/json"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
)

// ProvenanceFileName is the output filename of the standalone build-provenance
// file the terraform guest emits alongside a vendored module or a .tf.json
// artifact body. A vendored module's files stay byte-for-byte verbatim, and a
// JSON body (a .tf.json artifact, or the sensitivity override) cannot carry a
// comment at all, so provenance is stamped in a separate comment-only .tf file
// the Terraform loader parses as an empty (comment-only) config.
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
//     filename the sourced guest uses beside a vendored module. The companion is
//     the sole provenance carrier so the block is never duplicated across
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
		if strings.HasSuffix(ent.Name, ".tf.json") {
			// Recipe output labels ride as per-resource tags on structurally
			// addressable JSON bodies only. A body that carries no taggable
			// resource, or that does not parse, is emitted byte-for-byte
			// unchanged (the verbatim-emission contract); native-HCL bodies never
			// reach this branch and stay opaque.
			body = injectResourceTags(ent.Content, nodeCtx.OutputLabels)
		}
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

// injectResourceTags merges recipe output labels into the tag attribute of each
// top-level resource block in a Terraform JSON (.tf.json) body, best-effort.
//
// The offline render sandbox has no provider schema, so taggability is inferred
// from local evidence only: a resource block that already declares a "tags"
// object (the AWS / azurerm convention) or a "labels" object (the Google
// convention) is taggable, and the declared attribute names where the tags go.
// A block with neither is left untouched — inventing an attribute would emit
// invalid Terraform for a non-taggable resource. The computed "tags_all"
// attribute is never written. An author-declared tag key is authoritative: an
// output label never overwrites a key the block already sets.
//
// A body that does not parse as JSON, carries no "resource" block, or gains no
// tag is returned byte-for-byte unchanged (the verbatim-emission contract). A
// body that is modified is re-serialized deterministically (object keys sorted);
// key order and whitespace are not semantically load-bearing in Terraform JSON.
func injectResourceTags(content []byte, labels map[string]string) []byte {
	if len(labels) == 0 {
		return content
	}
	var root map[string]any
	if err := json.Unmarshal(content, &root); err != nil {
		return content
	}
	resources, ok := root["resource"].(map[string]any)
	if !ok {
		return content
	}
	changed := false
	for _, byType := range resources {
		byName, ok := byType.(map[string]any)
		if !ok {
			continue
		}
		for _, block := range byName {
			blk, ok := block.(map[string]any)
			if !ok {
				continue
			}
			for _, attr := range []string{"tags", "labels"} {
				existing, ok := blk[attr].(map[string]any)
				if !ok {
					continue
				}
				for k, v := range labels {
					if _, present := existing[k]; !present {
						existing[k] = v
						changed = true
					}
				}
			}
		}
	}
	if !changed {
		return content
	}
	reemitted, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return content
	}
	return append(reemitted, '\n')
}

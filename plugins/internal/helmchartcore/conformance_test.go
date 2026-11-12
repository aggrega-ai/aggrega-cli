// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package helmchartcore

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/codecconformance"
	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
)

// TestHelmChartCodecConformance holds the helm-chart structure-with-holes codec
// to the shared codec contract over the real Helm template shapes plus the
// multi-document adversarial classes. The corpus category coverage:
//
//   - value-position holes (patchable, structured): a plain value hole, a
//     colon-inside-action hole, a quoted-brace action, a dict-literal action, a
//     value-with-pipe action, and a non-template values.yaml. These carry an
//     op-apply case that replaces a non-template scalar.
//   - control-flow and structural regions (opaque, not patchable): {{ if }},
//     {{ range }}, {{ with }}, {{ define }}, an include … | nindent injection,
//     a toYaml injection, an interpolated scalar, and a root-level conditional.
//     These carry a halt op-apply case: the codec declines a tree, so applying
//     an op to the (nil) tree-view halts — the codec-side half of the
//     not-structured guardrail. (The walk-level Loud Guardrail that surfaces it
//     to the operator is asserted in the renderer package.)
//   - multi-document classes: a separator between two value-hole documents
//     (each addressable), a separator inside a block scalar (must not split),
//     and a mixed value-and-control-flow file (whole-file opaque).
//   - the RFC-6902 op-apply cases, which the codec serves by delegating
//     non-template YAML to the stock node-span codec.
//   - the absent-token guardrail.
func TestHelmChartCodecConformance(t *testing.T) {
	t.Parallel()
	codec := NewHelmChartCodec(2)

	corpus := helmChartCorpus()
	corpus = append(corpus, codecconformance.RFC6902Cases()...)
	codecconformance.Run(t, codec, corpus)

	if err := codecconformance.CheckAbsentTokenHalts(codec); err != nil {
		t.Error(err)
	}
}

// helmChartCorpus maps the real-Helm shapes and the multi-document adversarial
// shapes onto conformance cases. A structured (value-hole / non-template) shape
// carries an op-apply case that replaces a clean scalar; an opaque
// (control-flow / interpolated / structure-emitting) shape carries a halt
// op-apply case (the codec declines a tree, so the apply step halts).
func helmChartCorpus() []codecconformance.Case {
	var out []codecconformance.Case

	for _, sh := range realHelmShapes() {
		c := codecconformance.Case{
			Name:     "real/" + sh.name,
			Filename: sh.name,
			Input:    []byte(sh.body),
		}
		if sh.structured {
			c.Ops = []frp.JSONPatchOp{patchFor(sh.name)}
		} else {
			// Opaque shape: the codec declines a tree, so the op-apply family
			// must halt (StockApplier errors on the nil tree-view).
			c.Ops = []frp.JSONPatchOp{{Op: "replace", Path: "/x", Value: "y"}}
			c.WantHalt = true
		}
		out = append(out, c)
	}

	// Multi-document adversarial shapes. Each rides the round-trip + determinism
	// families; the control-flow-mixed file is whole-file opaque (halt op-apply),
	// and the block-scalar-separator file is a single structured document.
	for _, sh := range multidocAdversarialShapes() {
		c := codecconformance.Case{
			Name:     "multidoc/" + sh.name,
			Filename: sh.name,
			Input:    []byte(sh.body),
		}
		switch sh.name {
		case "multidoc-mixed-value-and-controlflow":
			// Whole-file opaque: the blanked scaffold does not parse as a whole.
			c.Ops = []frp.JSONPatchOp{{Op: "replace", Path: "/x", Value: "y"}}
			c.WantHalt = true
		case "sep-inside-block-scalar":
			// The block-scalar carries a template action, so the document's
			// structure is indeterminate (interpolated hole) and the codec
			// carves it opaque — round-trip + determinism only, no patch.
		default:
			// multidoc-two-value-holes: both documents are structured value
			// holes; round-trip + determinism only (per-document patch isolation
			// is covered by the helm codec's own tests).
		}
		out = append(out, c)
	}

	return out
}

// patchFor returns a replace op against a clean non-template scalar for a
// structured value-hole shape, addressing a path that exists in that shape's
// body and is NOT a template action (so the patch lands surgically without
// overwriting a hole).
func patchFor(name string) frp.JSONPatchOp {
	switch name {
	case "plain-value-hole":
		// Body has no non-template scalar; replace the tag hole's value position
		// with a literal (a legitimate guest overwrite).
		return frp.JSONPatchOp{Op: "replace", Path: "/image/tag", Value: "1.0.0"}
	case "value-with-quote-pipe":
		return frp.JSONPatchOp{Op: "replace", Path: "/spec/replicas", Value: float64(2)}
	case "non-template-values":
		return frp.JSONPatchOp{Op: "replace", Path: "/image/tag", Value: "1.26"}
	case "colon-in-action":
		return frp.JSONPatchOp{Op: "add", Path: "/metadata/annotations/added", Value: "v"}
	case "quoted-action-brace":
		return frp.JSONPatchOp{Op: "replace", Path: "/data/other", Value: "literal"}
	case "dict-literal-nested-brace":
		return frp.JSONPatchOp{Op: "replace", Path: "/data/x", Value: "literal"}
	default:
		return frp.JSONPatchOp{Op: "add", Path: "/added", Value: "v"}
	}
}

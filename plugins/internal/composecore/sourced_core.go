// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Filesystem-free sourced-mode rendering core for the compose provider. A Recipe
// with `source: <path>` plus `valuesMapping:` produces:
//
//  1. The base compose file is parsed as a single YAML document.
//  2. The valuesMapping output (the values dict already evaluated by Phase
//     B.1) is treated as a compose-overlay document.
//  3. The two are deep-merged per docker-compose's native overlay semantics:
//     scalars: overlay wins, maps merge, lists are REPLACED (not appended) —
//     this matches `docker compose -f base.yaml -f overlay.yaml config`
//     behaviour.
//  4. The services, volumes and configs the node's omit entries name are left
//     out of the merged document (see leaveOut), which an overlay alone cannot
//     do: an overlay value replaces, it never deletes.
//
// The merged compose document is emitted as one Intermediate=true
// EmittedFile (`<nodeID>.compose-fragment.yaml`) so it joins the Release-
// level merge alongside synthesized-mode fragments.
//
// RenderSourcedCore is the byte-identical-critical part of sourced-mode
// rendering: it receives the base compose bytes in memory and never reads from
// disk, so the caller supplies the source however it obtained it — the compose
// provider plugin reconstructs it from its render input's source projection.
// This file carries no build tag: it compiles for the host and for the wasip1
// guest alike, so it imports no host-only capability (no filesystem, no
// network).

package composecore

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// SourcedRender is what one sourced compose render returns.
type SourcedRender struct {
	// Files is the rendered fragment, keyed by its file name.
	Files map[string][]byte
	// Omitted holds one count per omit entry the render was handed, in the order
	// they arrived: the number of units that entry left out. It is nil for a render
	// handed none.
	Omitted []int
}

// RenderSourcedCore parses the base compose bytes, deep-merges the values dict
// on top, leaves out the units the node's omit entries name, injects the
// compose-bridge gateway (no-op for non-bridge renders) and the aggrega.ai/*
// provenance labels, and emits an Intermediate=true compose-fragment for the
// boundary merger to consume. sourcePath is the operator-facing source identifier
// threaded through only for diagnostics so the parse-error messages match the
// source the host read.
//
// The omit entries are applied by leaveOut, after the overlay and before anything
// the render adds; a render handed none emits exactly the fragment it would emit
// if entries did not exist.
//
// Exported so the compose provider plugin — a separate main package, which
// cannot reach an unexported compose function — renders through this core rather
// than reimplementing the parse-and-overlay it describes.
func RenderSourcedCore(baseComposeBytes []byte, sourcePath string, values map[string]any, nodeCtx rendercore.NodeRenderContext) (SourcedRender, error) {
	// A Compose file is a single YAML document. Decode the first document, then
	// require the stream to be exhausted: a single file carrying `---`-separated
	// documents passes the source resolver's file-count guard, but
	// yaml.Unmarshal would silently keep only the first and drop the rest — a
	// silent misrender. Halt loudly instead (Loud Guardrails — the compiler does
	// not assume well-formed input).
	dec := yaml.NewDecoder(bytes.NewReader(baseComposeBytes))
	var base map[string]any
	if err := dec.Decode(&base); err != nil {
		if errors.Is(err, io.EOF) {
			base = map[string]any{} // empty source
		} else {
			return SourcedRender{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: sourcePath}, fmt.Sprintf("compose: node %q: parse source %q", nodeCtx.NodeID, sourcePath))
		}
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return SourcedRender{}, cerrors.Unidentified(cerrors.CatalogError, cerrors.SourceLocation{File: sourcePath},
			fmt.Sprintf("compose: node %q: source %q contains multiple YAML documents; a compose file must be a single document", nodeCtx.NodeID, sourcePath),
			nil, nil)
	}
	if base == nil {
		base = map[string]any{}
	}

	merged := deepMergeOverlay(base, values)
	// The omit entries see the document the overlay produced, so a unit the values
	// added is left out like one the source declared, and run before the gateway
	// and the labels below, so nothing the render adds is ever matched.
	omitted, err := leaveOut(merged, nodeCtx.Omits, nodeCtx.NodeID)
	if err != nil {
		return SourcedRender{}, err
	}
	// compose-bridge: when the fragment carries the bridge marker, generate the
	// gateway's nginx stream config and mount it as a compose config, then strip
	// the marker. No-op for every non-bridge sourced render.
	if err := injectBridgeGateway(merged); err != nil {
		return SourcedRender{}, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("compose: node %q: bridge gateway", nodeCtx.NodeID))
	}
	origins := buildSingleOriginMap(merged, nodeCtx)
	injectAggregaLabelsIntoServices(merged, origins)

	body, err := rendercore.MarshalYAMLIndent(merged, nodeCtx.YAMLIndent)
	if err != nil {
		return SourcedRender{}, cerrors.Wrap(err, cerrors.TranslationError, cerrors.SourceLocation{File: nodeCtx.NodeID}, fmt.Sprintf("compose: node %q: marshal merged source", nodeCtx.NodeID))
	}

	// Output path mirrors synthesized-mode: <nodeID>.compose-fragment.yaml.
	// Both flow into the same boundary merger via Intermediate=true. The
	// orchestrator's per-node Phase B sub-passes wrap the file into an
	// EmittedFile and tag origin/intermediate; sourced-mode origin is
	// EmitOriginSource per the existing convention.
	return SourcedRender{
		Files:   map[string][]byte{nodeCtx.NodeID + ".compose-fragment.yaml": body},
		Omitted: omitted,
	}, nil
}

// deepMergeOverlay merges overlay onto base using docker-compose overlay
// semantics:
//
//   - When both base[k] and overlay[k] are maps, recurse.
//   - When overlay[k] is a non-map value (scalar, list, nil), it replaces
//     base[k] entirely.
//   - When base lacks k, overlay[k] is added.
//   - When overlay lacks k, base[k] is preserved.
//
// "Is a map" means whatever asStringMap recognises as one, so a value it
// does not recognise takes the replace branch rather than the recurse
// branch. That helper's own comment carries what follows from it.
//
// Returns the merged map. Inputs may be nil; nil base treated as empty.
func deepMergeOverlay(base, overlay map[string]any) map[string]any {
	if base == nil {
		base = map[string]any{}
	}
	out := make(map[string]any, len(base)+len(overlay))
	for k, v := range base {
		out[k] = v
	}
	for k, ov := range overlay {
		bv, hasBase := out[k]
		if !hasBase {
			out[k] = ov
			continue
		}
		bm, baseIsMap := asStringMap(bv)
		om, overlayIsMap := asStringMap(ov)
		if baseIsMap && overlayIsMap {
			out[k] = deepMergeOverlay(bm, om)
			continue
		}
		// All other cases: overlay replaces base (lists, scalars, mixed).
		out[k] = ov
	}
	return out
}

// asStringMap normalises a YAML-decoded interface to map[string]any.
// yaml.v3 decodes maps as either map[string]any (when keys are
// always strings) or map[any]any (when keys are mixed). The
// merger needs string-keyed maps; this helper coerces the latter.
//
// Those two shapes are the whole set it recognises, which is correct while
// both merge inputs arrive as plain decoded containers: the base document is
// decoded into a map[string]any above, and the overlay is required to be one
// on the way in. That requirement is a contract on the caller rather than
// something the compiler enforces, and it is the fragile half of the
// arrangement. An order-preserving container decodes a nested object as
// *types.OrderedMap[any], which this helper reports as not-a-map — and an
// unrecognised shape is indistinguishable here from a genuine scalar, so
// deepMergeOverlay replaces where it should have recursed, with no error and
// no panic. Teaching either input to decode through an ordered container
// therefore adds an arm here for that shape in the same change.
func asStringMap(v any) (map[string]any, bool) {
	switch m := v.(type) {
	case map[string]any:
		return m, true
	case map[any]any:
		out := make(map[string]any, len(m))
		for k, val := range m {
			ks, ok := k.(string)
			if !ok {
				return nil, false
			}
			out[ks] = val
		}
		return out, true
	default:
		return nil, false
	}
}

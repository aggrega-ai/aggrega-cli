// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import (
	"fmt"

	cerrors "github.com/cognitio/aggrega-cli/pkg/errors"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// normalizeUses converts the polymorphic `uses` block authored on a
// Component or Feature into a flat slice of NormalizedEdge — the
// canonical edge representation downstream code reads. The function
// handles two authoring forms; the choice is per-socket, not per-block,
// so a single `uses` map may carry both shapes simultaneously:
//
//   - String-array form: `{"databases": ["db1", "db2"]}`. Each array
//     entry produces one edge; Alias equals Target.
//   - Verbose-map form: `{"databases": {"my-db": {"target": "corp-db",
//     "inject": {...}}}}`. Each map entry produces one edge; the map
//     key becomes Alias, the inner `target:` field becomes Target,
//     the inner `inject:` field is parsed into the edge's Inject block.
//     parseVerboseEntry handles the simple-string shorthand
//     (`"my-db": "target-name"`) where the entry is a bare string
//     rather than a map.
//
// Resolver-side guarantees from upstream Step 1 mean every socket name
// this function sees is already known to the source entity's
// usesSchema — the resolver enforces socket-allowlist and
// required-socket rules before topology runs. This package therefore
// performs no socket-validity check; ErrInvalidUsesFormat fires only
// when the SHAPE of a value is malformed (non-string array element,
// neither-array-nor-map socket value, etc.).
//
// Iteration order:
//
//   - Outer socket loop iterates uses.All() in author order — uses is
//     an OrderedMap[any] so YAML authoring order is preserved. The
//     TestNormalizeUses_DeterministicOrder regression test pins this
//     discipline.
//   - Inner verbose-map alias loop iterates the OrderedMap in author
//     order via .All(). Aggregate-author authoring sequence for verbose-
//     form aliases is preserved through the topology normalization
//     because the map entry value (`v` in the switch arm) is itself an
//     *OrderedMap[any] (gamma-decoded), not a plain Go map.
//
// The originFeature parameter is non-empty when the `uses` block being
// normalised lives on a Feature (or Secret) rather than directly on a
// Component. Each emitted edge carries the value into its OriginFeature
// field — the per-edge origin marker that keeps Feature-authored edges
// separable from Component-authored edges. IR's IREdge.OriginFeature is
// the downstream surface that consumes it.
func normalizeUses(uses types.OrderedMap[any], sourceNodeID, originFeature string) ([]NormalizedEdge, error) {
	if uses.Len() == 0 {
		return nil, nil
	}

	var edges []NormalizedEdge

	// Author-order socket iteration (OrderedMap)
	for socket, raw := range uses.All() {
		if raw == nil {
			continue
		}

		switch v := raw.(type) {
		case []any:
			// String array form: ["db1", "db2"]
			for _, item := range v {
				target, ok := item.(string)
				if !ok {
					return nil, cerrors.Wrap(ErrInvalidUsesFormat, cerrors.DeveloperError, cerrors.SourceLocation{}, fmt.Sprintf("socket %q: array element must be a string, got %T", socket, item))
				}
				edges = append(edges, NormalizedEdge{
					Source:        sourceNodeID,
					Target:        target,
					Socket:        socket,
					Alias:         target,
					OriginFeature: originFeature,
				})
			}

		case *types.OrderedMap[any]:
			// Verbose map form (gamma-decoded): {"alias": {"target": "...", "inject": {...}}}
			for alias, entry := range v.All() {
				edge, err := parseVerboseEntry(sourceNodeID, socket, alias, entry, originFeature)
				if err != nil {
					return nil, err
				}
				edges = append(edges, edge)
			}

		default:
			return nil, cerrors.Wrap(ErrInvalidUsesFormat, cerrors.DeveloperError, cerrors.SourceLocation{}, fmt.Sprintf("socket %q: expected array or map, got %T", socket, raw))
		}
	}

	return edges, nil
}

// parseVerboseEntry decodes one entry of a verbose-map `uses` socket
// into a NormalizedEdge. The verbose form admits two shapes per entry:
//
//   - Map: `{"target": "...", "inject": {...}}` — the canonical case.
//     The inner `target:` field overrides the alias as the edge's
//     Target; absence of `target:` defaults Target to alias (the map
//     key). The inner `inject:` field, when present, is parsed by
//     parseInjectBlock into the edge's Inject *types.InjectBlock.
//   - Bare string: `"target-name"` — shorthand for "Target equals this
//     string, no inject directives." Convenient when the only thing the
//     author wants to say is the destination instance name; semantically
//     equivalent to `{"target": "target-name"}`.
//
// Anything else (a number, a slice, a nil) returns ErrInvalidUsesFormat
// wrapping the offending Go type and the (socket, alias) pair so the
// diagnostic points the author at the precise authoring position.
func parseVerboseEntry(source, socket, alias string, raw any, originFeature string) (NormalizedEdge, error) {
	om, ok := raw.(*types.OrderedMap[any])
	if !ok {
		// Simple string value treated as target
		if s, ok := raw.(string); ok {
			return NormalizedEdge{
				Source:        source,
				Target:        s,
				Socket:        socket,
				Alias:         alias,
				OriginFeature: originFeature,
			}, nil
		}
		return NormalizedEdge{}, cerrors.Wrap(ErrInvalidUsesFormat, cerrors.DeveloperError, cerrors.SourceLocation{}, fmt.Sprintf("socket %q alias %q: expected map or string, got %T", socket, alias, raw))
	}

	target := alias // default target = alias name
	if t, ok := om.Get("target"); ok {
		if ts, ok := t.(string); ok {
			target = ts
		}
	}

	edge := NormalizedEdge{
		Source:        source,
		Target:        target,
		Socket:        socket,
		Alias:         alias,
		OriginFeature: originFeature,
	}

	if injectRaw, ok := om.Get("inject"); ok {
		inject, err := parseInjectBlock(injectRaw)
		if err != nil {
			return NormalizedEdge{}, cerrors.Wrap(err, cerrors.DeveloperError, cerrors.SourceLocation{}, fmt.Sprintf("socket %q alias %q", socket, alias))
		}
		edge.Inject = inject
	}

	return edge, nil
}

// parseInjectBlock decodes the inner inject map authored under a verbose
// `uses` entry into a *types.InjectBlock. Three known mechanism keys are
// recognised — `env`, `file`, `arg` — matching the three standard inject
// mechanisms; unknown mechanism keys at this layer are silently dropped
// (the loop only reads the three known keys; nothing iterates over
// other entries).
//
// Typing-degradation contract. The function reads each mechanism's value
// as `any` and stores it directly into the matching field of
// *types.InjectBlock — which is itself typed as Env / File / Arg
// `any`. **No type-assertion happens here.** The downstream
// IR builder's convertInjectBlock is the place where the any
// values are unpacked: it accepts both `map[string]any` and
// `map[string]string` shapes, copying only string-valued entries and
// silently dropping non-string values.
//
// Topology's role is therefore one of structure-preserving forwarding:
// it confirms the outer `inject:` block is itself a map (returning
// ErrInvalidUsesFormat otherwise) but does not introspect the inner
// per-mechanism payloads. Any non-string-leaf coercion is the IR side's
// concern.
func parseInjectBlock(raw any) (*types.InjectBlock, error) {
	om, ok := raw.(*types.OrderedMap[any])
	if !ok {
		return nil, cerrors.Wrap(ErrInvalidUsesFormat, cerrors.DeveloperError, cerrors.SourceLocation{}, fmt.Sprintf("inject must be a map, got %T", raw))
	}

	inject := &types.InjectBlock{}
	if v, ok := om.Get("env"); ok {
		inject.Env = v
	}
	if v, ok := om.Get("file"); ok {
		inject.File = v
	}
	if v, ok := om.Get("arg"); ok {
		inject.Arg = v
	}
	return inject, nil
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"regexp"
	"sort"

	cerrors "github.com/cognitio/aggrega-cli/pkg/errors"
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// forbiddenSelfInjectRefs matches template actions referencing cross-node data.
var forbiddenSelfInjectRefs = regexp.MustCompile(`\{\{[^}]*\.context\.(inject|uses)[^}]*\}\}`)

// SelfInjectContext is the restricted template context for self-inject value: evaluation.
type SelfInjectContext struct {
	Values   types.OrderedMap[any]   `json:"values"`
	Infra    types.OrderedMap[any]   `json:"infra"`
	Context  SelfInjectContextBlock  `json:"context"`
	Metadata *types.TemplateMetadata `json:"metadata,omitempty"`
}

// SelfInjectContextBlock holds the restricted context fields for self-inject templates.
type SelfInjectContextBlock struct {
	InstanceID  string                   `json:"instance_id"`
	Labels      types.OrderedMap[string] `json:"labels,omitempty,omitzero"`
	Annotations *types.AnnotationBuckets `json:"annotations,omitempty"`
}

// ExecutePass2a resolves component-level self-inject for one node.
// Called before Pass 2b (uses-inject resolution).
//
// For each mechanism in SelfInject:
//   - Compact form (string values) → treated as literals
//   - Verbose form with from: → resolved from own provides
//   - Verbose form with value: → evaluated as Go template with restricted context
func ExecutePass2a(
	inject *types.InjectPipe,
	selfInject *types.InjectBlock,
	nodeID string,
	ownProvides types.OrderedMap[string],
	siCtx SelfInjectContext,
) (map[string]map[string]string, error) {
	if selfInject == nil {
		return nil, nil
	}

	// Track which keys were set by self-inject for collision detection
	selfKeys := map[string]map[string]string{} // mechanism → key → value

	// Process each mechanism
	if err := processSelfInjectMechanism(&inject.Env, "env", selfInject.Env, ownProvides, siCtx, selfKeys); err != nil {
		return nil, cerrors.Wrap(err, cerrors.DeveloperError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("node %q self-inject", nodeID))
	}
	if err := processSelfInjectMechanism(&inject.File, "file", selfInject.File, ownProvides, siCtx, selfKeys); err != nil {
		return nil, cerrors.Wrap(err, cerrors.DeveloperError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("node %q self-inject", nodeID))
	}
	if err := processSelfInjectMechanism(&inject.Arg, "arg", selfInject.Arg, ownProvides, siCtx, selfKeys); err != nil {
		return nil, cerrors.Wrap(err, cerrors.DeveloperError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("node %q self-inject", nodeID))
	}

	// Rebuild .all
	inject.All = buildInjectAll(*inject)

	return selfKeys, nil
}

// CheckSelfInjectCollisions detects self-inject vs uses-inject delivery key
// collisions visible at the static authoring layer: selfKeys (component-level
// inject author keys) compared against uses-inject keys derived from the
// node's outgoing edges in the IR graph. Halt-always policy: presence in both
// sources is sufficient signal regardless of value equality —
// presence in both sources is sufficient signal regardless of value equality;
// authors resolve by removing one source.
//
// A second detection layer is anticipated for template-expanded keys
// (delivery keys produced by self-inject value: template evaluation that may
// collide with uses-inject hydrated keys at render time). That layer is not
// yet wired; this function's scope is the static authoring layer only.
//
// FR-interception (FeatureRecipe injectMapping overwriting a developer-
// declared key) is explicitly NOT a collision (interception by design,
// governed by the 5-Tier Onion Model's last-wins semantics) — that path is
// structurally below this function (Phase A overlay merge happens in
// merge.go::DeepMergeInjectStructured, which this function does not touch).
// The collision check is invoked by the renderer orchestrator after Pass 2b
// (RehydrateInjectFromProvides) and before Phase A; Phase A's overwrites are
// not observable from this function's inputs by construction.
//
// Returns the first collision wrapped via cerrors.Wrap with DeveloperError
// category. Multiple collisions on the same node sort by (mechanism,
// deliveryKey, usesSocket) for deterministic emission across runs of the
// same input.
func CheckSelfInjectCollisions(selfKeys map[string]map[string]string, usesEdges []types.IREdge, nodeID string) error {
	if len(selfKeys) == 0 {
		return nil
	}

	// Collect static uses-inject (mechanism, deliveryKey, socket) tuples
	// from edges sourced at this node.
	type usesEntry struct {
		socket string
	}
	usesByKey := map[string]map[string][]usesEntry{} // mechanism → deliveryKey → []usesEntry
	for _, edge := range usesEdges {
		if edge.Source != nodeID || edge.Inject.Len() == 0 {
			continue
		}
		for mech, mechMap := range edge.Inject.All() {
			if _, ok := usesByKey[mech]; !ok {
				usesByKey[mech] = map[string][]usesEntry{}
			}
			for key := range mechMap.All() {
				usesByKey[mech][key] = append(usesByKey[mech][key], usesEntry{socket: edge.Socket})
			}
		}
	}

	type collision struct {
		mechanism, deliveryKey, usesSocket string
	}
	var collisions []collision
	for mech, keys := range selfKeys {
		mechUses, ok := usesByKey[mech]
		if !ok {
			continue
		}
		for key := range keys {
			entries, present := mechUses[key]
			if !present {
				continue
			}
			for _, entry := range entries {
				collisions = append(collisions, collision{mechanism: mech, deliveryKey: key, usesSocket: entry.socket})
			}
		}
	}
	if len(collisions) == 0 {
		return nil
	}

	// Multi-level sort for deterministic emission across runs:
	// (mechanism, deliveryKey, usesSocket).
	sort.Slice(collisions, func(i, j int) bool {
		if collisions[i].mechanism != collisions[j].mechanism {
			return collisions[i].mechanism < collisions[j].mechanism
		}
		if collisions[i].deliveryKey != collisions[j].deliveryKey {
			return collisions[i].deliveryKey < collisions[j].deliveryKey
		}
		return collisions[i].usesSocket < collisions[j].usesSocket
	})

	first := collisions[0]
	return cerrors.Wrap(
		fmt.Errorf("%w: delivery key %q in mechanism %q declared by both component-level self-inject and uses-level cross-inject (uses socket %q)",
			ErrSelfInjectKeyCollision, first.deliveryKey, first.mechanism, first.usesSocket),
		cerrors.DeveloperError,
		cerrors.SourceLocation{File: nodeID},
		fmt.Sprintf("node %q Pass 2 self-inject vs uses-inject collision", nodeID),
	)
}

func processSelfInjectMechanism(
	dest *types.OrderedMap[string],
	mechName string,
	raw any,
	ownProvides types.OrderedMap[string],
	siCtx SelfInjectContext,
	selfKeys map[string]map[string]string,
) error {
	if raw == nil {
		return nil
	}

	entries, err := normalizeSelfInjectEntries(raw)
	if err != nil {
		return cerrors.Wrap(err, cerrors.DeveloperError, cerrors.SourceLocation{}, mechName)
	}

	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		entry := entries[key]
		resolved, err := resolveSelfInjectEntry(key, entry, mechName, ownProvides, siCtx)
		if err != nil {
			return err
		}
		dest.Set(key, resolved)

		if selfKeys[mechName] == nil {
			selfKeys[mechName] = map[string]string{}
		}
		selfKeys[mechName][key] = resolved
	}

	return nil
}

// selfInjectEntry represents one normalized self-inject entry.
type selfInjectEntry struct {
	isLiteral bool   // compact form: literal string
	value     string // value: template text or literal
	from      string // from: provides key reference
}

func normalizeSelfInjectEntries(raw any) (map[string]selfInjectEntry, error) {
	result := map[string]selfInjectEntry{}

	switch v := raw.(type) {
	case *types.OrderedMap[any]:
		// Post-round-trip-fidelity shape: nested mappings inside an
		// any-typed InjectBlock field decode as *types.OrderedMap[any]
		// when reached via an OrderedMap[any]-routed authoring path.
		// Iteration via .All() preserves author order so emitted
		// self-inject entries follow YAML authoring sequence.
		if v == nil {
			return result, nil
		}
		for key, val := range v.All() {
			switch vv := val.(type) {
			case string:
				result[key] = selfInjectEntry{isLiteral: true, value: vv}
			case *types.OrderedMap[any]:
				// Verbose form post-migration: {value: ...} or {from: ...}.
				if vv == nil {
					continue
				}
				entry := selfInjectEntry{}
				if valStr, ok := vv.Get("value"); ok {
					if s, ok := valStr.(string); ok {
						entry.value = s
					}
				}
				if fromStr, ok := vv.Get("from"); ok {
					if s, ok := fromStr.(string); ok {
						entry.from = s
					}
				}
				result[key] = entry
			case map[string]any:
				// Defensive — accepts the legacy map shape from in-memory
				// fixtures or pre-migration call paths.
				entry := selfInjectEntry{}
				if valStr, ok := vv["value"]; ok {
					if s, ok := valStr.(string); ok {
						entry.value = s
					}
				}
				if fromStr, ok := vv["from"]; ok {
					if s, ok := fromStr.(string); ok {
						entry.from = s
					}
				}
				result[key] = entry
			default:
				result[key] = selfInjectEntry{isLiteral: true, value: fmt.Sprintf("%v", val)}
			}
		}
	case map[string]any:
		for key, val := range v {
			switch vv := val.(type) {
			case string:
				// Compact form in component.inject = literal
				result[key] = selfInjectEntry{isLiteral: true, value: vv}
			case *types.OrderedMap[any]:
				// Mixed shape: outer is plain Go map, inner is *OrderedMap[any]
				// (e.g., test fixture authored as map[string]any{"key": orderedMap}).
				if vv == nil {
					continue
				}
				entry := selfInjectEntry{}
				if valStr, ok := vv.Get("value"); ok {
					if s, ok := valStr.(string); ok {
						entry.value = s
					}
				}
				if fromStr, ok := vv.Get("from"); ok {
					if s, ok := fromStr.(string); ok {
						entry.from = s
					}
				}
				result[key] = entry
			case map[string]any:
				// Verbose form: {value: ...} or {from: ...}
				entry := selfInjectEntry{}
				if valStr, ok := vv["value"]; ok {
					if s, ok := valStr.(string); ok {
						entry.value = s
					}
				}
				if fromStr, ok := vv["from"]; ok {
					if s, ok := fromStr.(string); ok {
						entry.from = s
					}
				}
				result[key] = entry
			default:
				result[key] = selfInjectEntry{isLiteral: true, value: fmt.Sprintf("%v", val)}
			}
		}
	case map[string]string:
		for key, val := range v {
			result[key] = selfInjectEntry{isLiteral: true, value: val}
		}
	case []any:
		// Schema-verbose-form: array of {in, from?, value?} per inject_delivery
		// definition. The `in:` field carries the inject key (parallel to the
		// per-key iteration key in the map-based arms above); `value:` and
		// `from:` carry the same semantics as the verbose-shape extraction
		// in the *OrderedMap[any] and map[string]any arms above. Schema
		// validation enforces mutual-exclusivity of value: and from: per
		// entry; the IR-build-time validator at ir.ValidateInjectBlocks is
		// the second-line-of-defense halt that catches schema-bypass paths
		// before any consumer site (including this one) runs. If both
		// fields somehow reach decode here, both populate the entry — the
		// rendering-time fallthrough is direct-construction unit tests
		// only; downstream resolveSelfInjectEntry's if/else cascade picks
		// from: over value: in that scenario per the
		// reference-before-template ordering of its function body.
		for _, entry := range v {
			in, value, from, hasValue, hasFrom, ok := types.DecodeVerboseInjectEntry(entry)
			if !ok {
				continue
			}
			outEntry := selfInjectEntry{}
			if hasValue {
				outEntry.value = value
			}
			if hasFrom {
				outEntry.from = from
			}
			result[in] = outEntry
		}
	}

	return result, nil
}

func resolveSelfInjectEntry(
	key string,
	entry selfInjectEntry,
	mechName string,
	ownProvides types.OrderedMap[string],
	siCtx SelfInjectContext,
) (string, error) {
	// Compact literal
	if entry.isLiteral {
		return entry.value, nil
	}

	// from: → resolve from own provides
	if entry.from != "" {
		val, ok := ownProvides.Get(entry.from)
		if !ok {
			return "", cerrors.New(cerrors.DeveloperError, cerrors.SourceLocation{}, fmt.Sprintf("%s[%q]: from key %q not found in component provides", mechName, key, entry.from), nil, nil)
		}
		return val, nil
	}

	// value: → evaluate as Go template with restricted context
	if entry.value != "" {
		// Guard: check for forbidden references
		if forbiddenSelfInjectRefs.MatchString("{{" + entry.value + "}}") {
			// Check the actual template text
		}
		if forbiddenSelfInjectRefs.MatchString(entry.value) {
			return "", cerrors.Wrap(ErrSelfInjectForbiddenReference, cerrors.DeveloperError, cerrors.SourceLocation{},
				fmt.Sprintf("%s[%q] value: template references .context.inject or .context.uses", mechName, key))
		}

		resolved, err := EvalTemplate(
			fmt.Sprintf("self-inject.%s[%s]", mechName, key),
			entry.value,
			siCtx,
		)
		if err != nil {
			return "", cerrors.Wrap(err, cerrors.DeveloperError, cerrors.SourceLocation{}, fmt.Sprintf("%s[%q] value: template", mechName, key))
		}
		return resolved, nil
	}

	return "", nil
}

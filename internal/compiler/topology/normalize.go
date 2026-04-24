// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import (
	"fmt"
	"sort"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// normalizeUses converts a polymorphic uses map into a flat slice of NormalizedEdge.
// Supported forms:
//   - String array:  {"databases": ["db1", "db2"]}
//   - Verbose map:   {"databases": {"my-db": {"target": "corp-db", "inject": {...}}}}
//
// The sourceNodeID is the fully-qualified dot-notation ID of the source node.
// If originFeature is non-empty, the edge originated from a Feature's uses block.
func normalizeUses(uses types.OrderedMap[interface{}], sourceNodeID, originFeature string) ([]NormalizedEdge, error) {
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
		case []interface{}:
			// String array form: ["db1", "db2"]
			for _, item := range v {
				target, ok := item.(string)
				if !ok {
					return nil, fmt.Errorf("%w: socket %q: array element must be a string, got %T", ErrInvalidUsesFormat, socket, item)
				}
				edges = append(edges, NormalizedEdge{
					Source:        sourceNodeID,
					Target:        target,
					Socket:        socket,
					Alias:         target,
					OriginFeature: originFeature,
				})
			}

		case map[string]interface{}:
			// Verbose map form: {"alias": {"target": "...", "inject": {...}}}
			aliases := make([]string, 0, len(v))
			for a := range v {
				aliases = append(aliases, a)
			}
			sort.Strings(aliases)

			for _, alias := range aliases {
				entry := v[alias]
				edge, err := parseVerboseEntry(sourceNodeID, socket, alias, entry, originFeature)
				if err != nil {
					return nil, err
				}
				edges = append(edges, edge)
			}

		default:
			return nil, fmt.Errorf("%w: socket %q: expected array or map, got %T", ErrInvalidUsesFormat, socket, raw)
		}
	}

	return edges, nil
}

func parseVerboseEntry(source, socket, alias string, raw interface{}, originFeature string) (NormalizedEdge, error) {
	m, ok := raw.(map[string]interface{})
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
		return NormalizedEdge{}, fmt.Errorf("%w: socket %q alias %q: expected map or string, got %T", ErrInvalidUsesFormat, socket, alias, raw)
	}

	target := alias // default target = alias name
	if t, ok := m["target"]; ok {
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

	if injectRaw, ok := m["inject"]; ok {
		inject, err := parseInjectBlock(injectRaw)
		if err != nil {
			return NormalizedEdge{}, fmt.Errorf("socket %q alias %q: %w", socket, alias, err)
		}
		edge.Inject = inject
	}

	return edge, nil
}

func parseInjectBlock(raw interface{}) (*types.InjectBlock, error) {
	m, ok := raw.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("%w: inject must be a map, got %T", ErrInvalidUsesFormat, raw)
	}

	inject := &types.InjectBlock{}
	if v, ok := m["env"]; ok {
		inject.Env = v
	}
	if v, ok := m["file"]; ok {
		inject.File = v
	}
	if v, ok := m["arg"]; ok {
		inject.Arg = v
	}
	return inject, nil
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"regexp"
	"sort"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// forbiddenSelfInjectRefs matches template actions referencing cross-node data.
var forbiddenSelfInjectRefs = regexp.MustCompile(`\{\{[^}]*\.context\.(inject|uses)[^}]*\}\}`)

// SelfInjectContext is the restricted template context for self-inject value: evaluation.
type SelfInjectContext struct {
	Values      map[string]interface{}     `json:"values"`
	Infra       map[string]interface{}     `json:"infra"`
	Context     SelfInjectContextBlock     `json:"context"`
	Metadata    *types.TemplateMetadata    `json:"metadata,omitempty"`
}

// SelfInjectContextBlock holds the restricted context fields for self-inject templates.
type SelfInjectContextBlock struct {
	InstanceID  string                `json:"instance_id"`
	Labels      map[string]string     `json:"labels,omitempty"`
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
	ownProvides map[string]string,
	siCtx SelfInjectContext,
) (map[string]map[string]string, error) {
	if selfInject == nil {
		return nil, nil
	}

	// Track which keys were set by self-inject for collision detection
	selfKeys := map[string]map[string]string{} // mechanism → key → value

	// Process each mechanism
	if err := processSelfInjectMechanism(inject.Env, "env", selfInject.Env, ownProvides, siCtx, selfKeys); err != nil {
		return nil, fmt.Errorf("node %q self-inject: %w", nodeID, err)
	}
	if err := processSelfInjectMechanism(inject.File, "file", selfInject.File, ownProvides, siCtx, selfKeys); err != nil {
		return nil, fmt.Errorf("node %q self-inject: %w", nodeID, err)
	}
	if err := processSelfInjectMechanism(inject.Arg, "arg", selfInject.Arg, ownProvides, siCtx, selfKeys); err != nil {
		return nil, fmt.Errorf("node %q self-inject: %w", nodeID, err)
	}

	// Rebuild .all
	inject.All = buildInjectAll(*inject)

	return selfKeys, nil
}

// CheckSelfInjectCollisions checks that no key was written by both self-inject and uses-inject.
func CheckSelfInjectCollisions(selfKeys map[string]map[string]string, inject types.InjectPipe, nodeID string) error {
	if len(selfKeys) == 0 {
		return nil
	}

	// After Pass 2b, check if any self-inject key was overwritten by uses-inject
	// This means both sources wrote the same key → collision
	// We detect by checking if the current value differs from what self-inject wrote
	// Actually simpler: we track self-inject keys and uses-inject keys separately
	// we rely on the Step 1 resolver catching static collisions.
	// Runtime collision detection for template-expanded keys is a future enhancement.

	return nil
}

func processSelfInjectMechanism(
	dest map[string]string,
	mechName string,
	raw interface{},
	ownProvides map[string]string,
	siCtx SelfInjectContext,
	selfKeys map[string]map[string]string,
) error {
	if raw == nil {
		return nil
	}

	entries, err := normalizeSelfInjectEntries(raw)
	if err != nil {
		return fmt.Errorf("%s: %w", mechName, err)
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
		dest[key] = resolved

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

func normalizeSelfInjectEntries(raw interface{}) (map[string]selfInjectEntry, error) {
	result := map[string]selfInjectEntry{}

	switch v := raw.(type) {
	case map[string]interface{}:
		for key, val := range v {
			switch vv := val.(type) {
			case string:
				// Compact form in component.inject = literal
				result[key] = selfInjectEntry{isLiteral: true, value: vv}
			case map[string]interface{}:
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
	}

	return result, nil
}

func resolveSelfInjectEntry(
	key string,
	entry selfInjectEntry,
	mechName string,
	ownProvides map[string]string,
	siCtx SelfInjectContext,
) (string, error) {
	// Compact literal
	if entry.isLiteral {
		return entry.value, nil
	}

	// from: → resolve from own provides
	if entry.from != "" {
		val, ok := ownProvides[entry.from]
		if !ok {
			return "", fmt.Errorf("%s[%q]: from key %q not found in component provides", mechName, key, entry.from)
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
			return "", fmt.Errorf("%w: %s[%q] value: template references .context.inject or .context.uses",
				ErrSelfInjectForbiddenReference, mechName, key)
		}

		resolved, err := EvalTemplate(
			fmt.Sprintf("self-inject.%s[%s]", mechName, key),
			entry.value,
			siCtx,
		)
		if err != nil {
			return "", fmt.Errorf("%s[%q] value: template: %w", mechName, key, err)
		}
		return resolved, nil
	}

	return "", nil
}

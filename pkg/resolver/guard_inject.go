// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"gopkg.in/yaml.v3"
)

// guardInjectKeys validates every developer-authored `from:` reference inside
// an `inject:` block against the declared `provides` keys of the Component the
// reference resolves against. Two reference surfaces are checked:
//
//   - Component-level self-inject (`spec.components[c].inject`): a verbose
//     `from:` reference resolves against the host Component's own declared
//     `provides`. Compact-form string values are literals and verbose `value:`
//     entries are templates; neither is a provides-key reference, so both are
//     skipped.
//   - Uses-level cross-inject (`spec.components[c].uses[socket][alias].inject`):
//     every delivery value is a provides-key reference resolved against the
//     used target Component's declared `provides`. The target is the entry's
//     `target` (defaulting to the alias key); its instance type determines the
//     Component whose `provides` keys are the legal set.
//
// Custom mechanisms (beyond env / file / arg) flow through both surfaces
// uniformly via the inject block's Additional map.
//
// A reference whose key is absent from the resolved target's declared
// `provides` produces a ResolverError wrapping ErrInvalidInjectKey, naming the
// offending key, the target Component's catalog type URI, and the sorted list
// of declared `provides` keys so the developer sees the legal options. A
// Component declaring no `provides` rejects every `from:` reference targeting
// it.
//
// Errors sort by (Entity, Field) for byte-identical output across runs of the
// same input. Returns nil when agg is nil or catalog.Component is nil — the
// catalog-less paths skip the check rather than erroring. Instances whose type
// is not in the catalog are skipped; unknown-type reporting belongs to other
// guards.
func guardInjectKeys(agg *types.Aggregate, catalog CatalogLookup) []*ResolverError {
	if agg == nil || catalog.Component == nil {
		return nil
	}

	entity := fmt.Sprintf("Aggregate %s", agg.Metadata.Name)
	instances := collectComponentInstances(agg)

	// Instance name → declared type, for resolving uses targets to a Component.
	typeByInstance := make(map[string]string, len(instances))
	for _, p := range instances {
		typeByInstance[p.name] = p.inst.Type
	}

	// Memoise provides-key extraction per Component type. The bool reports
	// whether the type resolved in the catalog; absent types are skipped.
	providesCache := map[string]map[string]bool{}
	resolvedType := map[string]bool{}
	providesFor := func(compType string) (map[string]bool, bool) {
		if keys, seen := providesCache[compType]; seen {
			return keys, resolvedType[compType]
		}
		catComp, found := catalog.Component(compType)
		if !found {
			providesCache[compType] = nil
			resolvedType[compType] = false
			return nil, false
		}
		keys := componentProvidesKeys(catComp, catalog.SchemaLibraries)
		providesCache[compType] = keys
		resolvedType[compType] = true
		return keys, true
	}

	var errs []*ResolverError

	for _, p := range instances {
		compName, comp := p.name, p.inst

		// Self-inject `from:` references resolve against the host's own provides.
		if comp.Inject != nil {
			ownKeys, ok := providesFor(comp.Type)
			if ok {
				for _, ref := range selfInjectFromRefs(comp.Inject) {
					if !ownKeys[ref.providesKey] {
						errs = append(errs, &ResolverError{
							Entity:  entity,
							Field:   fmt.Sprintf(".spec.components[%q].inject.%s[%q]", compName, ref.mechanism, ref.deliveryKey),
							Message: invalidInjectKeyMessage(ref.providesKey, comp.Type, ownKeys),
							Err:     ErrInvalidInjectKey,
						})
					}
				}
			}
		}

		// Uses-level cross-inject references resolve against the used target's provides.
		for socket, sockVal := range comp.Uses.All() {
			for alias, entry := range sockVal.Verbose.All() {
				if entry.Inject == nil {
					continue
				}
				target := entry.Target
				if target == "" {
					target = alias
				}
				targetType, known := typeByInstance[target]
				if !known {
					continue
				}
				targetKeys, ok := providesFor(targetType)
				if !ok {
					continue
				}
				for _, ref := range usesInjectFromRefs(entry.Inject) {
					if !targetKeys[ref.providesKey] {
						errs = append(errs, &ResolverError{
							Entity:  entity,
							Field:   fmt.Sprintf(".spec.components[%q].uses[%q][%q].inject.%s[%q]", compName, socket, alias, ref.mechanism, ref.deliveryKey),
							Message: invalidInjectKeyMessage(ref.providesKey, targetType, targetKeys),
							Err:     ErrInvalidInjectKey,
						})
					}
				}
			}
		}
	}

	sortResolverErrors(errs)
	return errs
}

// injectFromRef is one provides-key reference extracted from an inject block:
// the mechanism it was declared under, the local delivery key, and the
// referenced provides key.
type injectFromRef struct {
	mechanism   string
	deliveryKey string
	providesKey string
}

// selfInjectFromRefs returns the provides-key references in a component-level
// self-inject block. Only verbose `from:` entries are references; compact
// string literals and verbose `value:` templates are not. Custom mechanisms
// flow through the same extraction as env / file / arg.
func selfInjectFromRefs(inject *types.InjectBlock) []injectFromRef {
	var refs []injectFromRef
	collect := func(mech string, raw any) {
		for _, e := range decodeFromEntries(raw, true) {
			refs = append(refs, injectFromRef{mechanism: mech, deliveryKey: e.deliveryKey, providesKey: e.providesKey})
		}
	}
	collect("env", inject.Env)
	collect("file", inject.File)
	collect("arg", inject.Arg)
	for mech, raw := range inject.Additional.All() {
		collect(mech, raw)
	}
	return refs
}

// usesInjectFromRefs returns the provides-key references in a uses-level inject
// block. Every delivery value is a provides-key reference: compact map values
// and verbose `from:` entries alike. Custom mechanisms flow through the same
// extraction as env / file / arg.
func usesInjectFromRefs(inject *types.InjectBlock) []injectFromRef {
	var refs []injectFromRef
	collect := func(mech string, raw any) {
		for _, e := range decodeFromEntries(raw, false) {
			refs = append(refs, injectFromRef{mechanism: mech, deliveryKey: e.deliveryKey, providesKey: e.providesKey})
		}
	}
	collect("env", inject.Env)
	collect("file", inject.File)
	collect("arg", inject.Arg)
	for mech, raw := range inject.Additional.All() {
		collect(mech, raw)
	}
	return refs
}

// fromEntry pairs a delivery key with the provides key it references.
type fromEntry struct {
	deliveryKey string
	providesKey string
}

// decodeFromEntries extracts (delivery-key, provides-key) pairs from one
// inject mechanism's authored value. The selfMode flag distinguishes the two
// reference surfaces:
//
//   - selfMode true (component-level self-inject): a compact string value is a
//     literal, not a provides-key reference, and is skipped. Only verbose
//     `from:` entries yield a reference.
//   - selfMode false (uses-level cross-inject): every compact string value is a
//     provides-key reference, and verbose `from:` entries yield references too.
//
// Verbose `value:` entries (templates) are never provides-key references in
// either mode. The mechanism value may arrive as a compact map
// (map[string]any / map[string]string), a verbose array, or the ordered-map
// round-trip shape; each is handled. Iteration order is not significant —
// the caller's errors are sorted at the resolver boundary.
func decodeFromEntries(raw any, selfMode bool) []fromEntry {
	var out []fromEntry
	if raw == nil {
		return out
	}
	switch v := raw.(type) {
	case map[string]any:
		for k, val := range v {
			switch vv := val.(type) {
			case string:
				// Compact string: a literal in self-inject, a provides-key
				// reference in uses-inject.
				if !selfMode {
					out = append(out, fromEntry{deliveryKey: k, providesKey: vv})
				}
			case map[string]any:
				// Verbose form: {from: K} is a provides-key reference in both modes.
				if fromRaw, ok := vv["from"]; ok {
					if s, ok := fromRaw.(string); ok && s != "" {
						out = append(out, fromEntry{deliveryKey: k, providesKey: s})
					}
				}
			case *types.OrderedMap[any]:
				if vv == nil {
					continue
				}
				if fromRaw, ok := vv.Get("from"); ok {
					if s, ok := fromRaw.(string); ok && s != "" {
						out = append(out, fromEntry{deliveryKey: k, providesKey: s})
					}
				}
			}
		}
	case map[string]string:
		for k, s := range v {
			if !selfMode {
				out = append(out, fromEntry{deliveryKey: k, providesKey: s})
			}
		}
	case *types.OrderedMap[any]:
		if v == nil {
			return out
		}
		for k, val := range v.All() {
			switch vv := val.(type) {
			case string:
				if !selfMode {
					out = append(out, fromEntry{deliveryKey: k, providesKey: vv})
				}
			case *types.OrderedMap[any]:
				if vv == nil {
					continue
				}
				if fromRaw, ok := vv.Get("from"); ok {
					if s, ok := fromRaw.(string); ok && s != "" {
						out = append(out, fromEntry{deliveryKey: k, providesKey: s})
					}
				}
			case map[string]any:
				if fromRaw, ok := vv["from"]; ok {
					if s, ok := fromRaw.(string); ok && s != "" {
						out = append(out, fromEntry{deliveryKey: k, providesKey: s})
					}
				}
			}
		}
	case types.OrderedMap[any]:
		return decodeFromEntries(&v, selfMode)
	case []any:
		for _, entry := range v {
			in, _, from, _, hasFrom, ok := types.DecodeVerboseInjectEntry(entry)
			if !ok || !hasFrom || from == "" {
				continue
			}
			out = append(out, fromEntry{deliveryKey: in, providesKey: from})
		}
	}
	return out
}

// invalidInjectKeyMessage formats the actionable diagnostic for an inject
// `from:` reference to a key the target Component does not declare. The legal
// keys are listed in sorted order; an empty `provides` is named explicitly so
// the developer understands the Component exposes no public API.
func invalidInjectKeyMessage(key, targetType string, declared map[string]bool) string {
	if len(declared) == 0 {
		return fmt.Sprintf("inject from-key %q is not a provides key of component type %q, which declares no provides keys", key, targetType)
	}
	keys := make([]string, 0, len(declared))
	for k := range declared {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return fmt.Sprintf("inject from-key %q is not declared in component type %q provides; declared keys are: %v", key, targetType, keys)
}

// componentProvidesKeys returns the set of declared `provides` keys for a
// catalog Component. The keys are the property names of the `provides` pillar
// in the Component's spec.schema, compiled to JSON Schema 2020-12 via
// pkg/compactform (the same compile path the provider-base and pillar
// validators use). A Component with no spec.schema, no `provides` pillar, or a
// `provides` pillar that compiles to no properties yields an empty set.
//
// The schema is read as-authored on the resolved Component; cross-document
// $ref expansion uses the supplied SchemaLibraries map when present.
func componentProvidesKeys(comp types.Component, libraries map[string]types.SchemaLibrary) map[string]bool {
	keys := map[string]bool{}
	node := &comp.Spec.Schema
	if node.Kind == 0 {
		return keys
	}
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		node = node.Content[0]
	}
	if node.Kind != yaml.MappingNode {
		return keys
	}

	var providesNode *yaml.Node
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == "provides" {
			providesNode = node.Content[i+1]
			break
		}
	}
	if providesNode == nil {
		return keys
	}

	opts := []compactform.CompileOption{}
	if libraries != nil {
		opts = append(opts, compactform.WithSchemaLibraries(libraries))
	}
	compiled, err := compactform.Compile(providesNode, opts...)
	if err != nil {
		// A provides pillar that fails to compile is reported by the catalog
		// pillar validator at load; the guard treats it as exposing no keys
		// rather than emitting a second diagnostic for the same fault.
		return keys
	}
	props, ok := compiled["properties"].(compactform.Schema)
	if !ok {
		return keys
	}
	for k := range props {
		keys[k] = true
	}
	return keys
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armcore

import (
	"fmt"
	"strings"
)

// StampProvenance records a provenance set on each resource a fragment deploys,
// under the key ProvenanceMetadataKey of the resource's own metadata object, and
// returns an advisory when it could not stamp everything. fragment is the
// fragment's sections keyed by section name, as a template holds them.
//
// The set merges into the metadata the author wrote on the resource: the
// author's keys stay and only ProvenanceMetadataKey is written. A resource
// declared existing: true is not deployed by the template, so it is not stamped. A
// resource whose metadata is not an object, or whose body is not an object, is
// left as written and the advisory names it by symbolic name and JSON pointer. A
// fragment that declares no resource it deploys is left as it is with an advisory
// saying so. An empty set stamps nothing and returns no advisory. The template's
// own metadata is not touched, so fragments stamped one by one assemble without
// colliding.
func StampProvenance(fragment map[string]any, set map[string]string) (advisory string) {
	if len(set) == 0 {
		return ""
	}
	resources, _ := fragment[sectionResources].(map[string]any)
	var stamped, unstamped []string
	for _, symbol := range sortedKeys(resources) {
		body, ok := resources[symbol].(map[string]any)
		if ok && isExisting(body) {
			continue
		}
		pointer := "/" + sectionResources + "/" + escapePointer(symbol)
		if !ok {
			unstamped = append(unstamped, fmt.Sprintf("%s (at JSON pointer %s, not an object)", symbol, pointer))
			continue
		}
		out, ok := withProvenance(body, set)
		if !ok {
			unstamped = append(unstamped, fmt.Sprintf("%s (at JSON pointer %s/%s, not an object)", symbol, pointer, sectionMetadata))
			continue
		}
		resources[symbol] = out
		stamped = append(stamped, symbol)
	}
	switch {
	case len(unstamped) > 0:
		return "skipped provenance injection for " + strings.Join(unstamped, ", ") + ": a resource carries provenance in its metadata object, and these are emitted exactly as authored; write each as an object, with metadata an object, for it to carry provenance"
	case len(stamped) == 0:
		return "skipped provenance injection: the fragment declares no resource the template deploys, so the provenance has no resource to describe; the resources this component contributes elsewhere carry the full set"
	}
	return ""
}

// withProvenance returns a copy of one resource body whose metadata object
// carries set under ProvenanceMetadataKey, keeping every key the author wrote in
// it. It reports false, and returns no body, when the resource's metadata is
// present and not an object.
func withProvenance(body map[string]any, set map[string]string) (map[string]any, bool) {
	metadata := map[string]any{}
	if authored, present := body[sectionMetadata]; present && authored != nil {
		m, isObject := authored.(map[string]any)
		if !isObject {
			return nil, false
		}
		metadata = clone(m)
	}
	block := make(map[string]any, len(set))
	for _, k := range sortedKeys(set) {
		block[k] = set[k]
	}
	metadata[ProvenanceMetadataKey] = block
	out := clone(body)
	out[sectionMetadata] = metadata
	return out, true
}

// WriteFragment writes a fragment as the JSON document ReadFragment reads back
// to the same sections: the $schema of its scope, then each carried section it
// holds, with the functions regrouped as a list of namespaces sorted by name.
// Keys are sorted and the document ends in one newline.
func WriteFragment(f Fragment) ([]byte, error) {
	doc := map[string]any{sectionSchema: schemaOf(f.Scope)}
	for _, section := range sortedKeys(f.Sections) {
		entries := f.Sections[section]
		if section != sectionFunctions {
			doc[section] = entries
			continue
		}
		byNamespace := map[string]map[string]any{}
		for _, full := range sortedKeys(entries) {
			src := f.Functions[full]
			if byNamespace[src.Namespace] == nil {
				byNamespace[src.Namespace] = map[string]any{}
			}
			byNamespace[src.Namespace][src.Member] = entries[full]
		}
		list := make([]any, 0, len(byNamespace))
		for _, namespace := range sortedKeys(byNamespace) {
			list = append(list, map[string]any{"namespace": namespace, "members": byNamespace[namespace]})
		}
		doc[section] = list
	}
	return encodeTemplate(doc)
}

// schemaOf returns the $schema of a scope; a scope it does not know is a
// resource group's.
func schemaOf(s Scope) string {
	switch s {
	case ScopeSubscription:
		return SchemaSubscription
	case ScopeManagementGroup:
		return SchemaManagementGroup
	}
	return SchemaResourceGroup
}

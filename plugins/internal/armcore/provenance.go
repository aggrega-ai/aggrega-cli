// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armcore

import "fmt"

// StampProvenance records a provenance set in the template-level metadata of a
// fragment, under ProvenanceMetadataKey, and returns an advisory when it could
// not. fragment is the fragment's sections keyed by section name, as a template
// holds them.
//
// The set merges into the metadata the author wrote: the author's keys stay, and
// only ProvenanceMetadataKey is written. A resource declared existing: true is
// not deployed by the template, so it is not a site for provenance; a fragment
// that declares no other resource is left as it is with an advisory saying so. A
// metadata value that is not an object is left as written and the advisory names
// it. An empty set stamps nothing and returns no advisory.
func StampProvenance(fragment map[string]any, set map[string]string) (advisory string) {
	if len(set) == 0 {
		return ""
	}
	if !declaresDeployedResource(fragment[sectionResources]) {
		return "skipped provenance injection: the fragment declares no resource the template deploys, so the provenance has no deployment to describe; the resources this component contributes elsewhere carry the full set"
	}
	metadata := map[string]any{}
	if authored, present := fragment[sectionMetadata]; present && authored != nil {
		m, ok := authored.(map[string]any)
		if !ok {
			return fmt.Sprintf("skipped provenance injection: the fragment's %s is not an object, so it is emitted exactly as authored; write %s as an object for it to carry provenance", sectionMetadata, sectionMetadata)
		}
		metadata = clone(m)
	}
	block := make(map[string]any, len(set))
	for _, k := range sortedKeys(set) {
		block[k] = set[k]
	}
	metadata[ProvenanceMetadataKey] = block
	fragment[sectionMetadata] = metadata
	return ""
}

// declaresDeployedResource reports whether a resources section holds a resource
// the template deploys, that is one not declared existing: true.
func declaresDeployedResource(resources any) bool {
	m, _ := resources.(map[string]any)
	for _, name := range sortedKeys(m) {
		if !isExisting(m[name]) {
			return true
		}
	}
	return false
}

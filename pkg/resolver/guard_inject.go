// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"log"

	"github.com/cognitio/aggrega-cli/pkg/types"
)

// guardInjectKeys is the transitional stub during the JSON Schema
// migration. The inject-key validation depended on extracting
// `provides:` keys from a CUE schema source; the validator pipeline
// no longer processes CUE. When a target catalog entity carries
// Spec.CueSchema, this guard logs a deprecation note and returns
// nil (skip) for that target's inject-key validation.
//
// When the catalog migrates, the schema-authoring phase populates a
// JSON Schema 2020-12 payload that carries the `provides:` shape
// explicitly; a future integration extracts the keys from the new
// payload and restores the inject-key validation surface.
func guardInjectKeys(agg *types.Aggregate, catalog CatalogLookup) []*ResolverError {
	if agg == nil || catalog.Component == nil {
		return nil
	}

	logged := map[string]bool{}
	for _, comp := range agg.Spec.Components.All() {
		target, ok := catalog.Component(comp.Type)
		if !ok {
			continue
		}
		hasInject := false
		for _, feat := range comp.Features.All() {
			if feat.Inject != nil {
				hasInject = true
				break
			}
		}
		if !hasInject {
			for _, sec := range comp.Secrets.All() {
				if sec.Inject != nil {
					hasInject = true
					break
				}
			}
		}
		if !hasInject {
			continue
		}
		if target.Spec.CueSchema != "" && !logged[target.Metadata.Name] {
			log.Printf("warning: target catalog entity %q authors Spec.CueSchema; inject-key validation skipped pending JSON Schema migration",
				target.Metadata.Name)
			logged[target.Metadata.Name] = true
		}
	}
	return nil
}

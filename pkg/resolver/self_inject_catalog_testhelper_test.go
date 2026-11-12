// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// selfInjectCatalog holds the component types the self-delivery fixtures in this
// package author against: two permitting delivery through a named set of
// mechanisms, and one permitting none at all.
func selfInjectCatalog() CatalogLookup {
	return CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			switch typeURI {
			case "core/web-server": // permits env and file
				return types.Component{
					Metadata: types.ComponentMetadata{Name: "core/web-server"},
					Spec:     types.ComponentSpec{Inject: []string{"env", "file"}},
				}, true
			case "core/api-with-ssm": // permits env and a mechanism it names itself
				return types.Component{
					Metadata: types.ComponentMetadata{Name: "core/api-with-ssm"},
					Spec:     types.ComponentSpec{Inject: []string{"env", "ssm_param"}},
				}, true
			case "core/datastore/postgres": // declares no injection allowlist
				return types.Component{
					Metadata: types.ComponentMetadata{Name: "core/datastore/postgres"},
					Spec:     types.ComponentSpec{},
				}, true
			default:
				return types.Component{}, false
			}
		},
		Feature: func(string) (types.Feature, bool) { return types.Feature{}, false },
	}
}

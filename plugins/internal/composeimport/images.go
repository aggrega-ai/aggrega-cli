// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package composeimport

// The image table is generated from the Compose Recipes and Components of the
// aggrega-hub checkout beside this repository; a test in ./imagetable fails while
// the generated table differs from what a regeneration would write.
//go:generate go run ./imagetable -hub ../../../../aggrega-hub -out imagetable_gen.go

import (
	"strings"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// imageRow is one row of the image table: an image repository, the catalog
// Component a Compose Recipe of the hub that runs an image of that repository
// implements, every label of that Component in the hub but its category, and
// what the hub's Compose Recipes of that Component say of how a service running
// the image is configured, each read from the Recipes as they are written:
//
//   - Tag, the infra key the image's tag is written from;
//   - Values, each variable of the service's environment the Recipes write as
//     one value of the Component, mapped to that value's key;
//   - Secrets, each variable the Recipes read an attached secret's value under
//     where the attachment declares no key of its own, mapped to the slot the
//     secret is attached on;
//   - Provides, each value of the Component a provides key publishes whole,
//     mapped to that key, and Host, the provides key publishing the instance's
//     own name, which a peer on the network reaches it by.
type imageRow struct {
	Repository string
	Component  string
	Traits     map[string]string
	Tag        string
	Values     map[string]string
	Secrets    map[string]string
	Provides   map[string]string
	Host       string
}

// mappings is the image table read as the importer's mapping arm: each image
// repository maps to the Component its row names, under the category that
// Component's name places it in, with the row's traits — the Component's other
// labels in the hub — and with the last segment of its name as its vendor. A
// service of a mapped image therefore matches that Component, where the
// workspace's catalog carries it with the hub's labels, at the high tier. The
// mapping_overrides setting layers over it, keyed by repository, as it layers
// over every importer's arm.
func mappings() map[string]importer.Mapping {
	out := make(map[string]importer.Mapping, len(imageTable))
	for _, row := range imageTable {
		out[row.Repository] = importer.Mapping{
			Component: row.Component,
			Category:  categoryOf(row.Component),
			Traits:    row.Traits,
			Vendor:    row.Component[strings.LastIndexByte(row.Component, '/')+1:],
		}
	}
	return out
}

// categoryOf returns the category segment of a <namespace>/<category>/<name>
// Component name, empty for a name of another shape.
func categoryOf(name string) string {
	parts := strings.Split(name, "/")
	if len(parts) != 3 {
		return ""
	}
	return parts[1]
}

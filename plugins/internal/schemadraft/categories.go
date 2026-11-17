// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package schemadraft

import (
	"slices"
	"strings"
)

// Categories are the closed set of Component categories the catalog's
// conventions name: the second segment of a Component's name and the value of
// its component.aggrega.ai/category label.
var Categories = []string{"ai-agent", "ai-model", "ai-tool", "ai-pipeline", "datastore", "gateway", "mesh",
	"network", "messaging", "storage", "compute", "observability", "security", "bigdata", "devtool"}

// categoryOf returns the category the Component name names, its segment after
// the namespace, where the name has a segment after it and the category is one
// of Categories; "" otherwise.
func categoryOf(name string) string {
	segments := strings.Split(name, "/")
	if len(segments) < 3 || !slices.Contains(Categories, segments[1]) {
		return ""
	}
	return segments[1]
}

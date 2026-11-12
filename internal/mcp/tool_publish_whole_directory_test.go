// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"strings"
	"testing"
)

// TestPublishSummarySaysWhenTheWholeDirectoryTravels holds the tool's summary to
// the one case in which a publication carries more than the entity names: a local
// source written as a template. The summary names the source, and a publication
// that carried only what its entity declares says nothing about it.
func TestPublishSummarySaysWhenTheWholeDirectoryTravels(t *testing.T) {
	const source = "./charts/{{ .locals.flavour }}"
	for _, row := range []struct {
		name    string
		payload publishEntityPayload
		want    bool
	}{
		{"a templated local source", publishEntityPayload{DryRun: true, Kind: "Recipe", WholeDirectorySource: source}, true},
		{"a publication carrying what it declares", publishEntityPayload{DryRun: true, Kind: "Recipe"}, false},
		{"a completed publication of a templated source", publishEntityPayload{Kind: "Recipe", Digest: "sha256:x", WholeDirectorySource: source}, true},
	} {
		t.Run(row.name, func(t *testing.T) {
			text := renderPublishEntityText(row.payload)
			got := strings.Contains(text, "every file in the entity's directory")
			if got != row.want {
				t.Errorf("summary mentions the whole directory: %v; want %v:\n%s", got, row.want, text)
			}
			if row.want && !strings.Contains(text, source) {
				t.Errorf("the summary does not name the source that made the whole directory travel:\n%s", text)
			}
		})
	}
}

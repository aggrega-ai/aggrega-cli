// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// connectionUses is a uses map whose connections carry attachments: gpt and nim
// each carry the SAME declaring Feature under the SAME slot name, plain carries a
// Feature that declares nothing, and bare carries no attachment.
func connectionUses() types.OrderedMap[types.EnrichedEdge] {
	declaring := func() types.OrderedMap[types.TemplateAttachment] {
		m := types.OrderedMap[types.TemplateAttachment]{}
		m.Set("credential", types.TemplateAttachment{Type: "feature.test/db-credential"})
		return m
	}
	silent := types.OrderedMap[types.TemplateAttachment]{}
	silent.Set("credential", types.TemplateAttachment{Type: "feature.test/plain-config"})

	uses := types.OrderedMap[types.EnrichedEdge]{}
	uses.Set("gpt", types.EnrichedEdge{Target: "openai", Component: "core/model", Features: declaring()})
	uses.Set("nim", types.EnrichedEdge{Target: "nvidia", Component: "core/model", Features: declaring()})
	uses.Set("plain", types.EnrichedEdge{Target: "echo", Component: "core/model", Features: silent})
	uses.Set("bare", types.EnrichedEdge{Target: "store", Component: "core/uses-target"})
	return uses
}

// featureDeclarations stands in for the render's one Feature reader.
func featureDeclarations(featureType string) resolver.SensitiveLeaves {
	switch featureType {
	case "feature.test/db-credential":
		return resolver.SensitiveLeaves{
			Values:   map[string]bool{"literalPassword": true},
			Infra:    map[string]bool{"apiKey": true},
			Provides: map[string]bool{"password": true},
		}
	case "feature.test/plain-config":
		return resolver.SensitiveLeaves{Values: map[string]bool{}, Infra: map[string]bool{}, Provides: map[string]bool{}}
	}
	return resolver.SensitiveLeaves{}
}

// TestUsesWithheld_ReadsEachConnectionAttachmentsFeature covers the reading that
// turns the attachments written on each connection into the set the uses-map
// display withholds by. Each row names one alias-and-slot coordinate and whether
// the set must carry it.
func TestUsesWithheld_ReadsEachConnectionAttachmentsFeature(t *testing.T) {
	t.Parallel()
	got := usesWithheld(connectionUses(), func(componentType string) map[string]bool {
		if componentType == "core/uses-target" {
			return map[string]bool{"zzTargetSecret": true}
		}
		return nil
	}, featureDeclarations)

	cases := []struct {
		name    string
		alias   string
		present bool
	}{
		{"a connection carrying a declaring Feature", "gpt", true},
		{"a second connection carrying the same Feature under the same slot", "nim", true},
		{"a connection carrying a Feature that declares nothing", "plain", false},
		{"a connection carrying no attachment", "bare", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			declared, present := got.Attachments[c.alias]["credential"]
			if present != c.present {
				t.Fatalf("present=%v, want %v; got %v", present, c.present, got.Attachments)
			}
			if !present {
				if _, aliasPresent := got.Attachments[c.alias]; aliasPresent {
					t.Errorf("an alias with nothing declared must be omitted, not mapped empty; got %v", got.Attachments)
				}
				return
			}
			if !declared.Values["literalPassword"] || !declared.Infra["apiKey"] || !declared.Provides["password"] {
				t.Errorf("the set must carry what the Feature declares in every pillar; got %+v", declared)
			}
		})
	}

	// The provides half is read in the same walk and is unchanged by it: the edge
	// whose target declares a key carries it, the others do not.
	if !got.Provides["bare"]["zzTargetSecret"] {
		t.Errorf("the target's declared provides key must still be read; got %v", got.Provides)
	}
	if _, present := got.Provides["gpt"]; present {
		t.Errorf("an edge whose target declares nothing must contribute no provides marking; got %v", got.Provides)
	}

	// No Feature reader reads nothing about the connection view, and the provides
	// half still runs.
	noReader := usesWithheld(connectionUses(), func(string) map[string]bool { return map[string]bool{"zzTargetSecret": true} }, nil)
	if noReader.Attachments != nil {
		t.Errorf("without a Feature reader nothing is read about the attachments; got %v", noReader.Attachments)
	}
	if !noReader.Provides["gpt"]["zzTargetSecret"] {
		t.Errorf("the provides half must not depend on the Feature reader; got %v", noReader.Provides)
	}
}

// TestWorkloadReadsWithheld_ConnectionAttachments is the reference-reading matrix
// for the connection view: a workload template reaching a connection's declared
// datum is re-rendered with that datum substituted. Every withheld row sits beside
// a control of the same shape the set does not reach.
func TestWorkloadReadsWithheld_ConnectionAttachments(t *testing.T) {
	t.Parallel()
	keys := redact.NodeKeys(nil, nil, types.OrderedMap[types.IRFeatureEntry]{},
		redact.CrossNode{Attachments: map[string]map[string]redact.AttachmentKeys{
			"gpt": {"credential": {
				Values:   map[string]bool{"literalPassword": true, "tuning.adminToken": true},
				Infra:    map[string]bool{"apiKey": true},
				Provides: map[string]bool{"password": true},
			}},
		}},
		nil, nil, redact.Derived{})

	cases := []struct {
		name     string
		withheld string
		control  string
	}{
		{
			name:     "declared values leaf at full depth",
			withheld: `{{ .context.uses.gpt.features.credential.values.literalPassword }}`,
			control:  `{{ .context.uses.gpt.features.credential.values.secretName }}`,
		},
		{
			name:     "a nested declared leaf, and an object containing one",
			withheld: `{{ .context.uses.gpt.features.credential.values.tuning }}`,
			control:  `{{ .context.uses.gpt.features.credential.values.label }}`,
		},
		{
			name:     "declared infra leaf",
			withheld: `{{ .context.uses.gpt.features.credential.infra.apiKey }}`,
			control:  `{{ .context.uses.gpt.features.credential.infra.region }}`,
		},
		{
			name:     "declared provides key",
			withheld: `{{ .context.uses.gpt.features.credential.provides.password }}`,
			control:  `{{ .context.uses.gpt.features.credential.provides.name }}`,
		},
		{
			name:     "the whole attachment, one level short of a pillar",
			withheld: `{{ .context.uses.gpt.features.credential }}`,
			control:  `{{ .context.uses.gpt.features.credential.type }}`,
		},
		{
			name:     "the connection's whole features entry",
			withheld: `{{ range $slot, $a := .context.uses.gpt.features }}{{ $a }}{{ end }}`,
			control:  `{{ range $slot, $a := .context.uses.nim.features }}{{ $a }}{{ end }}`,
		},
		{
			name:     "the same coordinate on a connection nothing was declared for",
			withheld: `{{ .context.uses.gpt.features.credential.values.literalPassword }}`,
			control:  `{{ .context.uses.nim.features.credential.values.literalPassword }}`,
		},
		{
			name:     "an edge field no declaration governs",
			withheld: `{{ .context.uses.gpt.features.credential.values.literalPassword }}`,
			control:  `{{ .context.uses.gpt.values.timeout }}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if !workloadReadsWithheld(c.withheld, keys) {
				t.Errorf("must be withheld: %s", c.withheld)
			}
			if workloadReadsWithheld(c.control, keys) {
				t.Errorf("must NOT be withheld: %s", c.control)
			}
		})
	}

	// A read of the whole uses map reaches every connection, the one carrying a
	// declared attachment included — the conservative answer a dynamic alias lookup
	// takes.
	if !workloadReadsWithheld(`{{ range $alias, $e := .context.uses }}{{ $e }}{{ end }}`, keys) {
		t.Error("a read of the whole uses map must reach a connection's declared attachment")
	}
}

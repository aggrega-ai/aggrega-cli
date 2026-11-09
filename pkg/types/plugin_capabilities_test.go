// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types_test

import (
	"encoding/json"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// specBlock marshals entity through the named encoder, re-parses the document,
// and returns its spec object. Presence of a per-plugin-type block is read as a
// key of that object rather than as a byte count, so the assertion says what it
// means and does not move when an unrelated field is added.
func specBlock(t *testing.T, entity types.Plugin, encoding string) map[string]any {
	t.Helper()
	var (
		raw []byte
		doc map[string]any
		err error
	)
	switch encoding {
	case "json":
		if raw, err = json.Marshal(entity); err != nil {
			t.Fatalf("json.Marshal: %v", err)
		}
		err = json.Unmarshal(raw, &doc)
	case "yaml":
		if raw, err = yaml.Marshal(&entity); err != nil {
			t.Fatalf("yaml.Marshal: %v", err)
		}
		err = yaml.Unmarshal(raw, &doc)
	default:
		t.Fatalf("unknown encoding %q", encoding)
	}
	if err != nil {
		t.Fatalf("%s re-parse: %v\n%s", encoding, err, raw)
	}
	spec, ok := doc["spec"].(map[string]any)
	if !ok {
		t.Fatalf("%s output carries no spec object:\n%s", encoding, raw)
	}
	return spec
}

// providerEntity builds a pluginType: provider entity that authors no provider
// block, then records capabilities through the setter.
func providerEntity(capabilities []string) types.Plugin {
	var entity types.Plugin
	entity.APIVersion = "aggrega.ai/v1beta1"
	entity.Kind = "Plugin"
	entity.Metadata.Name = "p"
	entity.Metadata.Version = "1.0.0"
	entity.Metadata.PluginType = "provider"
	entity.Spec.AbiVersion = "v1alpha1"
	entity.Spec.SetCapabilitiesFor("provider", capabilities)
	return entity
}

// TestSetCapabilitiesForEmptyListLeavesTheProviderBlockAbsent holds the setter
// to its contract that an empty list against an absent block leaves that block
// absent from the serialized entity.
//
// The provider block is held by value, so "absent" is "zero" and only its
// omitzero tag keeps it out of the output. An allocated slice of length zero is
// a non-zero field, so writing one where the caller meant "no capabilities"
// would emit a provider block no author wrote — and only on the JSON side,
// since the YAML encoder treats an all-zero struct as empty under omitempty.
// Both spellings of an empty list are asserted because they are
// indistinguishable to a caller and must stay indistinguishable in the bytes.
func TestSetCapabilitiesForEmptyListLeavesTheProviderBlockAbsent(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		capabilities []string
		wantProvider bool
	}{
		{"nil slice", nil, false},
		{"allocated slice of length zero", []string{}, false},
		// Positive control: the assertion below must be capable of seeing a
		// provider block, or the two rows above prove nothing.
		{"populated list", []string{"helm"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			entity := providerEntity(tc.capabilities)
			for _, encoding := range []string{"json", "yaml"} {
				block, present := specBlock(t, entity, encoding)["provider"]
				if present != tc.wantProvider {
					t.Fatalf("%s: provider block present = %v, want %v (capabilities %#v)",
						encoding, present, tc.wantProvider, tc.capabilities)
				}
				if !tc.wantProvider {
					continue
				}
				held, ok := block.(map[string]any)
				if !ok {
					t.Fatalf("%s: provider block is %T, want an object", encoding, block)
				}
				if _, ok := held["capabilities"]; !ok {
					t.Fatalf("%s: provider block carries no capabilities: %#v", encoding, held)
				}
			}
		})
	}
}

// TestSetCapabilitiesForEmptyListKeepsAPresentBlock holds the setter to the
// other half of the same contract: an empty list against a block that is
// already present clears the list and leaves the block, so a machine-derived
// write overrides an authored claim rather than letting a stale one survive.
// The three pointer-held blocks are covered because normalising an empty list
// to nil must not turn this case into a deletion.
func TestSetCapabilitiesForEmptyListKeepsAPresentBlock(t *testing.T) {
	t.Parallel()
	blocks := []struct {
		pluginType string
		key        string
		author     func(*types.PluginSpec)
	}{
		{"pipeline-step", "pipelineStep", func(s *types.PluginSpec) {
			s.PipelineStep = &types.PluginPipelineStep{Capabilities: []string{"helm"}}
		}},
		{"parser", "parser", func(s *types.PluginSpec) {
			s.Parser = &types.PluginParser{Capabilities: []string{"parse:yaml"}}
		}},
		{"importer", "importer", func(s *types.PluginSpec) {
			s.Importer = &types.PluginImporter{Capabilities: []string{"import:terraform"}}
		}},
	}
	spellings := []struct {
		name         string
		capabilities []string
	}{
		{"nil slice", nil},
		{"allocated slice of length zero", []string{}},
	}
	for _, block := range blocks {
		for _, spelling := range spellings {
			t.Run(block.pluginType+"/"+spelling.name, func(t *testing.T) {
				t.Parallel()
				var entity types.Plugin
				entity.APIVersion = "aggrega.ai/v1beta1"
				entity.Kind = "Plugin"
				entity.Metadata.Name = "p"
				entity.Metadata.Version = "1.0.0"
				entity.Metadata.PluginType = block.pluginType
				entity.Spec.AbiVersion = "v1alpha1"
				block.author(&entity.Spec)

				entity.Spec.SetCapabilitiesFor(block.pluginType, spelling.capabilities)

				held, present := specBlock(t, entity, "json")[block.key]
				if !present {
					t.Fatalf("%s block was dropped by an empty write; it must be cleared and kept", block.key)
				}
				fields, ok := held.(map[string]any)
				if !ok {
					t.Fatalf("%s block is %T, want an object", block.key, held)
				}
				if _, stale := fields["capabilities"]; stale {
					t.Fatalf("%s block kept a stale capability list: %#v", block.key, fields)
				}
				if got := entity.DeclaredCapabilities(); len(got) != 0 {
					t.Fatalf("%s declares %#v after an empty write, want none", block.key, got)
				}
			})
		}
	}
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm_test

import (
	"encoding/json"
	"testing"

	schemas "github.com/aggrega-ai/aggrega-schemas"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
)

// The plugin ABI version is declared by the shipped schemas and enforced by the
// host, and those are different files. The host's own constant is what refuses a
// guest at load, so a host that drifts from the schemas rejects every guest built
// against what the schemas publish — the divergence lands on the enforcing side,
// where it is most expensive. These tests hold the two schema declarations and
// the host constant to one value.
//
// The per-language constants generated from the descriptor schema are
// deliberately NOT re-asserted here. They are generated from the same
// declaration these tests read, and their currency is already held by the codec
// regeneration check; restating it would put that answer in a second place,
// which is the failure this file exists to close rather than reproduce.

// descriptorSchemaABIVersion reads the ABI version the descriptor schema pins.
// The value is read out of the shipped schema rather than restated, because a
// restatement agrees on the day it is written and drifts afterwards.
func descriptorSchemaABIVersion(t *testing.T) string {
	t.Helper()
	var doc struct {
		Defs struct {
			PluginDescriptor struct {
				Properties struct {
					AbiVersion struct {
						Const string `json:"const"`
					} `json:"abi_version"`
				} `json:"properties"`
			} `json:"PluginDescriptor"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal([]byte(schemas.WasmDescribeV1alpha1), &doc); err != nil {
		t.Fatalf("unmarshal plugin-descriptor schema: %v", err)
	}
	got := doc.Defs.PluginDescriptor.Properties.AbiVersion.Const
	if got == "" {
		// A schema that stopped pinning the field, or a renamed position, would
		// otherwise leave the comparison below asserting nothing.
		t.Fatalf("the descriptor schema pins no abi_version value")
	}
	return got
}

// pluginEntitySchemaABIVersions reads the ABI versions a Plugin entity may
// declare. This surface is an enum rather than a single pinned value, because a
// future ABI revision adds an accepted value here before the host speaks two.
func pluginEntitySchemaABIVersions(t *testing.T) []string {
	t.Helper()
	var doc struct {
		Properties struct {
			Spec struct {
				Properties struct {
					AbiVersion struct {
						Enum []string `json:"enum"`
					} `json:"abiVersion"`
				} `json:"properties"`
			} `json:"spec"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(schemas.PluginV1alpha1), &doc); err != nil {
		t.Fatalf("unmarshal plugin-entity schema: %v", err)
	}
	got := doc.Properties.Spec.Properties.AbiVersion.Enum
	if len(got) == 0 {
		t.Fatalf("the plugin-entity schema declares no abiVersion value set")
	}
	return got
}

// TestABIVersion_HostConstantMatchesDescriptorSchema holds the version the host
// speaks to the version the descriptor schema pins.
//
// The descriptor schema is the primary authority: it describes the very document
// a guest returns from describe(), and the host's compatibility check reads
// abi_version out of that document. So a host constant that disagrees with this
// schema refuses exactly the guests the published contract told plugin authors to
// build. Bumping the ABI is a schema edit, a regeneration, and a hand edit to the
// host constant; this test is what fails when the third step is missed.
func TestABIVersion_HostConstantMatchesDescriptorSchema(t *testing.T) {
	declared := descriptorSchemaABIVersion(t)
	if wasm.ABIVersionV1Alpha1 != declared {
		t.Errorf("the host speaks abi_version %q; the descriptor schema pins %q. "+
			"A guest built against the published contract would be refused at load",
			wasm.ABIVersionV1Alpha1, declared)
	}
}

// TestABIVersion_PluginEntitySchemaAcceptsHostVersion holds the versions a Plugin
// entity may declare to the version the host speaks.
//
// This is the authoring side of the same contract: an operator writes
// spec.abiVersion on a Plugin entity, and the plugin it routes to is loaded by a
// host that accepts exactly one version. A value the entity schema accepts but
// the host does not is a Plugin that validates and then cannot load; a host
// version the entity schema rejects is a plugin nobody can author an entity for.
// Both directions are asserted.
func TestABIVersion_PluginEntitySchemaAcceptsHostVersion(t *testing.T) {
	accepted := pluginEntitySchemaABIVersions(t)

	var hostIsAccepted bool
	for _, v := range accepted {
		if v == wasm.ABIVersionV1Alpha1 {
			hostIsAccepted = true
			continue
		}
		t.Errorf("the plugin-entity schema accepts abiVersion %q, which the host does not speak "+
			"(the host speaks %q); an entity declaring it would validate and then fail to load",
			v, wasm.ABIVersionV1Alpha1)
	}
	if !hostIsAccepted {
		t.Errorf("the host speaks abiVersion %q, which the plugin-entity schema does not accept "+
			"(it accepts %v); no entity could be authored for a plugin the host can load",
			wasm.ABIVersionV1Alpha1, accepted)
	}
}

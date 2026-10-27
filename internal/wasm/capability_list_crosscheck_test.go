// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestValidatePlugin_CapabilityListNamesOnlyReportedCapabilities proves the
// publish-side validation of an entity's capability list against the plugin's
// describe(): a list equal to what the plugin reports passes; a narrowed list
// passes and draws the under-declaration notice; a list naming a capability the
// plugin does not report halts with ErrWasmContractMismatch naming that
// capability. The frp-step-go fixture is a pipeline step reporting helm and
// helm-chart.
func TestValidatePlugin_CapabilityListNamesOnlyReportedCapabilities(t *testing.T) {
	wasmBytes := loadFrpStepGoBytes(t)

	base, err := ValidatePlugin(context.Background(), wasmBytes, ValidateOptions{
		Identity:     "frp/patch:1.0.0",
		DeclaredType: string(PluginTypePipelineStep),
	})
	if err != nil {
		t.Fatalf("binary-only validate should pass; got %v", err)
	}
	reported := base.Descriptor.Capabilities
	if len(reported) != 2 || reported[0] != "helm" || reported[1] != "helm-chart" {
		t.Fatalf("the fixture reports capabilities %q; this test is written for [helm helm-chart]", reported)
	}
	describeVersion := ""
	if base.Descriptor.Metadata != nil {
		describeVersion = base.Descriptor.Metadata.Version
	}

	entity := func(capabilities ...string) *EntityInputs {
		return &EntityInputs{
			Version: describeVersion,
			Claim: EntityContractClaim{Exports: []EntityExportClaim{
				{Name: "step", Operates: operatesPatch, Scope: ScopeOwn},
			}},
			CuratedSurface: EntityCuratedSurface{
				ExportNames:  []string{"step"},
				Capabilities: capabilities,
			},
		}
	}
	validate := func(in *EntityInputs) (ValidateReport, error) {
		return ValidatePlugin(context.Background(), wasmBytes, ValidateOptions{
			Identity:     "frp/patch:1.0.0",
			DeclaredType: string(PluginTypePipelineStep),
			Entity:       in,
		})
	}

	report, err := validate(entity("helm", "helm-chart"))
	if err != nil {
		t.Fatalf("a list equal to the reported capabilities should pass; got %v", err)
	}
	if len(report.Notices) != 0 {
		t.Errorf("a list equal to the reported capabilities should draw no notice; got %q", report.Notices)
	}

	report, err = validate(entity("helm"))
	if err != nil {
		t.Fatalf("a narrowed list should pass; got %v", err)
	}
	if len(report.Notices) != 1 || !strings.Contains(report.Notices[0], `"helm-chart"`) {
		t.Errorf("a narrowed list should draw one notice naming helm-chart; got %q", report.Notices)
	}

	_, err = validate(entity("helm", "helm-chart", "compose"))
	if !errors.Is(err, ErrWasmContractMismatch()) {
		t.Fatalf("a list naming a capability the plugin does not report should halt with "+
			"ErrWasmContractMismatch; got %v", err)
	}
	for _, want := range []string{"frp/patch:1.0.0", `"compose"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal should name %s; got %v", want, err)
		}
	}
}

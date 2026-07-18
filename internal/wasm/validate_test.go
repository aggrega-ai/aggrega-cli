// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestValidatePlugin_TypeMismatch proves the declared-vs-reported plugin-type
// gate: a streaming CLI guest that self-reports plugin type "cli" validated with a
// non-cli DeclaredType halts with ErrWasmPluginTypeMismatch, and the message names
// the identity and both types.
func TestValidatePlugin_TypeMismatch(t *testing.T) {
	wasmBytes := buildCLIArgsExitGuest(t)
	_, err := ValidatePlugin(context.Background(), wasmBytes, ValidateOptions{
		Identity:     "frp/patch:1.0.0",
		DeclaredType: string(PluginTypeFRPStep),
	})
	if !errors.Is(err, ErrWasmPluginTypeMismatch) {
		t.Fatalf("declared frp_step over a cli guest should halt with ErrWasmPluginTypeMismatch; got %v", err)
	}
	for _, want := range []string{"frp/patch:1.0.0", string(PluginTypeFRPStep), string(PluginTypeCLI)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("type-mismatch halt should name %q; got %v", want, err)
		}
	}
}

// TestValidatePlugin_StreamingCLIPasses proves a streaming CLI guest validated
// with the matching DeclaredType passes in binary-only mode: the describe-only
// load admits the streaming import through the union allowlist, and the descriptor
// is returned.
func TestValidatePlugin_StreamingCLIPasses(t *testing.T) {
	wasmBytes := buildCLIArgsExitGuest(t)
	report, err := ValidatePlugin(context.Background(), wasmBytes, ValidateOptions{
		Identity:     "cli/args-exit:1.0.0",
		DeclaredType: string(PluginTypeCLI),
	})
	if err != nil {
		t.Fatalf("a streaming cli guest declared cli should pass validation; got %v", err)
	}
	if report.Descriptor.PluginType != string(PluginTypeCLI) {
		t.Fatalf("validated descriptor plugin_type = %q; want %q", report.Descriptor.PluginType, PluginTypeCLI)
	}
}

// TestValidatePlugin_EntityCrossChecks proves the entity-mode cross-checks run:
// an agreeing capability claim and host-function record pass, and a divergent
// host-function record halts with ErrWasmHostFunctionsStampMismatch. The
// frp-step-go fixture exports "step" (operates=patch, scope=own) and imports no
// custom host functions.
func TestValidatePlugin_EntityCrossChecks(t *testing.T) {
	wasmBytes := loadFrpStepGoBytes(t)

	ok := &EntityInputs{
		Claim: EntityContractClaim{Exports: []EntityExportClaim{
			{Name: "step", Operates: operatesPatch, Scope: ScopeOwn},
		}},
	}
	if _, err := ValidatePlugin(context.Background(), wasmBytes, ValidateOptions{
		Identity:     "frp/patch:1.0.0",
		DeclaredType: string(PluginTypeFRPStep),
		Entity:       ok,
	}); err != nil {
		t.Fatalf("an agreeing entity surface should pass validation; got %v", err)
	}

	stale := &EntityInputs{
		Claim:         EntityContractClaim{Exports: []EntityExportClaim{{Name: "step", Operates: operatesPatch, Scope: ScopeOwn}}},
		HostFunctions: []string{"aggrega_log"}, // the binary imports none
	}
	_, err := ValidatePlugin(context.Background(), wasmBytes, ValidateOptions{
		Identity:     "frp/patch:1.0.0",
		DeclaredType: string(PluginTypeFRPStep),
		Entity:       stale,
	})
	if !errors.Is(err, ErrWasmHostFunctionsStampMismatch) {
		t.Fatalf("a stale host-function record should halt with ErrWasmHostFunctionsStampMismatch; got %v", err)
	}
}

// TestNonCLIStreamingDescriptorRejectedAtDescribe pins that a non-cli binary that
// imports the CLI streaming emitters is refused at the describe-load itself, not by
// any later gate. A binary honestly declaring a streaming host function under a
// non-cli reported type is rejected by descriptor validation (the streaming
// emitters are illegal outside the cli profile); a binary that hides the import is
// rejected by the load's declared-vs-imported host-function cross-check. Either
// way, the describe-only load — the profile ValidatePlugin uses — is itself the
// import-legality gate for this class, so no separate strict reload of the bytes is
// needed to catch it.
func TestNonCLIStreamingDescriptorRejectedAtDescribe(t *testing.T) {
	// A well-formed frp_step descriptor whose "step" export declares the cli-only
	// streaming emitter — the honest self-report of a non-cli binary that imports
	// it. Descriptor validation checks each export's declared host functions
	// against the profile allowlist for the reported type, which for a non-cli type
	// admits neither streaming name.
	raw := []byte(`{"abi_version":"v1alpha1","plugin_type":"frp_step","exports":[{"name":"step","operates":"patch","scope":"own","host_functions":["aggrega_cli_write_stdout"]}]}`)
	_, err := parseAndValidateDescriptor(raw, "frp/patch:1.0.0")
	if !errors.Is(err, ErrWasmManifestRejected) {
		t.Fatalf("a non-cli descriptor declaring a streaming host function should be rejected at describe validation with ErrWasmManifestRejected; got %v", err)
	}
}

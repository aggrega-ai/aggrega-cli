// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

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
		DeclaredType: string(PluginTypePipelineStep),
	})
	if !errors.Is(err, ErrWasmPluginTypeMismatch()) {
		t.Fatalf("declared pipeline-step over a cli guest should halt with ErrWasmPluginTypeMismatch; got %v", err)
	}
	for _, want := range []string{"frp/patch:1.0.0", string(PluginTypePipelineStep), string(PluginTypeCLI)} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("type-mismatch halt should name %q; got %v", want, err)
		}
	}
}

// TestValidatePlugin_EmptyDeclaredTypeRejected proves the required-field
// precondition: validating with an empty DeclaredType halts up front rather than
// loading the binary, since an empty declared type would otherwise skip the
// describe-load's type verdict and admit unverified bytes. The halt names the
// identity.
func TestValidatePlugin_EmptyDeclaredTypeRejected(t *testing.T) {
	wasmBytes := buildCLIArgsExitGuest(t)
	_, err := ValidatePlugin(context.Background(), wasmBytes, ValidateOptions{
		Identity:     "cli/args-exit:1.0.0",
		DeclaredType: "",
	})
	if err == nil {
		t.Fatal("an empty DeclaredType should halt validation; got nil error")
	}
	if !strings.Contains(err.Error(), "cli/args-exit:1.0.0") {
		t.Errorf("the empty-declared-type halt should name the identity; got %v", err)
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

	// Discover the binary's describe() build-provenance version via a binary-only
	// validate, so the agreeing entity's recorded version tracks whatever the
	// fixture reports rather than hard-coding the build-provenance string.
	base, err := ValidatePlugin(context.Background(), wasmBytes, ValidateOptions{
		Identity:     "frp/patch:1.0.0",
		DeclaredType: string(PluginTypePipelineStep),
	})
	if err != nil {
		t.Fatalf("binary-only validate should pass; got %v", err)
	}
	describeVersion := ""
	if base.Descriptor.Metadata != nil {
		describeVersion = base.Descriptor.Metadata.Version
	}

	ok := &EntityInputs{
		Version: describeVersion,
		Claim: EntityContractClaim{Exports: []EntityExportClaim{
			{Name: "step", Operates: operatesPatch, Scope: ScopeOwn},
		}},
	}
	if _, err := ValidatePlugin(context.Background(), wasmBytes, ValidateOptions{
		Identity:     "frp/patch:1.0.0",
		DeclaredType: string(PluginTypePipelineStep),
		Entity:       ok,
	}); err != nil {
		t.Fatalf("an agreeing entity surface should pass validation; got %v", err)
	}

	stale := &EntityInputs{
		Claim:         EntityContractClaim{Exports: []EntityExportClaim{{Name: "step", Operates: operatesPatch, Scope: ScopeOwn}}},
		HostFunctions: []string{"aggrega_log"}, // the binary imports none
	}
	_, err = ValidatePlugin(context.Background(), wasmBytes, ValidateOptions{
		Identity:     "frp/patch:1.0.0",
		DeclaredType: string(PluginTypePipelineStep),
		Entity:       stale,
	})
	if !errors.Is(err, ErrWasmHostFunctionsStampMismatch()) {
		t.Fatalf("a stale host-function record should halt with ErrWasmHostFunctionsStampMismatch; got %v", err)
	}
}

// TestValidatePlugin_VersionCrossCheck proves the entity-mode version cross-check:
// a recorded metadata.version that diverges from the binary's describe()
// build-provenance version halts with ErrWasmPluginVersionMismatch, and a matching
// version passes. The describe() version is discovered from the binary itself so
// the test does not hard-code the fixture's build-provenance string; the mismatch
// case appends a divergent suffix to guarantee inequality.
func TestValidatePlugin_VersionCrossCheck(t *testing.T) {
	wasmBytes := loadFrpStepGoBytes(t)

	// Discover the binary's describe() build-provenance version via a binary-only
	// validate, so the match case tracks whatever the fixture reports.
	base, err := ValidatePlugin(context.Background(), wasmBytes, ValidateOptions{
		Identity:     "frp/patch:1.0.0",
		DeclaredType: string(PluginTypePipelineStep),
	})
	if err != nil {
		t.Fatalf("binary-only validate should pass; got %v", err)
	}
	describeVersion := ""
	if base.Descriptor.Metadata != nil {
		describeVersion = base.Descriptor.Metadata.Version
	}

	claim := EntityContractClaim{Exports: []EntityExportClaim{
		{Name: "step", Operates: operatesPatch, Scope: ScopeOwn},
	}}

	mismatched := &EntityInputs{Claim: claim, Version: describeVersion + "9.9.9-divergent"}
	_, err = ValidatePlugin(context.Background(), wasmBytes, ValidateOptions{
		Identity:     "frp/patch:1.0.0",
		DeclaredType: string(PluginTypePipelineStep),
		Entity:       mismatched,
	})
	if !errors.Is(err, ErrWasmPluginVersionMismatch()) {
		t.Fatalf("a divergent metadata.version should halt with ErrWasmPluginVersionMismatch; got %v", err)
	}
	for _, want := range []string{"frp/patch:1.0.0", describeVersion + "9.9.9-divergent"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("version-mismatch halt should name %q; got %v", want, err)
		}
	}

	matched := &EntityInputs{Claim: claim, Version: describeVersion}
	if _, err := ValidatePlugin(context.Background(), wasmBytes, ValidateOptions{
		Identity:     "frp/patch:1.0.0",
		DeclaredType: string(PluginTypePipelineStep),
		Entity:       matched,
	}); err != nil {
		t.Fatalf("a matching metadata.version should pass validation; got %v", err)
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
	// A well-formed pipeline-step descriptor whose "step" export declares the cli-only
	// streaming emitter — the honest self-report of a non-cli binary that imports
	// it. Descriptor validation checks each export's declared host functions
	// against the profile allowlist for the reported type, which for a non-cli type
	// admits neither streaming name.
	raw := []byte(`{"abi_version":"v1beta1","plugin_type":"pipeline-step","exports":[{"name":"step","operates":"patch","scope":"own","host_functions":["aggrega_cli_write_stdout"]}]}`)
	_, err := parseAndValidateDescriptor(raw, "frp/patch:1.0.0")
	if !errors.Is(err, ErrWasmManifestRejected()) {
		t.Fatalf("a non-cli descriptor declaring a streaming host function should be rejected at describe validation with ErrWasmManifestRejected; got %v", err)
	}
}

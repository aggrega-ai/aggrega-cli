// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"context"
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
)

func TestParseAndValidateDescriptor(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{
			name: "valid frp_step descriptor",
			raw:  `{"abi_version":"v1alpha1","plugin_type":"frp_step","exports":[{"name":"step"}]}`,
		},
		{
			name:    "malformed JSON",
			raw:     `{"abi_version":`,
			wantErr: true,
		},
		{
			name:    "unsupported ABI version",
			raw:     `{"abi_version":"v2","plugin_type":"frp_step","exports":[{"name":"step"}]}`,
			wantErr: true,
		},
		{
			name:    "frp_step missing minimum export",
			raw:     `{"abi_version":"v1alpha1","plugin_type":"frp_step","exports":[{"name":"render_node"}]}`,
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			desc, err := parseAndValidateDescriptor([]byte(tc.raw), "fixture")
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got descriptor %+v", desc)
				}
				if !errors.Is(err, ErrWasmManifestRejected) {
					t.Fatalf("error does not wrap ErrWasmManifestRejected: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if desc.PluginType != string(PluginTypeFRPStep) {
				t.Fatalf("plugin type: got %q want %q", desc.PluginType, PluginTypeFRPStep)
			}
		})
	}
}

// TestInvokeExportUndeclared confirms the gate refuses to invoke an
// export the descriptor did not declare, before the guest runs.
func TestInvokeExportUndeclared(t *testing.T) {
	t.Parallel()

	desc := PluginDescriptor{
		AbiVersion: ABIVersionV1Alpha1,
		PluginType: string(PluginTypeFRPStep),
		Exports:    []PluginExport{{Name: "step"}},
	}
	plugin := &Plugin{name: "fixture"}
	_, err := InvokeExport(context.Background(), plugin, desc, "render_node", nil)
	if err == nil {
		t.Fatal("expected error invoking undeclared export")
	}
	if !errors.Is(err, ErrWasmManifestRejected) {
		t.Fatalf("error does not wrap ErrWasmManifestRejected: %v", err)
	}
}

// TestLoadAndDescribeFixture exercises the live gate end-to-end against
// the frp-step Go fixture: load, describe(), validate, then dispatch the
// declared step export. Skips cleanly when the PDK source or the TinyGo
// toolchain is absent.
func TestLoadAndDescribeFixture(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")

	loader := &Loader{}
	ctx := context.Background()

	plugin, desc, err := loader.LoadAndDescribe(ctx, PluginSpec{
		Name: "frp-step-go",
		Path: pluginPath,
	})
	if err != nil {
		t.Fatalf("LoadAndDescribe: %v", err)
	}
	defer plugin.Close(ctx)

	if desc.PluginType != string(PluginTypeFRPStep) {
		t.Fatalf("plugin type: got %q want %q", desc.PluginType, PluginTypeFRPStep)
	}
	if !desc.hasExport(frpStepExportName) {
		t.Fatalf("descriptor does not declare the %q export", frpStepExportName)
	}

	out, err := InvokeExport(ctx, plugin, desc, frpStepExportName, []byte(`{}`))
	if err != nil {
		t.Fatalf("InvokeExport(step): %v", err)
	}
	if len(out) == 0 {
		t.Fatal("step returned empty output")
	}

	// An export the descriptor does not declare is refused even on a
	// live plugin.
	if _, err := InvokeExport(ctx, plugin, desc, "render_node", nil); !errors.Is(err, ErrWasmManifestRejected) {
		t.Fatalf("undeclared export on live plugin: got %v", err)
	}
}

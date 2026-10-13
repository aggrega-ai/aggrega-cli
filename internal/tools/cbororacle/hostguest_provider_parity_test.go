// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cbororacle_test

import (
	"bytes"
	"encoding/hex"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
)

// The provider render family carries the shared diagnostic in its replies and,
// inside an assembly's input, in each contributor's render. The provider guests
// shipped with the compiler read and write the compiler side's codec, so they
// agree with the host by construction; a provider built with a plugin kit writes
// and reads the plugin side's. These tests pin that the two speak one binary form
// for the diagnostic: each side reads what the other writes, and both write the
// same bytes for the same value.

// providerDiagnostics is a pair of diagnostics a provider might report, one of
// them carrying a code and a location, so the structural form is reached beyond
// its two required members. The compiler side's diagnostic is the plugin side's
// shared type under another name, so one value serves both sides' fixtures; what
// differs between the sides is how each one's codec carries it.
func providerDiagnostics() []renderctx.Diagnostic {
	return []renderctx.Diagnostic{
		{Severity: abi.SeverityHalt, Message: "main.tf:3: Unsupported block type", Code: "hcl-parse",
			Location: &renderctx.DiagnosticLocation{URI: "payments-api", Kind: "terraform", Line: 3}},
		{Severity: abi.SeverityWarn, Message: "values.legacyPort is deprecated", Code: "deprecated-field"},
	}
}

// guestToHost asserts the compiler side strictly decodes the plugin side's bytes
// and re-encodes them identically: the direction a kit-built provider's reply
// travels.
func guestToHost[H interface {
	DecodeCBORStrict([]byte) error
	EncodeCBOR() ([]byte, error)
}](t *testing.T, name string, guestBytes []byte, host H) {
	t.Helper()
	if err := host.DecodeCBORStrict(guestBytes); err != nil {
		t.Fatalf("%s: compiler-side strict decode of plugin-side bytes: %v", name, err)
	}
	back, err := host.EncodeCBOR()
	if err != nil {
		t.Fatalf("%s: compiler-side re-encode: %v", name, err)
	}
	if !bytes.Equal(guestBytes, back) {
		t.Errorf("%s: the two sides of the wire disagree.\nplugin side:   %s\ncompiler side: %s",
			name, hex.EncodeToString(guestBytes), hex.EncodeToString(back))
	}
}

func TestHostGuestWireParity_ProviderReplyDiagnostics(t *testing.T) {
	t.Parallel()

	guestDiags := providerDiagnostics

	t.Run("render_node", func(t *testing.T) {
		t.Parallel()
		host := rendercore.RenderNodeOutput{
			Files:       []rendercore.RenderedFile{{Path: "main.tf", Content: []byte("resource \"x\" \"y\" {}\n")}},
			Diagnostics: providerDiagnostics(),
		}
		hostBytes, err := host.EncodeCBOR()
		if err != nil {
			t.Fatalf("compiler-side encode: %v", err)
		}
		crossSide(t, "RenderNodeOutput", hostBytes, &abi.RenderNodeOutput{})
		guest := abi.RenderNodeOutput{
			Files:       []abi.RenderedFile{{Path: "main.tf", Content: []byte("resource \"x\" \"y\" {}\n")}},
			Diagnostics: guestDiags(),
		}
		guestBytes, err := guest.EncodeCBOR()
		if err != nil {
			t.Fatalf("plugin-side encode: %v", err)
		}
		guestToHost(t, "RenderNodeOutput", guestBytes, &rendercore.RenderNodeOutput{})
	})

	t.Run("validate_node_input", func(t *testing.T) {
		t.Parallel()
		hostBytes, err := rendercore.ValidateNodeOutput{Diagnostics: providerDiagnostics()}.EncodeCBOR()
		if err != nil {
			t.Fatalf("compiler-side encode: %v", err)
		}
		crossSide(t, "ValidateNodeOutput", hostBytes, &abi.ValidateNodeOutput{})
		guestBytes, err := abi.ValidateNodeOutput{Diagnostics: guestDiags()}.EncodeCBOR()
		if err != nil {
			t.Fatalf("plugin-side encode: %v", err)
		}
		guestToHost(t, "ValidateNodeOutput", guestBytes, &rendercore.ValidateNodeOutput{})
	})

	t.Run("assemble_boundary", func(t *testing.T) {
		t.Parallel()
		hostBytes, err := rendercore.AssembleBoundaryOutput{
			Files:       []rendercore.RenderedFile{{Path: "docker-compose.yaml", Content: []byte("services: {}\n")}},
			Diagnostics: providerDiagnostics(),
		}.EncodeCBOR()
		if err != nil {
			t.Fatalf("compiler-side encode: %v", err)
		}
		crossSide(t, "AssembleBoundaryOutput", hostBytes, &abi.AssembleBoundaryOutput{})
		guestBytes, err := abi.AssembleBoundaryOutput{
			Files:       []abi.RenderedFile{{Path: "docker-compose.yaml", Content: []byte("services: {}\n")}},
			Diagnostics: guestDiags(),
		}.EncodeCBOR()
		if err != nil {
			t.Fatalf("plugin-side encode: %v", err)
		}
		guestToHost(t, "AssembleBoundaryOutput", guestBytes, &rendercore.AssembleBoundaryOutput{})
	})
}

// TestHostGuestWireParity_AssemblyInputCarriesContributorDiagnostics covers the
// other direction: the host hands an assembler each contributor's render, the
// diagnostics included, so a kit-built assembler must be able to read them.
func TestHostGuestWireParity_AssemblyInputCarriesContributorDiagnostics(t *testing.T) {
	t.Parallel()

	in := rendercore.AssembleBoundaryInput{
		RenderedNodes: []rendercore.RenderedNode{{
			ID:   "payments-api",
			Type: "core/web-service",
			Rendered: rendercore.RenderNodeOutput{
				Files:       []rendercore.RenderedFile{{Path: "service.yaml", Content: []byte("kind: Service\n")}},
				Diagnostics: providerDiagnostics(),
			},
		}},
		BoundaryPath: "dist/prod",
	}
	hostBytes, err := in.EncodeCBOR()
	if err != nil {
		t.Fatalf("compiler-side encode: %v", err)
	}
	var guest abi.AssembleBoundaryInput
	if err := guest.DecodeCBOR(hostBytes); err != nil {
		t.Fatalf("plugin-side decode of compiler-side bytes: %v", err)
	}
	got := guest.RenderedNodes[0].Rendered.Diagnostics
	if len(got) != 2 || got[0].Message != "main.tf:3: Unsupported block type" || got[0].Location == nil || got[0].Location.Line != 3 {
		t.Errorf("the plugin side did not read the contributor's diagnostics the compiler side wrote: %+v", got)
	}
}

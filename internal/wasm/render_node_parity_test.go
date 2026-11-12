// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package wasm_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// renderNodeParityAttachments is the fixture's attachment author order, with
// the sensitivity marking each entry carries. The order is deliberately NOT
// alphabetical and carries three entries: a two-entry or alphabetically-ordered
// set could not distinguish a guest that preserves the wire order from one that
// re-sorts the map, so the assertion over it would be vacuous. The markings are
// mixed so an echo of them proves per-entry reading rather than a constant.
var renderNodeParityAttachments = []struct {
	alias     string
	sensitive bool
}{
	{"zeta-cache", false},
	{"alpha-db", true},
	{"mid-queue", true},
}

// renderNodeParityInput builds the single fixed RenderNodeInput both
// per-language render fixtures render, encoded as the render socket carries it:
// canonical CBOR, written by the host's generated encoder. The node id and
// prefix are ASCII so the guests' UTF-8 encoding of the emitted file content is
// unambiguous and identical across languages. The node carries attachments so
// the fixtures can report what each language's generated codec does with an
// author-ordered map and whether it reads an attachment's sensitivity marking.
func renderNodeParityInput(t *testing.T) []byte {
	t.Helper()
	features := types.OrderedMap[types.IRFeatureEntry]{}
	for _, a := range renderNodeParityAttachments {
		features.Set(a.alias, types.IRFeatureEntry{
			Type:            "core/" + a.alias,
			ResolvedVersion: "1.0.0",
			Sensitive:       a.sensitive,
		})
	}
	in := renderer.RenderNodeInput{
		Node: renderer.IRNodeProjection{
			ID:             "parity-node-01",
			Type:           "core/web-service",
			ResolvedRecipe: "core/recipe/parity:1.0.0",
			Features:       features,
			Provenance: renderer.NodeProvenance{
				CompilerVersion: "test",
				AggregateName:   "parity",
				Environment:     "prod",
			},
		},
		Context: renderctx.RenderContext{},
	}
	b, err := in.EncodeCBOR()
	if err != nil {
		t.Fatalf("encode RenderNodeInput: %v", err)
	}
	return b
}

// TestRenderNodeInput_HostEmitsAttachmentsInAuthorOrder pins the promise the
// socket contract makes about the payload itself: the host emits the node's
// attachments in the order the author declared them, not sorted, so identical
// input yields identical bytes. It reads the encoded payload rather than the Go
// value, because the byte order on the wire is the whole claim — and it is the
// premise every guest-side order finding below is measured against. Each alias
// first appears as its entry's key, ahead of the entry's own bytes, so the first
// occurrences are the entries' order.
func TestRenderNodeInput_HostEmitsAttachmentsInAuthorOrder(t *testing.T) {
	t.Parallel()
	payload := string(renderNodeParityInput(t))

	prev := -1
	for _, a := range renderNodeParityAttachments {
		at := strings.Index(payload, a.alias)
		if at < 0 {
			t.Fatalf("alias %q absent from the encoded payload", a.alias)
		}
		if at <= prev {
			t.Errorf("alias %q appears at byte %d, not after the preceding alias at byte %d: the payload is not in author order", a.alias, at, prev)
		}
		prev = at
	}

	// The marking has to survive the encode too, or a guest has nothing to read.
	// Each entry crosses as the bytes of its own JSON document, so the marking is
	// read there.
	if !strings.Contains(payload, `"sensitive":true`) {
		t.Errorf("no attachment carries sensitive:true on the wire; payload = %x", payload)
	}
}

// TestRenderNodeInput_GoGuestCodecDecodesAttachments measures the third
// language's realization of the same author-ordered attachment map. The schema
// marks the map order-preserving, so the Go guest codec decodes it into the
// binding's order-keeping container: a Go guest reads the attachment set, every
// entry's content — including the sensitivity marking — and the author order the
// host wrote, which the assertion reads as a sequence.
func TestRenderNodeInput_GoGuestCodecDecodesAttachments(t *testing.T) {
	t.Parallel()
	var decoded abi.RenderNodeInput
	if err := decoded.DecodeCBOR(renderNodeParityInput(t)); err != nil {
		t.Fatalf("Go guest codec rejected the host payload: %v", err)
	}
	if got, want := decoded.Node.Features.Len(), len(renderNodeParityAttachments); got != want {
		t.Fatalf("Go guest decoded %d attachment(s), want %d", got, want)
	}
	gotOrder := decoded.Node.Features.Keys()
	for i, a := range renderNodeParityAttachments {
		if i < len(gotOrder) && gotOrder[i] != a.alias {
			t.Errorf("Go guest decoded the attachments in the order %v, not the author's", gotOrder)
			break
		}
	}
	for _, a := range renderNodeParityAttachments {
		raw, ok := decoded.Node.Features.Get(a.alias)
		if !ok {
			t.Errorf("Go guest decoded no attachment %q", a.alias)
			continue
		}
		var entry abi.IRFeatureEntry
		if err := entry.UnmarshalJSON(raw); err != nil {
			t.Errorf("Go guest could not read attachment %q through the IR feature-entry shape: %v", a.alias, err)
			continue
		}
		if entry.Sensitive != a.sensitive {
			t.Errorf("attachment %q decoded sensitive=%v, want %v", a.alias, entry.Sensitive, a.sensitive)
		}
	}
}

// renderNodeFixtureOutput loads the named render fixture, exercises its
// describe() self-report through the host descriptor validator, invokes
// render_node with the shared input, and returns the output decoded the way the
// host decodes it: strictly, from the binary encoding.
func renderNodeFixtureOutput(t *testing.T, fixtureName string, input []byte) renderer.RenderNodeOutput {
	t.Helper()
	plugin, cleanup := loadFixturePlugin(t, fixtureName)
	defer cleanup()

	descBytes, err := plugin.Call(context.Background(), "describe", nil)
	if err != nil {
		t.Fatalf("plugin.Call describe (%s): %v", fixtureName, err)
	}
	var desc wasm.PluginDescriptor
	if err := json.Unmarshal(descBytes, &desc); err != nil {
		t.Fatalf("unmarshal PluginDescriptor (%s): %v (bytes: %s)", fixtureName, err, descBytes)
	}
	if err := desc.Validate(fixtureName); err != nil {
		t.Fatalf("descriptor Validate (%s): %v", fixtureName, err)
	}

	outBytes, err := plugin.Call(context.Background(), "render_node", input)
	if err != nil {
		t.Fatalf("plugin.Call render_node (%s): %v", fixtureName, err)
	}
	var out renderer.RenderNodeOutput
	if err := out.DecodeCBORStrict(outBytes); err != nil {
		t.Fatalf("decode RenderNodeOutput (%s): %v (bytes: %x)", fixtureName, err, outBytes)
	}
	return out
}

// fileByPath returns the named rendered file's content, failing the test when
// the guest emitted no file at that path.
func fileByPath(t *testing.T, name string, out renderer.RenderNodeOutput, path string) string {
	t.Helper()
	for _, f := range out.Files {
		if f.Path == path {
			return string(f.Content)
		}
	}
	t.Fatalf("%s: no rendered file at %q (emitted %d file(s))", name, path, len(out.Files))
	return ""
}

// attachmentEchoAuthorOrder is the attachment echo a guest emits when it
// observes the wire's own key order. Each entry is alias=sensitive, so the echo
// reports the marking as well as the order.
func attachmentEchoAuthorOrder() string {
	parts := make([]string, 0, len(renderNodeParityAttachments))
	for _, a := range renderNodeParityAttachments {
		parts = append(parts, echoEntry(a.alias, a.sensitive))
	}
	return strings.Join(parts, ",")
}

func echoEntry(alias string, sensitive bool) string {
	if sensitive {
		return alias + "=sensitive"
	}
	return alias + "=plain"
}

// TestRenderSocketParity_RustTypeScript proves the Rust and TypeScript PDK
// render sockets produce equivalent output for one fixed RenderNodeInput.
// Each fixture is loaded through the real loader, its descriptor is
// validated, and its render_node output is decoded through the host's
// generated RenderNodeOutput codec (so the comparison is over decoded
// structure, not raw guest bytes). The node-id echo must be structurally
// equal across the two languages: cross-language render-socket parity.
//
// The attachment echo is asserted per language as well as compared across them.
// The host emits the node's attachments in author order (pinned separately,
// above), and the schema marks the attachment map order-preserving, so each
// language's generated codec decodes it into that language's order-keeping
// container: Rust's binding container and TypeScript's Map each keep the order the
// wire carried. A guest therefore reads the same attachment SET, the same
// per-attachment values and sensitivity markings, and the same author ORDER in
// every language. Pinning each realization here keeps that measured rather than
// surveyed once.
func TestRenderSocketParity_RustTypeScript(t *testing.T) {
	input := renderNodeParityInput(t)

	rustOut := renderNodeFixtureOutput(t, "render-node-rust.wasm", input)
	tsOut := renderNodeFixtureOutput(t, "render-node-typescript.wasm", input)

	// Pin the per-fixture contract first: two files at fixed paths, one echoing
	// the node id and one echoing the attachments the guest observed. This makes
	// a divergence report which side broke the contract before the
	// cross-language comparison.
	for _, tc := range []struct {
		name          string
		out           renderer.RenderNodeOutput
		expectedEcho  string
		echoRationale string
	}{
		{"rust", rustOut, attachmentEchoAuthorOrder(), "the generated Rust codec decodes the attachment map into its order-keeping container"},
		{"typescript", tsOut, attachmentEchoAuthorOrder(), "the generated TypeScript codec reads the attachment map in member order into a Map"},
	} {
		if len(tc.out.Files) != 2 {
			t.Fatalf("%s: expected exactly two rendered files; got %d", tc.name, len(tc.out.Files))
		}
		if got := fileByPath(t, tc.name, tc.out, "parity.txt"); got != "rendered-node:parity-node-01" {
			t.Errorf("%s: unexpected node-id echo %q", tc.name, got)
		}
		if got := fileByPath(t, tc.name, tc.out, "attachments.txt"); got != tc.expectedEcho {
			t.Errorf("%s: attachment echo = %q, want %q (%s)", tc.name, got, tc.expectedEcho, tc.echoRationale)
		}
	}

	rustNodeEcho := fileByPath(t, "rust", rustOut, "parity.txt")
	tsNodeEcho := fileByPath(t, "typescript", tsOut, "parity.txt")
	if !reflect.DeepEqual(rustNodeEcho, tsNodeEcho) {
		t.Fatalf("render-socket parity mismatch between Rust and TypeScript:\n rust = %q\n ts   = %q", rustNodeEcho, tsNodeEcho)
	}

	// The render facts each fixture reports are compared byte for byte: a fact's
	// value crosses as the JSON its guest wrote, so two guests writing the same
	// value produce the same bytes whatever their language's map container does —
	// which is what lets a provider fix the order of a value's keys itself. Both fixtures list their facts out of name order,
	// report every value as a list, and write the echoed node's keys out of key
	// order, so a decode that sorted either would fail here.
	wantFacts := []struct{ name, value string }{
		{"emitted_paths", `["parity.txt","attachments.txt"]`},
		{"echoed_node", `[{"prefix":"rendered-node:","id":"parity-node-01"}]`},
	}
	for _, tc := range []struct {
		name string
		out  renderer.RenderNodeOutput
	}{{"rust", rustOut}, {"typescript", tsOut}} {
		if len(tc.out.RenderFacts) != len(wantFacts) {
			t.Fatalf("%s: reported %d render facts, want %d", tc.name, len(tc.out.RenderFacts), len(wantFacts))
		}
		for i, want := range wantFacts {
			got := tc.out.RenderFacts[i]
			if got.Name != want.name || string(got.Value) != want.value {
				t.Errorf("%s: render fact %d = %s=%s, want %s=%s", tc.name, i, got.Name, got.Value, want.name, want.value)
			}
		}
	}
}

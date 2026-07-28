// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm_test

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
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
// per-language render fixtures render. The node id and prefix are ASCII so
// the guests' UTF-8 encoding of the emitted file content is unambiguous and
// identical across languages. The node carries attachments so the fixtures can
// report what each language's generated codec does with an author-ordered map
// and whether it reads an attachment's sensitivity marking.
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
	b, err := in.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal RenderNodeInput: %v", err)
	}
	return b
}

// TestRenderNodeInput_HostEmitsAttachmentsInAuthorOrder pins the promise the
// socket contract makes about the payload itself: the host emits the node's
// attachments in the order the author declared them, not sorted, so identical
// input yields identical bytes. It reads the marshalled payload rather than the
// Go value, because the byte order on the wire is the whole claim — and it is the
// premise every guest-side order finding below is measured against.
func TestRenderNodeInput_HostEmitsAttachmentsInAuthorOrder(t *testing.T) {
	t.Parallel()
	payload := string(renderNodeParityInput(t))

	prev := -1
	for _, a := range renderNodeParityAttachments {
		at := strings.Index(payload, `"`+a.alias+`"`)
		if at < 0 {
			t.Fatalf("alias %q absent from the marshalled payload", a.alias)
		}
		if at <= prev {
			t.Errorf("alias %q appears at byte %d, not after the preceding alias at byte %d: the payload is not in author order", a.alias, at, prev)
		}
		prev = at
	}

	// The marking has to survive the marshal too, or a guest has nothing to read.
	if !strings.Contains(payload, `"sensitive":true`) {
		t.Errorf("no attachment carries sensitive:true on the wire; payload = %s", payload)
	}
}

// TestRenderNodeInput_GoGuestCodecDecodesAttachments measures the third
// language's realization of the same author-ordered attachment map. The Go guest
// codec decodes it into a plain Go map, so a Go guest reads the attachment set
// and every entry's content — including the sensitivity marking — but has no
// author order to read at all: Go randomizes map iteration by design, which is a
// stronger loss than the Rust container's re-sorting rather than a milder one.
// The assertion is therefore over the key SET and the decoded marking, never over
// a sequence, because an order assertion here would be the flaky kind of test
// that passes by luck.
func TestRenderNodeInput_GoGuestCodecDecodesAttachments(t *testing.T) {
	t.Parallel()
	var decoded abi.RenderNodeInput
	if err := decoded.UnmarshalJSON(renderNodeParityInput(t)); err != nil {
		t.Fatalf("Go guest codec rejected the host payload: %v", err)
	}
	if got, want := len(decoded.Node.Features), len(renderNodeParityAttachments); got != want {
		t.Fatalf("Go guest decoded %d attachment(s), want %d", got, want)
	}
	for _, a := range renderNodeParityAttachments {
		raw, ok := decoded.Node.Features[a.alias]
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
// render_node with the shared input, and returns the decoded output.
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
	if err := out.UnmarshalJSON(outBytes); err != nil {
		t.Fatalf("unmarshal RenderNodeOutput (%s): %v (bytes: %s)", fixtureName, err, outBytes)
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
// observes the wire's own key order; attachmentEchoSortedOrder is the echo a
// guest emits when its decode container re-sorts the keys. Each entry is
// alias=sensitive, so the echo reports the marking as well as the order.
func attachmentEchoAuthorOrder() string {
	parts := make([]string, 0, len(renderNodeParityAttachments))
	for _, a := range renderNodeParityAttachments {
		parts = append(parts, echoEntry(a.alias, a.sensitive))
	}
	return strings.Join(parts, ",")
}

func attachmentEchoSortedOrder() string {
	sorted := make([]struct {
		alias     string
		sensitive bool
	}, len(renderNodeParityAttachments))
	copy(sorted, renderNodeParityAttachments)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].alias < sorted[j].alias })
	parts := make([]string, 0, len(sorted))
	for _, a := range sorted {
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
// structure, not raw guest JSON bytes). The node-id echo must be structurally
// equal across the two languages: cross-language render-socket parity.
//
// The attachment echo is asserted per language rather than compared across them,
// and the asymmetry is the finding, not an oversight. The host emits the node's
// attachments in author order (pinned separately, above), but each language's
// generated codec decodes an author-ordered wire map into that language's own
// map container: TypeScript's object literal keeps the order the document had,
// while Rust's sorted-map container re-orders the keys alphabetically. A guest
// therefore reads the same attachment SET and the same per-attachment values in
// every language, and reads each attachment's sensitivity marking in every
// language, but only reads the author ORDER in the languages whose container
// preserves it. Anything keyed by attachment alias is safe in every language;
// anything that depends on the sequence of the aliases is not. Pinning both
// observed realizations here keeps that fact measured rather than surveyed once.
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
		{"rust", rustOut, attachmentEchoSortedOrder(), "the generated Rust codec decodes the attachment map into a sorted-key container"},
		{"typescript", tsOut, attachmentEchoAuthorOrder(), "a TypeScript object literal keeps the key order the JSON document carried"},
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
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/wasmengine"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The cross-boundary link pass wires a uses edge whose ends assemble into
// different dispatch groups through a provider's link_publish / link_resolve
// exports. These tests drive the unexported pass with REAL wasm engines
// wrapping a compiled test guest, so the actual guest exports run rather than a
// Go stand-in. The guest (aggrega-pdk/rust/examples/link-cross-boundary):
//
//   - link_publish derives a deterministic pointer string from the edge,
//     returns it as the published handle, and returns one add op recording it
//     on the consumed (target) endpoint's "boundary.yaml".
//   - link_resolve reads the handle and returns one add op injecting the
//     pointer into the consuming (source) endpoint's "boundary.yaml".
//
// So the pointer flows publish → handle → resolve, and the pass applies the
// producer-side wiring to the target group and the consumer-side wiring to the
// source group.

const (
	linkFixture      = "link-cross-boundary-rust.wasm"
	renderFixture    = "render-node-rust.wasm"
	linkProvider     = "link-cross-boundary"
	otherProvider    = "render-only"
	linkFile         = "boundary.yaml"
	linkSocket       = "db"
	linkFileInitYAML = "kind: boundary\n"
)

// fixtureEngine builds a real wasmengine engine over a compiled test guest,
// resolved from its on-disk fixture bytes. It skips the test when the wasm
// toolchain is absent (EnsureFixtureBuilt marks that case).
func fixtureEngine(t *testing.T, loader *wasm.Loader, fixture string) renderer.Provider {
	t.Helper()
	path := wasmtest.EnsureFixtureBuilt(t, fixture)
	guestBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", fixture, err)
	}
	uri := "file://" + path
	lookup := func(u string) (pluginpath.ResolvedPlugin, error) {
		return pluginpath.ResolvedPlugin{
			Bytes:  guestBytes,
			URI:    u,
			Tier:   pluginpath.TierLocal,
			Limits: &pluginpath.PluginRuntimeLimits{MaxMemoryPages: 4096},
		}, nil
	}
	return wasmengine.New(lookup, uri, loader)
}

// linkEdge is the single cross-boundary uses edge every case wires: consumer
// (source) uses producer (target) on the "db" socket.
func linkEdge() types.IREdge {
	return types.IREdge{Source: "consumer", Target: "producer", Socket: linkSocket}
}

// TestCrossBoundaryLink_SameProviderWires proves a same-provider cross-boundary
// edge is wired end-to-end through the guest's link exports: the publish patch
// lands on the target group and the resolve patch lands on the source group,
// carrying the pointer the publish half derived.
func TestCrossBoundaryLink_SameProviderWires(t *testing.T) {
	loader := wasm.NewCachingLoader(nil)
	engine := fixtureEngine(t, loader, linkFixture)
	factory := func(string) (renderer.Provider, error) { return engine, nil }

	nodes := map[string]renderer.LinkPassNode{
		"consumer": {Path: "app", Provider: linkProvider, Engine: engine, Files: map[string][]byte{linkFile: []byte(linkFileInitYAML)}},
		"producer": {Path: "data", Provider: linkProvider, Engine: engine, Files: map[string][]byte{linkFile: []byte(linkFileInitYAML)}},
	}

	warnings, filesByPath, err := renderer.RunCrossBoundaryLinkPassForTest(
		context.Background(), []types.IREdge{linkEdge()}, nodes, nil, factory, 2)
	if err != nil {
		t.Fatalf("link pass: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("same-provider wire must not warn, got %v", warnings)
	}

	// The pointer the guest derives from the edge: target node id + socket.
	const wantPointer = "producer--" + linkSocket

	target := string(filesByPath["data"][linkFile])
	source := string(filesByPath["app"][linkFile])

	// Producer-side wiring landed on the target group only.
	if !strings.Contains(target, "published") || !strings.Contains(target, wantPointer) {
		t.Errorf("target file missing publish wiring %q:\n%s", wantPointer, target)
	}
	if strings.Contains(target, "resolved") {
		t.Errorf("consumer-side wiring leaked into the target (producer) file:\n%s", target)
	}
	// Consumer-side wiring landed on the source group only, carrying the
	// pointer the publish half derived (proving the handle round-trip).
	if !strings.Contains(source, "resolved") || !strings.Contains(source, wantPointer) {
		t.Errorf("source file missing resolve wiring %q:\n%s", wantPointer, source)
	}
	if strings.Contains(source, "published") {
		t.Errorf("producer-side wiring leaked into the source (consumer) file:\n%s", source)
	}
	// Byte-faithful: the pre-existing content survives verbatim in both files.
	if !strings.Contains(target, "kind: boundary") || !strings.Contains(source, "kind: boundary") {
		t.Errorf("pre-existing content not preserved:\ntarget=%s\nsource=%s", target, source)
	}
}

// TestCrossBoundaryLink_Deterministic proves the pass produces byte-identical
// patched files across two runs of identical input.
func TestCrossBoundaryLink_Deterministic(t *testing.T) {
	loader := wasm.NewCachingLoader(nil)
	engine := fixtureEngine(t, loader, linkFixture)
	factory := func(string) (renderer.Provider, error) { return engine, nil }

	run := func() map[string]map[string][]byte {
		nodes := map[string]renderer.LinkPassNode{
			"consumer": {Path: "app", Provider: linkProvider, Engine: engine, Files: map[string][]byte{linkFile: []byte(linkFileInitYAML)}},
			"producer": {Path: "data", Provider: linkProvider, Engine: engine, Files: map[string][]byte{linkFile: []byte(linkFileInitYAML)}},
		}
		_, filesByPath, err := renderer.RunCrossBoundaryLinkPassForTest(
			context.Background(), []types.IREdge{linkEdge()}, nodes, nil, factory, 2)
		if err != nil {
			t.Fatalf("link pass: %v", err)
		}
		return filesByPath
	}

	first := run()
	second := run()
	for _, path := range []string{"app", "data"} {
		if !bytes.Equal(first[path][linkFile], second[path][linkFile]) {
			t.Errorf("non-deterministic output for path %q:\nfirst=%s\nsecond=%s",
				path, first[path][linkFile], second[path][linkFile])
		}
	}
}

// TestCrossBoundaryLink_CrossProviderLeavesTheValuePipeInPlace proves the link
// pass is inert on a cross-boundary edge whose ends are rendered by different
// providers: no assembled file changes, so the scalar value the injection
// contract already piped is what the output carries.
//
// The pass raises nothing here, and that is the point rather than an omission.
// The finding an operator reads for such an edge is the declared advisory
// CrossBoundaryLinkEndsShareOneProvider, raised earlier from the stage at which
// both providers are known — so what this row measures is the half the pass still
// owns: that it edits nothing and lets the build finish.
func TestCrossBoundaryLink_CrossProviderLeavesTheValuePipeInPlace(t *testing.T) {
	loader := wasm.NewCachingLoader(nil)
	linkEng := fixtureEngine(t, loader, linkFixture)
	// The target end is a genuinely different, non-linking provider, so only
	// the source advertises linking — the "at least one advertises" gate.
	otherEng := fixtureEngine(t, loader, renderFixture)
	factory := func(p string) (renderer.Provider, error) {
		if p == otherProvider {
			return otherEng, nil
		}
		return linkEng, nil
	}

	nodes := map[string]renderer.LinkPassNode{
		"consumer": {Path: "app", Provider: linkProvider, Engine: linkEng, Files: map[string][]byte{linkFile: []byte(linkFileInitYAML)}},
		"producer": {Path: "data", Provider: otherProvider, Engine: otherEng, Files: map[string][]byte{linkFile: []byte(linkFileInitYAML)}},
	}

	warnings, filesByPath, err := renderer.RunCrossBoundaryLinkPassForTest(
		context.Background(), []types.IREdge{linkEdge()}, nodes, nil, factory, 2)
	if err != nil {
		t.Fatalf("a cross-provider crossing must not halt the pass: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("the pass reports nothing for a cross-provider crossing; got %d: %v",
			len(warnings), warnings)
	}
	// No assembled file changed: the scalar value-pipe stands.
	if got := string(filesByPath["app"][linkFile]); got != linkFileInitYAML {
		t.Errorf("source file changed on unsupported cross-provider edge:\n%s", got)
	}
	if got := string(filesByPath["data"][linkFile]); got != linkFileInitYAML {
		t.Errorf("target file changed on unsupported cross-provider edge:\n%s", got)
	}
}

// TestCrossBoundaryLink_OpaqueTargetHalts proves a publish patch whose target
// assembled file is a bare scalar (an opaque unit the codec cannot address)
// halts the pass loudly with ErrPipelineTargetOpaque.
func TestCrossBoundaryLink_OpaqueTargetHalts(t *testing.T) {
	loader := wasm.NewCachingLoader(nil)
	engine := fixtureEngine(t, loader, linkFixture)
	factory := func(string) (renderer.Provider, error) { return engine, nil }

	nodes := map[string]renderer.LinkPassNode{
		"consumer": {Path: "app", Provider: linkProvider, Engine: engine, Files: map[string][]byte{linkFile: []byte(linkFileInitYAML)}},
		// The target's assembled file is a bare scalar: no object tree-view,
		// so the guest's publish patch cannot address it.
		"producer": {Path: "data", Provider: linkProvider, Engine: engine, Files: map[string][]byte{linkFile: []byte("just a bare scalar line\n")}},
	}

	_, _, err := renderer.RunCrossBoundaryLinkPassForTest(
		context.Background(), []types.IREdge{linkEdge()}, nodes, nil, factory, 2)
	if !errors.Is(err, frp.ErrPipelineTargetOpaque()) {
		t.Fatalf("want ErrPipelineTargetOpaque, got %v", err)
	}
}

// TestCrossBoundaryLink_TemplateMayOnlyCopyALinkedValue holds a template reading
// a value off a connection that crosses into another output to copying it: a
// template writing the value whole, inside a larger string, or through a function
// that leaves it as it is evaluates, its output carrying the delivery placeholder
// the provider later rewrites and the key recorded as read; one that compares,
// branches on, measures or changes the value halts, naming the template and each
// provides key the difference turns on — and only those. A template reading none
// of the connection's values evaluates as it always has and records nothing.
func TestCrossBoundaryLink_TemplateMayOnlyCopyALinkedValue(t *testing.T) {
	var provides types.OrderedMap[string]
	provides.Set("port", "3306")
	provides.Set("host", "!GetAtt Db.Endpoint.Address")
	provides.Set("protocol", "mariadb")
	host := rendercore.CrossBoundaryLinkSentinel("consumer", "producer", "db", "host")
	port := rendercore.CrossBoundaryLinkSentinel("consumer", "producer", "db", "port")

	for _, tc := range []struct {
		name     string
		text     string
		wantOut  string
		wantRead []string
		halts    []string // provides keys the halt names; empty means the template evaluates
		notNamed []string // provides keys the halt must not name
	}{
		{name: "a value written whole", text: `Value: {{ .context.uses.primary.provides.host }}`, wantOut: "Value: " + host, wantRead: []string{"host"}},
		{name: "a value inside a larger string", text: `url: "db://{{ .context.uses.primary.provides.host }}:{{ .context.uses.primary.provides.port }}"`,
			wantOut: `url: "db://` + host + `:` + port + `"`, wantRead: []string{"port", "host"}},
		{name: "a value quoted", text: `{{ .context.uses.primary.provides.host | quote }}`, wantOut: `"` + host + `"`, wantRead: []string{"host"}},
		{name: "a value piped through toString", text: `{{ .context.uses.primary.provides.port | toString }}`, wantOut: port, wantRead: []string{"port"}},
		{name: "no linked value read", text: `socket: {{ .context.uses.primary.socket }}`, wantOut: "socket: db"},
		{name: "a branch on a value", text: `{{ if eq .context.uses.primary.provides.protocol "mariadb" }}yes{{ else }}no{{ end }}`, halts: []string{"protocol"}},
		{name: "a value changed", text: `{{ upper .context.uses.primary.provides.host }}`, halts: []string{"host"}},
		{name: "a value measured", text: `{{ len .context.uses.primary.provides.port }}`, halts: []string{"port"}},
		{name: "a branch beside a copy names only the branched key",
			text:  `{{ .context.uses.primary.provides.host }} {{ if eq .context.uses.primary.provides.protocol "mariadb" }}yes{{ end }}`,
			halts: []string{"protocol"}, notNamed: []string{"host"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("evaluation panicked: %v", r)
				}
			}()
			out, read, err := renderer.EvalOverLinkReadsForTest("artifacts[consumer/template.yaml]", tc.text, provides)
			if len(tc.halts) == 0 {
				if err != nil {
					t.Fatalf("a template copying the value halted: %v", err)
				}
				if out != tc.wantOut {
					t.Errorf("output = %q, want %q", out, tc.wantOut)
				}
				if strings.Contains(out, rendercore.CrossBoundaryLinkSentinelPrefix+"read-") {
					t.Errorf("the read placeholder left the host in the output %q", out)
				}
				if !slices.Equal(read, tc.wantRead) {
					t.Errorf("recorded read keys %q, want %q", read, tc.wantRead)
				}
				return
			}
			if !errors.Is(err, renderer.ErrCrossBoundaryLinkValueNotCopied()) {
				t.Fatalf("want the copy-only halt, got output %q, error %v", out, err)
			}
			msg := err.Error()
			for _, want := range []string{
				"a template does more than copy a value it reads off a cross-boundary link",
				`template "artifacts[consumer/template.yaml]"`,
				"branch on a value of the component's own instead",
			} {
				if !strings.Contains(msg, want) {
					t.Errorf("the halt does not carry %q\n  got: %s", want, msg)
				}
			}
			for _, key := range tc.halts {
				want := `provides key "` + key + `" that component "consumer" reads off its connection "primary" on socket "db" to component "producer"`
				if !strings.Contains(msg, want) {
					t.Errorf("the halt does not name %s\n  got: %s", want, msg)
				}
			}
			for _, key := range tc.notNamed {
				if strings.Contains(msg, `provides key "`+key+`"`) {
					t.Errorf("the halt names %q, which the template only copied\n  got: %s", key, msg)
				}
			}
			if len(read) != 0 {
				t.Errorf("a halting template recorded read keys %q", read)
			}
		})
	}
}

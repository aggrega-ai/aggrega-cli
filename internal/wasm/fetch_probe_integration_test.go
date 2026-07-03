// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/externalinputs"
)

// The external-input store must satisfy the loader's fetch seam; a
// drift in either signature fails compilation here.
var _ FetchService = (*externalinputs.Service)(nil)

// loadFetchProbe builds (or reuses) the fetch-probe fixture and loads
// it through the production Loader carrying svc as its external-input
// service. The fixture's probe export echoes the fetched bytes — or
// the in-band failure message — as its output.
func loadFetchProbe(t *testing.T, ctx context.Context, svc FetchService) *Plugin {
	t.Helper()
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "fetch-probe-go.wasm")

	loader := &Loader{ExternalInputs: svc}
	plugin, err := loader.Load(ctx, PluginSpec{
		Name: "fetch-probe-go",
		Path: pluginPath,
	})
	if err != nil {
		t.Fatalf("Load fetch-probe fixture: %v", err)
	}
	t.Cleanup(func() { _ = plugin.Close(ctx) })
	return plugin
}

// TestFetchProbe_AcquireModeFetchesAndPins exercises the full guest →
// host-function → external-input store path: a real Service in
// acquisition mode over a temp store, backed by a loopback HTTP
// server. The guest's output must be the served bytes, and the
// service must record the acquisition with the plugin's name as the
// requiring site.
func TestFetchProbe_AcquireModeFetchesAndPins(t *testing.T) {
	served := []byte("fetched-through-the-acquisition-channel")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(served)
	}))
	defer srv.Close()
	inputURL := srv.URL + "/input.txt"

	svc := externalinputs.NewService(t.TempDir(), true, nil, nil)
	ctx := context.Background()
	plugin := loadFetchProbe(t, ctx, svc)

	out, err := plugin.Call(ctx, "probe", []byte(inputURL))
	if err != nil {
		t.Fatalf("probe call: %v", err)
	}
	if !bytes.Equal(out, served) {
		t.Fatalf("guest output = %q, want the served bytes %q", out, served)
	}

	recs := svc.AcquiredRecords()
	if len(recs) != 1 {
		t.Fatalf("AcquiredRecords: want exactly one pin, got %d (%+v)", len(recs), recs)
	}
	if recs[0].URL != inputURL {
		t.Errorf("pinned URL = %q, want %q", recs[0].URL, inputURL)
	}
	if !slices.Contains(recs[0].RequiredBy, "fetch-probe-go") {
		t.Errorf("pin provenance %v missing the plugin name", recs[0].RequiredBy)
	}
	if warnings := plugin.Warnings(); len(warnings) != 0 {
		t.Errorf("successful fetch must record no warning; got %v", warnings)
	}
}

// TestFetchProbe_FrozenModeUnpinnedURLFailsInBand proves the frozen-
// closure miss surfaces as a deterministic in-band failure: the guest
// output carries the message naming the URL and the remedy, and the
// Plugin's warning channel carries it too — never a trap.
func TestFetchProbe_FrozenModeUnpinnedURLFailsInBand(t *testing.T) {
	inputURL := "http://127.0.0.1:1/never-pinned.txt"
	svc := externalinputs.NewService(t.TempDir(), false, nil, nil)
	ctx := context.Background()
	plugin := loadFetchProbe(t, ctx, svc)

	out, err := plugin.Call(ctx, "probe", []byte(inputURL))
	if err != nil {
		t.Fatalf("probe call must not trap on a frozen-closure miss: %v", err)
	}
	for _, want := range []string{inputURL, "not pinned"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("guest output missing %q: %s", want, out)
		}
	}
	warnings := plugin.Warnings()
	if len(warnings) != 1 || !strings.Contains(warnings[0], inputURL) {
		t.Errorf("warning channel must carry the failure naming the URL; got %v", warnings)
	}
}

// TestFetchProbe_NilServiceFailsInBand proves the zero-value Loader
// path: with no external-input service configured, the guest receives
// the not-available failure envelope in-band.
func TestFetchProbe_NilServiceFailsInBand(t *testing.T) {
	ctx := context.Background()
	plugin := loadFetchProbe(t, ctx, nil)

	out, err := plugin.Call(ctx, "probe", []byte("https://example.com/x"))
	if err != nil {
		t.Fatalf("probe call must not trap without a service: %v", err)
	}
	for _, want := range []string{"external inputs are not available in this context", "https://example.com/x"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("guest output missing %q: %s", want, out)
		}
	}
}

// TestFetchProbe_DeriveHostFunctionsStampsFetch proves the fixture's
// .wasm import section carries the fetch host function — the
// capability stamp publish derives and the catalog surfaces.
func TestFetchProbe_DeriveHostFunctionsStampsFetch(t *testing.T) {
	pluginPath := wasmtest.EnsureFixtureBuilt(t, "fetch-probe-go.wasm")
	wasmBytes, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	derived, err := DeriveHostFunctions("fetch-probe-go", wasmBytes)
	if err != nil {
		t.Fatalf("DeriveHostFunctions: %v", err)
	}
	if !slices.Contains(derived, "aggrega_fetch_external_input") {
		t.Fatalf("derived host functions %v missing aggrega_fetch_external_input", derived)
	}
}

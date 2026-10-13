// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm_test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
)

// loadParseSourceProbe builds (or reuses) the parse-source-probe fixture and
// loads it through the production Loader's LoadAndDescribe gate — so the
// descriptor's declared host functions are cross-checked against the binary's
// imports as part of the load. The fixture's import export decodes the
// ImportInput it is handed, hands its primary source bytes to the host as a
// source to parse, and answers an ImportOutput in canonical CBOR carrying the
// returned tree as its one file, or an in-band failure in its error field.
func loadParseSourceProbe(t *testing.T, ctx context.Context) (*wasm.Plugin, wasm.PluginDescriptor) {
	t.Helper()
	path := wasmtest.EnsureFixtureBuilt(t, "parse-source-probe-go.wasm")
	loader := &wasm.Loader{}
	plugin, desc, err := loader.LoadAndDescribe(ctx, wasm.PluginSpec{Name: "parse-source-probe-go", Path: path})
	if err != nil {
		t.Fatalf("LoadAndDescribe parse-source-probe fixture: %v", err)
	}
	t.Cleanup(func() { _ = plugin.Close(ctx) })
	return plugin, desc
}

// probeInput is the payload the probe's import export reads: an ImportInput
// carrying source as its primary bytes, in the canonical CBOR the importer
// socket carries, so the probe is driven with exactly what the host hands an
// importer.
func probeInput(t *testing.T, source []byte) []byte {
	t.Helper()
	payload, err := abi.ImportInput{Format: "demo", Bytes: source}.EncodeCBOR()
	if err != nil {
		t.Fatalf("encode probe import input: %v", err)
	}
	return payload
}

// decodeProbeAnswer reads the probe's answer the way the importer socket reads
// an importer's: an ImportOutput in canonical CBOR, through the strict entry
// point. It returns the tree the one file carries on success and the in-band
// failure otherwise. It returns its error rather than failing a test, so a host
// callback can call it.
func decodeProbeAnswer(out []byte) (tree, failure string, err error) {
	var answer abi.ImportOutput
	if err := answer.DecodeCBORStrict(out); err != nil {
		return "", "", fmt.Errorf("the probe's answer is not an ImportOutput in canonical CBOR: %w (bytes %q)", err, out)
	}
	if answer.Error != "" {
		return "", answer.Error, nil
	}
	if len(answer.Files) != 1 {
		return "", "", fmt.Errorf("the probe answered %d files and no failure; want exactly the tree file", len(answer.Files))
	}
	return answer.Files[0].Content, "", nil
}

// probeAnswer is decodeProbeAnswer for a test body, failing the test when the
// answer is not the shape the probe promises.
func probeAnswer(t *testing.T, out []byte) (tree, failure string) {
	t.Helper()
	tree, failure, err := decodeProbeAnswer(out)
	if err != nil {
		t.Fatal(err)
	}
	return tree, failure
}

// embeddedParserSeam returns a SourceParser that obtains a tree by running the
// real parse-go fixture parser THROUGH THE HOST: it loads the parser guest and
// invokes its parse export, threading the ctx it was handed so the depth carried
// on ctx crosses into the parser's own host-call boundary. This stands in for the
// dispatcher-built seam, exercising host-mediated parsing end to end. It speaks
// the parser socket's wire as the host does — the input encoded to canonical CBOR,
// the answer read back through the strict binary decode.
func embeddedParserSeam(t *testing.T) wasm.SourceParser {
	t.Helper()
	parserPath := wasmtest.EnsureFixtureBuilt(t, "parse-go.wasm")
	return func(ctx context.Context, format string, source []byte, options json.RawMessage) (json.RawMessage, error) {
		loader := &wasm.Loader{}
		guest, desc, err := loader.LoadAndDescribe(ctx, wasm.PluginSpec{Name: "parse-go", Path: parserPath})
		if err != nil {
			return nil, err
		}
		defer guest.Close(ctx)
		payload, err := abi.ParseInput{Format: format, Bytes: source, Options: options}.EncodeCBOR()
		if err != nil {
			return nil, err
		}
		out, err := wasm.InvokeExport(ctx, guest, desc, wasm.ParseExport, payload)
		if err != nil {
			return nil, err
		}
		var po abi.ParseOutput
		if err := wasm.DecodeGuestOutputCBOR(wasm.ParseExport, out, &po); err != nil {
			return nil, err
		}
		return po.Tree, nil
	}
}

// TestParseSourceProbe_HostMediatedTree proves the full consumer → host →
// producer path: a consumer guest importing aggrega_parse_source receives a
// host-mediated decoded tree from an embedded parser, and the load cross-checked
// the descriptor's declared aggrega_parse_source against the binary's imports.
func TestParseSourceProbe_HostMediatedTree(t *testing.T) {
	ctx := context.Background()
	plugin, desc := loadParseSourceProbe(t, ctx)

	if !slices.Contains(plugin.HostFunctions(), "aggrega_parse_source") {
		t.Fatalf("plugin host functions %v do not include aggrega_parse_source (load cross-check did not see it)", plugin.HostFunctions())
	}

	plugin.SetSourceParser(embeddedParserSeam(t))

	out, err := wasm.InvokeExport(ctx, plugin, desc, wasm.ImportExport, probeInput(t, []byte(`{"hello":"world"}`)))
	if err != nil {
		t.Fatalf("InvokeExport(import): %v", err)
	}
	// The parse-go scaffold echoes the source as {format, source}; a genuine
	// host-mediated parse ran and its tree reached the consumer verbatim.
	got, failure := probeAnswer(t, out)
	if failure != "" {
		t.Fatalf("consumer reported a failure: %s", failure)
	}
	if !strings.Contains(got, `"format":"json"`) || !strings.Contains(got, `hello`) {
		t.Errorf("consumer tree = %q; want the parser's tree carrying the format and source", got)
	}
}

// TestParseSourceProbe_ReentryGuard is the mandatory guard-test AND the
// ctx-preservation keyboard-verify. A producer reached through the seam attempts
// its own producer-consumption call; the host must deny it with the re-entry
// sentinel. The denial can only fire if the incremented depth carried on ctx
// crossed the wazero host-call boundary into the producer's host-function body —
// so a passing assertion proves ctx-preservation. Were ctx NOT preserved, the
// producer's own seam would run and its canned tree would surface instead.
func TestParseSourceProbe_ReentryGuard(t *testing.T) {
	ctx := context.Background()
	consumer, cdesc := loadParseSourceProbe(t, ctx)

	fixturePath := wasmtest.EnsureFixtureBuilt(t, "parse-source-probe-go.wasm")
	// The consumer's seam loads a PRODUCER (the same probe fixture, which itself
	// calls aggrega_parse_source) and invokes it with the source it was asked to
	// parse, as an import input, threading the ctx the seam was handed. The
	// producer carries its own always-succeeding seam, so a nested call that the
	// guard failed to deny would surface this canned tree.
	const producerCannedTree = `{"nested_producer_seam_ran":true}`
	consumer.SetSourceParser(func(ctx context.Context, format string, source []byte, options json.RawMessage) (json.RawMessage, error) {
		loader := &wasm.Loader{}
		producer, pdesc, err := loader.LoadAndDescribe(ctx, wasm.PluginSpec{Name: "parse-source-producer", Path: fixturePath})
		if err != nil {
			return nil, err
		}
		defer producer.Close(ctx)
		producer.SetSourceParser(func(context.Context, string, []byte, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(producerCannedTree), nil
		})
		payload, err := abi.ImportInput{Format: "demo", Bytes: source}.EncodeCBOR()
		if err != nil {
			return nil, err
		}
		out, err := wasm.InvokeExport(ctx, producer, pdesc, wasm.ImportExport, payload)
		if err != nil {
			return nil, err
		}
		tree, failure, err := decodeProbeAnswer(out)
		if err != nil {
			return nil, err
		}
		if failure != "" {
			return nil, fmt.Errorf("%s", failure)
		}
		return json.RawMessage(tree), nil
	})

	out, err := wasm.InvokeExport(ctx, consumer, cdesc, wasm.ImportExport, probeInput(t, []byte(`{"x":1}`)))
	if err != nil {
		t.Fatalf("InvokeExport(import): %v", err)
	}
	tree, failure := probeAnswer(t, out)
	if strings.Contains(tree, "nested_producer_seam_ran") {
		t.Fatalf("the nested producer-consumption call succeeded — ctx did not carry the depth across the boundary; tree = %q", tree)
	}
	if !strings.Contains(failure, wasm.ErrProducerConsumptionReentry().Error()) {
		t.Fatalf("nested call was not denied with the re-entry sentinel; failure = %q, tree = %q", failure, tree)
	}
}

// TestParseSourceProbe_HostTimeDoesNotSpendTheGuestCeiling proves the per-call
// ceiling bounds GUEST execution and not the host work a guest asks for: a
// consumer loaded with a deliberately tiny ceiling asks the host for a tree, the
// host takes far longer than that ceiling to produce one, and the call still
// succeeds.
//
// The discriminator is structural rather than a timing threshold. The seam's
// spend is a FLOOR the host is made to spend on the ceiling's own clock, not a
// duration measured — so a build that charged host time to the guest cannot
// merely be slow here, it must fail the call outright with a cancellation,
// whatever the machine's speed or load. The two designs therefore differ in
// RESULT, which is what keeps this assertion out of the wall-clock-threshold
// class it would otherwise sit in. The spend is processor time on the guest's
// thread where the ceiling reads processor time, and a sleep where it reads wall
// time (see SpendHostTime): a seam that only slept would spend nothing a
// processor-time ceiling reads, and could not catch the charging.
//
// It is one half of a pair, and neither half stands alone: this half would still
// pass if the ceiling had simply been removed, and
// TestLoaderEnforcesTimeoutCancelsSpinningCall is the other half, where a guest
// that spins WITHOUT calling the host is cancelled. Together they pin the
// distinction the ceiling exists to draw.
func TestParseSourceProbe_HostTimeDoesNotSpendTheGuestCeiling(t *testing.T) {
	// The seam spend sits well above the ceiling so charging it would be
	// unambiguously over budget, and well below any suite-level bound so a
	// passing run stays cheap.
	const ceilingMs = 250
	const seamSpend = time.Second

	ctx := context.Background()
	path := wasmtest.EnsureFixtureBuilt(t, "parse-source-probe-go.wasm")
	loader := &wasm.Loader{}
	plugin, desc, err := loader.LoadAndDescribe(ctx, wasm.PluginSpec{
		Name:      "parse-source-probe-go",
		Path:      path,
		TimeoutMs: ceilingMs,
	})
	if err != nil {
		t.Fatalf("LoadAndDescribe parse-source-probe fixture: %v", err)
	}
	defer plugin.Close(ctx)

	const cannedTree = `{"seam":"slow"}`
	plugin.SetSourceParser(func(context.Context, string, []byte, json.RawMessage) (json.RawMessage, error) {
		wasm.SpendHostTime(seamSpend)
		return json.RawMessage(cannedTree), nil
	})

	out, err := wasm.InvokeExport(ctx, plugin, desc, wasm.ImportExport, probeInput(t, []byte(`{"n":1}`)))
	if err != nil {
		t.Fatalf("a host call spending %s under a %dms guest ceiling must not cancel the guest: %v", seamSpend, ceilingMs, err)
	}
	if tree, failure := probeAnswer(t, out); failure != "" || tree != cannedTree {
		t.Errorf("tree = %q, failure = %q; want the seam tree %q and no failure", tree, failure, cannedTree)
	}
}

// TestParseSourceProbe_SequentialCallsSucceed proves the guard is not
// over-strict: two sequential depth-0 producer-consumption calls both succeed,
// because each enters depth 1 and returns to 0.
func TestParseSourceProbe_SequentialCallsSucceed(t *testing.T) {
	ctx := context.Background()
	plugin, desc := loadParseSourceProbe(t, ctx)

	const cannedTree = `{"seam":"ok"}`
	plugin.SetSourceParser(func(context.Context, string, []byte, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(cannedTree), nil
	})

	for i := 0; i < 2; i++ {
		out, err := wasm.InvokeExport(ctx, plugin, desc, wasm.ImportExport, probeInput(t, []byte(`{"n":1}`)))
		if err != nil {
			t.Fatalf("sequential call %d: InvokeExport(import): %v", i, err)
		}
		if tree, failure := probeAnswer(t, out); failure != "" || tree != cannedTree {
			t.Errorf("sequential call %d tree = %q, failure = %q; want the seam tree %q and no failure", i, tree, failure, cannedTree)
		}
	}
}

// TestParseSourceProbe_NoParser proves a no-parser-for-format failure surfaces
// the ErrNoParserForFormat MESSAGE in-band (not a wasm trap) to the consumer, and
// on the plugin's warning channel.
func TestParseSourceProbe_NoParser(t *testing.T) {
	ctx := context.Background()
	plugin, desc := loadParseSourceProbe(t, ctx)

	plugin.SetSourceParser(func(context.Context, string, []byte, json.RawMessage) (json.RawMessage, error) {
		return nil, fmt.Errorf("%w: no parser plugin advertises %q", catalog.ErrNoParserForFormat(), "parse:bogus")
	})

	out, err := wasm.InvokeExport(ctx, plugin, desc, wasm.ImportExport, probeInput(t, []byte(`{}`)))
	if err != nil {
		t.Fatalf("InvokeExport(import) trapped instead of surfacing in-band: %v", err)
	}
	if _, failure := probeAnswer(t, out); !strings.Contains(failure, catalog.ErrNoParserForFormat().Error()) {
		t.Errorf("consumer failure = %q; want an in-band failure carrying the no-parser message", failure)
	}
	warnings := plugin.DrainWarnings()
	found := false
	for _, w := range warnings {
		if strings.Contains(w, catalog.ErrNoParserForFormat().Error()) {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v; want the no-parser message on the warning channel", warnings)
	}
}

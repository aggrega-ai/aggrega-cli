// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// countingLookup wraps a plugin-path lookup and counts every resolution through
// it. It is a test-only instrument: loadAndDescribeStepPlugin resolves exactly
// once and then crosses to the guest exactly once, with no other path to
// either, so the count is the number of times the build asked a step guest to
// describe itself.
func countingLookup(inner pluginpath.Lookup, crossings *int64) pluginpath.Lookup {
	return func(uri string) (pluginpath.ResolvedPlugin, error) {
		atomic.AddInt64(crossings, 1)
		if inner == nil {
			return pluginpath.ResolvedPlugin{}, errors.New("no plugin resolvable in this test")
		}
		return inner(uri)
	}
}

// unretainedDescriber reproduces the per-site descriptor read as it stood
// before the self-report was retained: every call resolves, loads, describes and
// closes afresh. It is the comparison arm — keeping it here means the "fewer
// crossings" and "identical bytes" claims are measured against a shape that
// actually runs in the same test, rather than against a remembered one.
func unretainedDescriber(lookup pluginpath.Lookup) stepDescriber {
	return func(ctx context.Context, step types.PipelineStep) (wasm.PluginDescriptor, error) {
		return loadAndDescribeStepPlugin(ctx, step.Plugin, lookup)
	}
}

// TestStepDescriptorRetentionCrossesOnceForOnePlugin measures both arms against
// one real step guest: the unretained read crosses once per read, the retained
// one crosses once in total however many readers ask. Both arms run in this
// test, so the comparison is a measurement rather than a claim about a shape
// that is no longer present.
//
// It also pins the two properties that make the retention safe to introduce:
// every read answers with the same content the first crossing produced, and a
// second plugin is still described on its own first read rather than being
// masked by the first plugin's entry.
func TestStepDescriptorRetentionCrossesOnceForOnePlugin(t *testing.T) {
	lookup := refPluginLookup(t)
	t.Parallel()

	const reads = 4
	step := types.PipelineStep{ID: "patch", Plugin: "frp-step-patch-rust.wasm", Function: "step"}
	ctx := context.Background()

	var unretainedCrossings int64
	unretained := unretainedDescriber(countingLookup(lookup, &unretainedCrossings))
	var first wasm.PluginDescriptor
	for i := 0; i < reads; i++ {
		desc, err := unretained(ctx, step)
		if err != nil {
			t.Fatalf("unretained read %d: %v", i, err)
		}
		if i == 0 {
			first = desc
		}
	}
	if unretainedCrossings != reads {
		t.Fatalf("unretained crossings = %d; want %d (one per read)", unretainedCrossings, reads)
	}

	var retainedCrossings int64
	retained := newStepDescriber(newStepDescriptorMemo(), countingLookup(lookup, &retainedCrossings))
	for i := 0; i < reads; i++ {
		desc, err := retained(ctx, step)
		if err != nil {
			t.Fatalf("retained read %d: %v", i, err)
		}
		if !reflect.DeepEqual(desc, first) {
			t.Fatalf("retained read %d returned a different self-report than the first crossing produced", i)
		}
	}
	if retainedCrossings != 1 {
		t.Fatalf("retained crossings = %d; want 1 (the first read only)", retainedCrossings)
	}
	if retainedCrossings >= unretainedCrossings {
		t.Fatalf("retention did not reduce crossings: retained %d, unretained %d", retainedCrossings, unretainedCrossings)
	}

	// The self-report has to be real for the counts above to mean anything: a
	// descriptor nobody could have produced would make both arms agree on
	// nothing. Assert the guest reported the export the steps name.
	if _, ok := findExport(first, step.Function); !ok {
		t.Fatalf("the described guest declares no %q export; the crossing counts describe nothing", step.Function)
	}

	// A second plugin is described on its own first read. Without this the
	// single-crossing result above would also be produced by a memo that
	// answered every URI with whatever it stored first.
	var secondCrossings int64
	perPlugin := newStepDescriber(newStepDescriptorMemo(), countingLookup(lookup, &secondCrossings))
	for _, uri := range []string{"frp-step-patch-rust.wasm", "a-second-plugin.wasm"} {
		if _, err := perPlugin(ctx, types.PipelineStep{ID: "s", Plugin: uri, Function: "step"}); err != nil {
			t.Fatalf("read of %q: %v", uri, err)
		}
	}
	if secondCrossings != 2 {
		t.Fatalf("crossings for two distinct plugins = %d; want 2 (one each)", secondCrossings)
	}
}

// TestStepDescriptorRetentionDescribesNothingUntilAsked pins the laziness the
// retention rests on: building the set and binding an accessor over it crosses
// to no guest, so a plugin that no step ever reaches is never loaded and a
// plugin that cannot load still fails at the moment its first reader asks.
func TestStepDescriptorRetentionDescribesNothingUntilAsked(t *testing.T) {
	t.Parallel()

	var crossings int64
	memo := newStepDescriptorMemo()
	describe := newStepDescriber(memo, countingLookup(nil, &crossings))
	if crossings != 0 {
		t.Fatalf("crossings after constructing the accessor = %d; want 0 — nothing may be described before a reader asks", crossings)
	}

	// The first ask is where the failure surfaces, unchanged in kind and in
	// moment from a read that never retained anything.
	if _, err := describe(context.Background(), types.PipelineStep{ID: "s", Plugin: "unresolvable.wasm", Function: "step"}); err == nil {
		t.Fatal("expected the unresolvable plugin to fail at the first read")
	}
	if crossings != 1 {
		t.Fatalf("crossings after the first ask = %d; want 1", crossings)
	}
}

// TestStepDescriptorRetentionRetainsARefusal proves a plugin the host refuses is
// refused identically at every later read, from one attempt. Retaining only the
// successes would let a second reader re-attempt a load the first reader already
// failed, so two readers of one plugin could disagree about whether it loads.
func TestStepDescriptorRetentionRetainsARefusal(t *testing.T) {
	t.Parallel()

	var crossings int64
	describe := newStepDescriber(newStepDescriptorMemo(), countingLookup(nil, &crossings))
	step := types.PipelineStep{ID: "s", Plugin: "unresolvable.wasm", Function: "step"}

	firstErr := errOf(t, describe, step)
	secondErr := errOf(t, describe, step)
	if firstErr.Error() != secondErr.Error() {
		t.Fatalf("two reads of one refused plugin disagreed: %v vs %v", firstErr, secondErr)
	}
	if crossings != 1 {
		t.Fatalf("crossings for a refused plugin read twice = %d; want 1", crossings)
	}
}

// errOf reads a descriptor expecting a refusal and returns the error.
func errOf(t *testing.T, describe stepDescriber, step types.PipelineStep) error {
	t.Helper()
	_, err := describe(context.Background(), step)
	if err == nil {
		t.Fatal("expected a refusal, got none")
	}
	return err
}

// TestPipelineEmitsIdenticalBytesWithAndWithoutRetention drives one real
// multi-step pipeline over a real guest twice — once through the per-site read
// that preceded retention, once through the retained one — and compares what
// each emitted. It is the byte-identity half of the retention claim, measured
// on both arms in one run.
//
// The corpus is asserted non-empty before it is compared: two empty emissions
// are trivially identical, so a comparison that could pass on nothing would
// prove nothing.
func TestPipelineEmitsIdenticalBytesWithAndWithoutRetention(t *testing.T) {
	lookup := refPluginLookup(t)
	t.Parallel()

	// Several steps naming one plugin is the shape the retention changes: the
	// unretained arm describes the plugin once per step, the retained arm once.
	steps := []types.PipelineStep{
		{ID: "patch-a", Plugin: "frp-step-patch-rust.wasm", Function: "step"},
		{ID: "patch-b", Plugin: "frp-step-patch-rust.wasm", Function: "step"},
		{ID: "patch-c", Plugin: "frp-step-patch-rust.wasm", Function: "step"},
	}

	run := func(describe stepDescriber, crossings *int64, countedLookup pluginpath.Lookup) pipelineResult {
		t.Helper()
		own := []EmittedFile{{Name: "bundle.yaml", Origin: EmitOriginWorkload, Body: []byte(multiDocOwn)}}
		codec := newStockYAMLCodec(2)
		res, err := runNodePipelineWithInvoker(
			context.Background(), steps, own, nil, false, "helm", "",
			renderctx.RenderContext{}, &types.TemplateContext{},
			codec, codec,
			newStepInvoker(countedLookup), describe, "node",
		)
		if err != nil {
			t.Fatalf("pipeline run: %v", err)
		}
		return res
	}

	var unretainedCrossings int64
	unretainedLookup := countingLookup(lookup, &unretainedCrossings)
	before := run(unretainedDescriber(unretainedLookup), &unretainedCrossings, unretainedLookup)

	var retainedCrossings int64
	retainedLookup := countingLookup(lookup, &retainedCrossings)
	after := run(newStepDescriber(newStepDescriptorMemo(), retainedLookup), &retainedCrossings, retainedLookup)

	// Anti-needle: a parity of two empty emissions must not be able to pass.
	if len(before.Own) == 0 {
		t.Fatal("the pre-retention arm emitted no files; there is nothing to compare")
	}
	var emittedBytes int
	for _, f := range before.Own {
		emittedBytes += len(f.Body)
	}
	if emittedBytes == 0 {
		t.Fatal("the pre-retention arm emitted only empty files; there is nothing to compare")
	}
	t.Logf("compared %d emitted file(s), %d byte(s) total", len(before.Own), emittedBytes)

	if len(before.Own) != len(after.Own) {
		t.Fatalf("emitted file count moved: %d before, %d after", len(before.Own), len(after.Own))
	}
	for i := range before.Own {
		if before.Own[i].Name != after.Own[i].Name {
			t.Fatalf("emitted file %d renamed: %q before, %q after", i, before.Own[i].Name, after.Own[i].Name)
		}
		if string(before.Own[i].Body) != string(after.Own[i].Body) {
			t.Fatalf("emitted bytes of %q are not identical across the two arms", before.Own[i].Name)
		}
	}
	if !reflect.DeepEqual(before.HostPatched, after.HostPatched) {
		t.Fatal("host-scope modifications are not identical across the two arms")
	}
	if !reflect.DeepEqual(before.Warnings, after.Warnings) {
		t.Fatal("warnings are not identical across the two arms")
	}

	// Both arms ran the same steps through the same invoker, so the difference
	// between the two counts is the describe crossings the retention removed.
	if retainedCrossings >= unretainedCrossings {
		t.Fatalf("retention did not reduce crossings over the pipeline: retained %d, unretained %d",
			retainedCrossings, unretainedCrossings)
	}
	t.Logf("crossings over %d steps: %d unretained, %d retained", len(steps), unretainedCrossings, retainedCrossings)
}

// TestStepDescriptorRetentionSpansSeparatePipelineRuns proves the retention
// carries across independent pipeline runs rather than only within one, which is
// the property the render depends on: the sites that run a node's pipeline and
// the site that replays a deferred one are separate calls, and they collapse to
// one crossing only because they are handed the same retained set.
//
// It drives the executor twice under two node identities, once with the set
// shared and once with each call left to retain on its own, and compares the
// crossings each arrangement costs.
func TestStepDescriptorRetentionSpansSeparatePipelineRuns(t *testing.T) {
	lookup := refPluginLookup(t)
	t.Parallel()

	steps := []types.PipelineStep{{ID: "patch", Plugin: "frp-step-patch-rust.wasm", Function: "step"}}

	runTwice := func(shared *stepDescriptorMemo) (int64, []pipelineResult) {
		t.Helper()
		var crossings int64
		counted := countingLookup(lookup, &crossings)
		var out []pipelineResult
		for _, nodeID := range []string{"node-a", "node-b"} {
			own := []EmittedFile{{Name: "bundle.yaml", Origin: EmitOriginWorkload, Body: []byte(multiDocOwn)}}
			res, err := runNodePipeline(
				context.Background(), steps, own, nil, false, "helm", "",
				renderctx.RenderContext{}, &types.TemplateContext{}, 2,
				counted, shared, nil, nodeID,
			)
			if err != nil {
				t.Fatalf("pipeline run for %q: %v", nodeID, err)
			}
			out = append(out, res)
		}
		return crossings, out
	}

	perCall, perCallOut := runTwice(nil)
	sharedCrossings, sharedOut := runTwice(newStepDescriptorMemo())

	if sharedCrossings >= perCall {
		t.Fatalf("sharing the retained set across runs did not reduce crossings: shared %d, per-call %d",
			sharedCrossings, perCall)
	}
	t.Logf("crossings across two pipeline runs: %d retaining per call, %d sharing one set", perCall, sharedCrossings)

	// The two arrangements must still emit the same bytes, or the reduction was
	// bought by rendering something different.
	if len(perCallOut) != len(sharedOut) {
		t.Fatalf("run count differs: %d vs %d", len(perCallOut), len(sharedOut))
	}
	var compared int
	for i := range perCallOut {
		if len(perCallOut[i].Own) == 0 {
			t.Fatalf("run %d emitted no files; there is nothing to compare", i)
		}
		if len(perCallOut[i].Own) != len(sharedOut[i].Own) {
			t.Fatalf("run %d emitted %d file(s) per call and %d sharing", i, len(perCallOut[i].Own), len(sharedOut[i].Own))
		}
		for j := range perCallOut[i].Own {
			if string(perCallOut[i].Own[j].Body) != string(sharedOut[i].Own[j].Body) {
				t.Fatalf("run %d file %q: emitted bytes differ between the two arrangements", i, perCallOut[i].Own[j].Name)
			}
			compared++
		}
	}
	if compared == 0 {
		t.Fatal("no emitted file was compared; the parity assertion proves nothing")
	}
	t.Logf("compared %d emitted file(s) across the two arrangements", compared)
}

// populatedDescriptor is a self-report with every field carrying content,
// including every slice, pointer and raw message the shape offers. The
// isolation test needs a value where each aliasing field is non-empty, because
// a field left at its zero value cannot demonstrate that it was or was not
// copied.
func populatedDescriptor() wasm.PluginDescriptor {
	return wasm.PluginDescriptor{
		AbiVersion:   "v1alpha1",
		PluginType:   "feature-recipe-pipeline",
		Capabilities: []string{"helm", "compose"},
		Exports: []wasm.PluginExport{{
			Name:          "step",
			InputPattern:  "b",
			HostFunctions: []string{"aggrega_log"},
			Description:   "a step",
			Stability:     "stable",
			Operates:      "patch",
			Scope:         "own",
			Selector:      "a selector",
			Input:         json.RawMessage(`{"type":"object"}`),
			Output:        json.RawMessage(`{"type":"object"}`),
		}},
		Contract: &wasm.ProviderContract{
			InputKinds:             wasm.InputKinds{Sourced: true, Synthesized: true, ArtifactsStandalone: true},
			BoundaryAssembly:       "merge",
			ArtifactDelimiters:     &wasm.ArtifactDelimiters{Left: "[[", Right: "]]"},
			SourceFilter:           []string{"chart/**"},
			AssemblyNameAnnotation: &wasm.AssemblyNameAnnotation{Annotation: "aggrega.ai/name", RequireUnique: true},
			ReusableInstance:       true,
			CrossBoundaryLinking:   true,
		},
		Description:    "a plugin",
		Commands:       []wasm.PluginCLICommand{{Name: "do", Description: "does", Flags: []wasm.PluginCommandFlag{{Name: "flag", Shorthand: "f", Description: "a flag", Type: "string", Default: "x"}}}},
		SettingsSchema: json.RawMessage(`{"type":"object"}`),
		Metadata:       &wasm.PluginDescriptorMeta{Author: "a", Repository: "b", License: "c", Version: "d", Commit: "e"},
	}
}

// TestIsolatedDescriptorSharesNoMutableMemory proves the copy handed to a reader
// reaches none of the retained value's memory, in both directions: a reader
// writing through what it was given cannot change what a later reader sees, and
// a later write to the retained value cannot change what an earlier reader is
// still holding.
//
// The mutations are applied through every aliasing field the shape offers, so a
// copy that went shallow anywhere is caught rather than merely a copy that went
// shallow at the top.
func TestIsolatedDescriptorSharesNoMutableMemory(t *testing.T) {
	t.Parallel()

	retained := populatedDescriptor()
	pristine := populatedDescriptor()
	handed := isolatedDescriptor(retained)

	mutate(&handed)
	if !reflect.DeepEqual(retained, pristine) {
		t.Fatal("writing through the handed-out copy reached the retained value")
	}

	stillHeld := isolatedDescriptor(retained)
	mutate(&retained)
	if !reflect.DeepEqual(stillHeld, pristine) {
		t.Fatal("writing to the retained value reached a copy a reader was still holding")
	}
}

// mutate writes through every field of the descriptor that can alias, so the
// isolation test fails on a copy that shares any one of them.
func mutate(d *wasm.PluginDescriptor) {
	d.Capabilities[0] = "mutated"
	d.Exports[0].Name = "mutated"
	d.Exports[0].HostFunctions[0] = "mutated"
	d.Exports[0].Input[0] = 'X'
	d.Exports[0].Output[0] = 'X'
	d.Contract.BoundaryAssembly = "mutated"
	d.Contract.SourceFilter[0] = "mutated"
	d.Contract.ArtifactDelimiters.Left = "mutated"
	d.Contract.AssemblyNameAnnotation.Annotation = "mutated"
	d.Commands[0].Name = "mutated"
	d.Commands[0].Flags[0].Name = "mutated"
	d.SettingsSchema[0] = 'X'
	d.Metadata.Author = "mutated"
}

// TestRetainedDescriptorSurvivesAReadersMutation proves the isolation holds
// through the accessor itself over a real guest, not only over the copy helper:
// a reader that writes through what the accessor returned finds the next read
// unchanged.
func TestRetainedDescriptorSurvivesAReadersMutation(t *testing.T) {
	lookup := refPluginLookup(t)
	t.Parallel()

	describe := newStepDescriber(newStepDescriptorMemo(), lookup)
	step := types.PipelineStep{ID: "patch", Plugin: "frp-step-patch-rust.wasm", Function: "step"}
	ctx := context.Background()

	first, err := describe(ctx, step)
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	// The real self-report must offer something to write through, or the
	// mutation below would be a no-op and the assertion would pass on nothing.
	if len(first.Exports) == 0 {
		t.Fatal("the described guest declares no exports; there is nothing to mutate")
	}

	// Snapshot through the self-report's own encoding rather than through the
	// copy this test exists to check. Taking the snapshot with that copy would
	// blind the comparison exactly when the copy is the thing at fault: a
	// snapshot that shared the value's memory would follow the mutation and
	// then match, reporting a pass for the one state the test is looking for.
	before := marshalDescriptor(t, first)

	first.Exports[0].Name = "mutated"
	first.Exports[0].Scope = "mutated"
	if len(first.Capabilities) > 0 {
		first.Capabilities[0] = "mutated"
	}
	if first.Contract != nil {
		first.Contract.BoundaryAssembly = "mutated"
	}

	second, err := describe(ctx, step)
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if marshalDescriptor(t, second) != before {
		t.Fatal("a reader's writes reached the retained self-report the next reader was answered from")
	}
}

// marshalDescriptor encodes a self-report through its own generated encoding,
// giving a snapshot that owns its bytes. It is deliberately not the copy the
// retention hands out: a snapshot taken with that copy could alias the value it
// was meant to preserve, and a comparison against such a snapshot passes
// whatever the copy does.
func marshalDescriptor(t *testing.T, desc wasm.PluginDescriptor) string {
	t.Helper()
	raw, err := json.Marshal(desc)
	if err != nil {
		t.Fatalf("encode self-report: %v", err)
	}
	return string(raw)
}

// TestDescriptorShapeStaysFullyIsolatable is the drift gate on the copy. The
// reflective walk reaches a field only when the reflect package can set it, and
// only for the kinds the walk knows how to rebuild — so a self-report that grew
// an unexported field, or a field of a kind the walk cannot copy, would be
// shared rather than copied, silently and with no failing assertion anywhere
// else.
//
// It walks the self-report's whole type tree and refuses either shape, so the
// contract growing a field that the copy cannot isolate fails here instead of
// weakening the isolation in production.
func TestDescriptorShapeStaysFullyIsolatable(t *testing.T) {
	t.Parallel()

	seen := map[reflect.Type]bool{}
	var walk func(path string, ty reflect.Type)
	walk = func(path string, ty reflect.Type) {
		if seen[ty] {
			return
		}
		seen[ty] = true
		switch ty.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array:
			walk(path+"[]", ty.Elem())
		case reflect.Map:
			walk(path+".key", ty.Key())
			walk(path+".value", ty.Elem())
		case reflect.Struct:
			for i := 0; i < ty.NumField(); i++ {
				f := ty.Field(i)
				if !f.IsExported() {
					t.Errorf("%s.%s is unexported: the copy cannot isolate it, so it would be shared with every reader",
						path, f.Name)
					continue
				}
				walk(path+"."+f.Name, f.Type)
			}
		case reflect.Chan, reflect.Func, reflect.UnsafePointer:
			t.Errorf("%s is a %s: the copy cannot rebuild it, so it would be shared with every reader", path, ty.Kind())
		case reflect.Interface:
			// An interface's dynamic value is isolated at copy time; its static
			// type says nothing about what it will hold, so there is nothing to
			// refuse here.
		default:
			// A scalar carries no memory to share.
		}
	}
	walk("PluginDescriptor", reflect.TypeOf(wasm.PluginDescriptor{}))
}

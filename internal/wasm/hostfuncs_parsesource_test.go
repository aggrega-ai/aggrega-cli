// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package wasm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi/go/pkg/abi"
)

// parseSourceRequest encodes a ParseInput into the request bytes the
// aggrega_parse_source host function body reads from plugin memory.
func parseSourceRequest(t *testing.T, format string, source []byte, options json.RawMessage) []byte {
	t.Helper()
	b, err := abi.ParseInput{Format: format, Bytes: source, Options: options}.EncodeCBOR()
	if err != nil {
		t.Fatalf("encode ParseInput: %v", err)
	}
	return b
}

// lastParseSourceEnvelope decodes the most recent payload the fake writer
// received as a ParseSourceResponse.
func lastParseSourceEnvelope(t *testing.T, w *fakeMemoryWriter) abi.ParseSourceResponse {
	t.Helper()
	if len(w.written) == 0 {
		t.Fatalf("no envelope was written to plugin memory")
	}
	var resp abi.ParseSourceResponse
	if err := resp.DecodeCBOR(w.written[len(w.written)-1]); err != nil {
		t.Fatalf("envelope is not a decodable ParseSourceResponse: %v (payload %x)", err, w.written[len(w.written)-1])
	}
	return resp
}

// TestHandleParseSource_Success pins the success path: the handler decodes the
// request, runs the injected seam, and writes the tree the seam produced. It
// also proves the seam is invoked at the incremented producer-consumption depth,
// so a producer reached through it observes depth 1.
func TestHandleParseSource_Success(t *testing.T) {
	t.Parallel()
	var gotFormat string
	var gotSource []byte
	var gotDepth int
	seam := func(ctx context.Context, format string, source []byte, options json.RawMessage) (json.RawMessage, error) {
		gotFormat = format
		gotSource = source
		gotDepth = producerConsumptionDepthFromContext(ctx)
		return json.RawMessage(`{"k":1}`), nil
	}
	p := &Plugin{name: "parse-source-test", sourceParser: seam}
	reader := &fakeMemoryReader{bytesByOffset: map[uint64][]byte{7: parseSourceRequest(t, "json", []byte("hi"), nil)}}
	writer := &fakeMemoryWriter{}

	stack := []uint64{7}
	p.handleAggregaParseSource(context.Background(), reader, writer, stack)

	if gotFormat != "json" || string(gotSource) != "hi" {
		t.Errorf("seam received format=%q source=%q; want json/hi", gotFormat, gotSource)
	}
	if gotDepth != 1 {
		t.Errorf("seam observed producer-consumption depth %d; want 1 (incremented at the seam entry)", gotDepth)
	}
	env := lastParseSourceEnvelope(t, writer)
	if env.Error != "" {
		t.Fatalf("success path wrote an error envelope: %q", env.Error)
	}
	if string(env.Tree) != `{"k":1}` {
		t.Errorf("envelope tree = %q; want the seam's tree {\"k\":1}", env.Tree)
	}
	if stack[0] != 1000 {
		t.Errorf("stack[0] = %d; want the writer's returned offset 1000", stack[0])
	}
}

// TestHandleParseSource_ReentryDenied proves the re-entrancy guard: a call
// arriving at the producer-consumption depth limit is denied loudly with the
// ErrProducerConsumptionReentry sentinel's message, the seam is never invoked,
// and the denial is recorded on the warning channel.
func TestHandleParseSource_ReentryDenied(t *testing.T) {
	t.Parallel()
	seamCalled := false
	seam := func(ctx context.Context, format string, source []byte, options json.RawMessage) (json.RawMessage, error) {
		seamCalled = true
		return json.RawMessage(`{"unreached":true}`), nil
	}
	p := &Plugin{name: "parse-source-test", sourceParser: seam}
	reader := &fakeMemoryReader{bytesByOffset: map[uint64][]byte{7: parseSourceRequest(t, "json", []byte("hi"), nil)}}
	writer := &fakeMemoryWriter{}

	ctx := contextWithProducerConsumptionDepth(context.Background(), producerConsumptionDepthLimit)
	p.handleAggregaParseSource(ctx, reader, writer, []uint64{7})

	if seamCalled {
		t.Fatalf("seam was invoked on a re-entrant call; the guard must deny before running the producer")
	}
	env := lastParseSourceEnvelope(t, writer)
	if !strings.Contains(env.Error, ErrProducerConsumptionReentry().Error()) {
		t.Errorf("envelope error = %q; want it to carry the re-entry sentinel message %q", env.Error, ErrProducerConsumptionReentry().Error())
	}
	if env.HostfuncErrorCode != abi.CodeProducerConsumptionReentry {
		t.Errorf("hostfunc_error_code = %q; want %q", env.HostfuncErrorCode, abi.CodeProducerConsumptionReentry)
	}
	warnings := p.DrainWarnings()
	if len(warnings) != 1 || !strings.Contains(warnings[0], ErrProducerConsumptionReentry().Error()) {
		t.Errorf("warnings = %v; want one warning carrying the re-entry message", warnings)
	}
}

// TestHandleParseSource_ReentryBeatsMissingSeam pins the guard ordering: a
// re-entrant call is denied as a re-entry even when the reached plugin carries no
// seam, so the salient failure (the cycle) is what surfaces.
func TestHandleParseSource_ReentryBeatsMissingSeam(t *testing.T) {
	t.Parallel()
	p := &Plugin{name: "parse-source-test"} // nil sourceParser
	reader := &fakeMemoryReader{bytesByOffset: map[uint64][]byte{7: parseSourceRequest(t, "json", []byte("hi"), nil)}}
	writer := &fakeMemoryWriter{}

	ctx := contextWithProducerConsumptionDepth(context.Background(), producerConsumptionDepthLimit)
	p.handleAggregaParseSource(ctx, reader, writer, []uint64{7})

	env := lastParseSourceEnvelope(t, writer)
	if !strings.Contains(env.Error, ErrProducerConsumptionReentry().Error()) {
		t.Errorf("envelope error = %q; want the re-entry message to win over the missing-seam message", env.Error)
	}
}

// TestHandleParseSource_MissingSeam proves a call with no injected seam fails
// loudly in-band rather than as a silent no-op.
func TestHandleParseSource_MissingSeam(t *testing.T) {
	t.Parallel()
	p := &Plugin{name: "parse-source-test"} // nil sourceParser
	reader := &fakeMemoryReader{bytesByOffset: map[uint64][]byte{7: parseSourceRequest(t, "json", []byte("hi"), nil)}}
	writer := &fakeMemoryWriter{}

	p.handleAggregaParseSource(context.Background(), reader, writer, []uint64{7})

	env := lastParseSourceEnvelope(t, writer)
	if !strings.Contains(env.Error, "no source parser is available") {
		t.Errorf("envelope error = %q; want the no-seam-available message", env.Error)
	}
	if env.HostfuncErrorCode != abi.CodeNoSourceParser {
		t.Errorf("hostfunc_error_code = %q; want %q", env.HostfuncErrorCode, abi.CodeNoSourceParser)
	}
	if len(p.DrainWarnings()) != 1 {
		t.Errorf("missing-seam failure was not recorded on the warning channel")
	}
}

// TestHandleParseSource_SeamErrorSurfaced proves an error the seam returns
// crosses in-band verbatim and lands on the warning channel — the path a
// no-parser-for-format failure takes, whose message the injected seam carries.
func TestHandleParseSource_SeamErrorSurfaced(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("no parser plugin advertises the requested format")
	seam := func(ctx context.Context, format string, source []byte, options json.RawMessage) (json.RawMessage, error) {
		return nil, sentinel
	}
	p := &Plugin{name: "parse-source-test", sourceParser: seam}
	reader := &fakeMemoryReader{bytesByOffset: map[uint64][]byte{7: parseSourceRequest(t, "helm", []byte("x"), nil)}}
	writer := &fakeMemoryWriter{}

	p.handleAggregaParseSource(context.Background(), reader, writer, []uint64{7})

	env := lastParseSourceEnvelope(t, writer)
	if env.Error != sentinel.Error() {
		t.Errorf("envelope error = %q; want the seam's error message %q", env.Error, sentinel.Error())
	}
	if env.HostfuncErrorCode != abi.CodeParseFailed {
		t.Errorf("hostfunc_error_code = %q; want the generic %q for an uncoded seam error", env.HostfuncErrorCode, abi.CodeParseFailed)
	}
	warnings := p.DrainWarnings()
	if len(warnings) != 1 || !strings.Contains(warnings[0], sentinel.Error()) {
		t.Errorf("warnings = %v; want one warning carrying the seam error", warnings)
	}
}

// TestHandleParseSource_CodedSeamError proves the boundary reads the wire code
// off a CodedHostError the seam returns: a parser-resolution failure wrapped by
// the importer seam adapter surfaces as no_parser_for_format, with the human
// message preserved. This is the sentinel-to-wire-code mapping done at the seam,
// not by importing the sentinel's package into the host boundary.
func TestHandleParseSource_CodedSeamError(t *testing.T) {
	t.Parallel()
	underlying := errors.New("resolve parser for format \"terraform\": no parser plugin advertises the requested format")
	seam := func(ctx context.Context, format string, source []byte, options json.RawMessage) (json.RawMessage, error) {
		return nil, &CodedHostError{Code: abi.CodeNoParserForFormat, Err: underlying}
	}
	p := &Plugin{name: "parse-source-test", sourceParser: seam}
	reader := &fakeMemoryReader{bytesByOffset: map[uint64][]byte{7: parseSourceRequest(t, "terraform", []byte("x"), nil)}}
	writer := &fakeMemoryWriter{}

	p.handleAggregaParseSource(context.Background(), reader, writer, []uint64{7})

	env := lastParseSourceEnvelope(t, writer)
	if env.HostfuncErrorCode != abi.CodeNoParserForFormat {
		t.Errorf("hostfunc_error_code = %q; want %q from the coded seam error", env.HostfuncErrorCode, abi.CodeNoParserForFormat)
	}
	if env.Error != underlying.Error() {
		t.Errorf("envelope error = %q; want the coded error's underlying message %q", env.Error, underlying.Error())
	}
}

// TestHandleParseSource_BadRequest proves a malformed request fails in-band
// rather than trapping, and the seam is never reached. The fixture is a text
// document, which is the shape the request slot does not carry: it is a
// well-formed JSON object and still undecodable here, so the assertion also
// shows the handler is not silently tolerant of the other encoding.
func TestHandleParseSource_BadRequest(t *testing.T) {
	t.Parallel()
	seamCalled := false
	seam := func(ctx context.Context, format string, source []byte, options json.RawMessage) (json.RawMessage, error) {
		seamCalled = true
		return nil, nil
	}
	p := &Plugin{name: "parse-source-test", sourceParser: seam}
	reader := &fakeMemoryReader{bytesByOffset: map[uint64][]byte{7: []byte(`{"format":"json","bytes":"aGk="}`)}}
	writer := &fakeMemoryWriter{}

	p.handleAggregaParseSource(context.Background(), reader, writer, []uint64{7})

	if seamCalled {
		t.Fatalf("seam was invoked on a malformed request")
	}
	env := lastParseSourceEnvelope(t, writer)
	if !strings.Contains(env.Error, "decoding the request failed") {
		t.Errorf("envelope error = %q; want the decode-failure message", env.Error)
	}
	if env.HostfuncErrorCode != abi.CodeRequestUndecodable {
		t.Errorf("hostfunc_error_code = %q; want %q", env.HostfuncErrorCode, abi.CodeRequestUndecodable)
	}
}

// TestHandleParseSource_UnreadableRequest proves an unreadable request slot
// fails in-band with the unreadable-request code rather than trapping.
func TestHandleParseSource_UnreadableRequest(t *testing.T) {
	t.Parallel()
	p := &Plugin{name: "parse-source-test"}
	reader := &fakeMemoryReader{errOnOffset: 7}
	writer := &fakeMemoryWriter{}

	p.handleAggregaParseSource(context.Background(), reader, writer, []uint64{7})

	env := lastParseSourceEnvelope(t, writer)
	if !strings.Contains(env.Error, "reading the request from plugin memory failed") {
		t.Errorf("envelope error = %q; want the unreadable-request message", env.Error)
	}
	if env.HostfuncErrorCode != abi.CodeRequestUndecodable && env.HostfuncErrorCode != abi.CodeRequestUnreadable {
		t.Errorf("hostfunc_error_code = %q; want %q", env.HostfuncErrorCode, abi.CodeRequestUnreadable)
	}
}

// TestHandleParseSource_RequestSlotIsBytesNotText pins the request slot's shape
// at the read seam, which is the one property a round trip through this handler
// cannot establish on its own: a handler and a guest that made the same mistake
// about the slot's shape would agree with each other and pass every round-trip
// assertion.
//
// The fixture is registered ONLY on the reader's bytes channel. A handler that
// read the slot as text would find no text fixture at that offset, take the
// unreadable-request branch, and never reach the seam — so the seam observing the
// request is the assertion that the bytes read is the one performed.
func TestHandleParseSource_RequestSlotIsBytesNotText(t *testing.T) {
	t.Parallel()
	seamReached := false
	seam := func(ctx context.Context, format string, source []byte, options json.RawMessage) (json.RawMessage, error) {
		seamReached = true
		return json.RawMessage(`{}`), nil
	}
	p := &Plugin{name: "parse-source-test", sourceParser: seam}
	reader := &fakeMemoryReader{
		bytesByOffset:   map[uint64][]byte{7: parseSourceRequest(t, "json", []byte("hi"), nil)},
		stringsByOffset: map[uint64]string{}, // deliberately empty: a text read finds nothing here
	}
	writer := &fakeMemoryWriter{}

	p.handleAggregaParseSource(context.Background(), reader, writer, []uint64{7})

	if !seamReached {
		env := lastParseSourceEnvelope(t, writer)
		t.Fatalf("the request slot was not read as bytes; the handler failed with %q (code %q)", env.Error, env.HostfuncErrorCode)
	}
}

// TestParseSourceEnvelope_BothShapesEncodeInTheSameEncoding pins that the failure
// envelope and the success envelope travel in one encoding. A guest reads one
// reply slot and cannot know before decoding which of the two it holds, so a
// failure envelope in the other encoding would be undecodable by exactly the path
// that has to report the failure.
//
// Both shapes are asserted because each hides the other's defect: a handler that
// encoded only the success shape correctly passes any test built from success
// fixtures alone, and one that encoded only the failure shape correctly passes
// any test built from failure fixtures alone.
func TestParseSourceEnvelope_BothShapesEncodeInTheSameEncoding(t *testing.T) {
	t.Parallel()

	// Success shape: the seam produced a tree.
	p := &Plugin{name: "parse-source-test", sourceParser: func(context.Context, string, []byte, json.RawMessage) (json.RawMessage, error) {
		return json.RawMessage(`{"a":1}`), nil
	}}
	writer := &fakeMemoryWriter{}
	p.handleAggregaParseSource(context.Background(),
		&fakeMemoryReader{bytesByOffset: map[uint64][]byte{7: parseSourceRequest(t, "json", []byte("hi"), nil)}},
		writer, []uint64{7})
	if len(writer.written) == 0 {
		t.Fatalf("success path wrote no envelope")
	}
	gotSuccess := writer.written[len(writer.written)-1]
	wantSuccess, err := abi.ParseSourceResponse{Tree: json.RawMessage(`{"a":1}`)}.EncodeCBOR()
	if err != nil {
		t.Fatalf("encode the expected success envelope: %v", err)
	}
	if !bytes.Equal(gotSuccess, wantSuccess) {
		t.Errorf("success envelope bytes = %x; want %x", gotSuccess, wantSuccess)
	}

	// Failure shape: the same handler, no seam.
	pf := &Plugin{name: "parse-source-test"}
	wf := &fakeMemoryWriter{}
	pf.handleAggregaParseSource(context.Background(),
		&fakeMemoryReader{bytesByOffset: map[uint64][]byte{7: parseSourceRequest(t, "json", []byte("hi"), nil)}},
		wf, []uint64{7})
	if len(wf.written) == 0 {
		t.Fatalf("failure path wrote no envelope")
	}
	gotFailure := wf.written[len(wf.written)-1]
	wantFailure, err := abi.ParseSourceResponse{
		Error:             "aggrega_parse_source: no source parser is available in this context",
		HostfuncErrorCode: abi.CodeNoSourceParser,
	}.EncodeCBOR()
	if err != nil {
		t.Fatalf("encode the expected failure envelope: %v", err)
	}
	if !bytes.Equal(gotFailure, wantFailure) {
		t.Errorf("failure envelope bytes = %x; want %x", gotFailure, wantFailure)
	}
}

// TestParseSourceHostFunction_CrossCheck proves the aggrega_parse_source host
// function participates in the load-time host-function cross-check: a descriptor
// declaring it while the binary imports it passes, and a binary importing it that
// the descriptor omits is rejected.
func TestParseSourceHostFunction_CrossCheck(t *testing.T) {
	t.Parallel()
	desc := PluginDescriptor{
		Exports: []PluginExport{{Name: "import", HostFunctions: []string{"aggrega_parse_source"}}},
	}
	if err := crossCheckHostFunctions("p", desc, []string{"aggrega_parse_source"}); err != nil {
		t.Errorf("declared+imported aggrega_parse_source should cross-check clean: %v", err)
	}
	if err := crossCheckHostFunctions("p", PluginDescriptor{}, []string{"aggrega_parse_source"}); err == nil {
		t.Errorf("importing aggrega_parse_source without declaring it must be rejected")
	}
}

// TestParseSourceHostFunction_Registered proves the host function is registered
// in the allowlist and the signature registry with the i64->i64 shape, so a
// build-profile guest importing it loads and instantiates.
func TestParseSourceHostFunction_Registered(t *testing.T) {
	t.Parallel()
	if !hostFunctionAllowlist["aggrega_parse_source"] {
		t.Errorf("aggrega_parse_source is not in the host-function allowlist")
	}
	sig, ok := hostFunctionSignatureFor("aggrega_parse_source")
	if !ok {
		t.Fatalf("aggrega_parse_source has no registered signature")
	}
	if len(sig.params) != 1 || len(sig.results) != 1 {
		t.Errorf("aggrega_parse_source signature = params %v results %v; want one i64 param and one i64 result", sig.params, sig.results)
	}
}

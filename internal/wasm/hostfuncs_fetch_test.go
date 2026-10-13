// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
)

// fakeMemoryWriter is a pluginMemoryWriter test double the fetch host-
// function body tests use in place of *extism.CurrentPlugin. It records
// each written payload and hands back a synthetic non-zero offset per
// write; failWrite forces the write-failure branch.
type fakeMemoryWriter struct {
	written   [][]byte
	failWrite bool
}

func (f *fakeMemoryWriter) WriteBytes(b []byte) (uint64, error) {
	if f.failWrite {
		return 0, fmt.Errorf("simulated write failure")
	}
	f.written = append(f.written, b)
	// Synthetic non-zero offset: 1000 + index of this write.
	return uint64(1000 + len(f.written) - 1), nil
}

// lastEnvelope decodes the most recent payload the fake writer received
// as a FetchExternalInputResponse, through the reply type's canonical-CBOR
// entry point — the one decode a guest reaches for either outcome. It
// decodes strictly, so a reply carrying a key the shape does not declare
// fails here rather than decoding to an empty value.
func (f *fakeMemoryWriter) lastEnvelope(t *testing.T) abi.FetchExternalInputResponse {
	t.Helper()
	var env abi.FetchExternalInputResponse
	if err := env.DecodeCBORStrict(f.lastWritten(t)); err != nil {
		t.Fatalf("envelope is not the reply type's canonical CBOR: %v (payload %x)", err, f.lastWritten(t))
	}
	return env
}

// lastWritten returns the most recent payload the fake writer received,
// verbatim.
func (f *fakeMemoryWriter) lastWritten(t *testing.T) []byte {
	t.Helper()
	if len(f.written) == 0 {
		t.Fatalf("no envelope was written to plugin memory")
	}
	return f.written[len(f.written)-1]
}

// fakeFetchService is a FetchService test double capturing the fetch
// arguments and returning canned bytes or a canned error.
type fakeFetchService struct {
	gotURL        string
	gotRequiredBy string
	payload       []byte
	err           error
}

func (f *fakeFetchService) Fetch(_ context.Context, url string, requiredBy string) ([]byte, error) {
	f.gotURL = url
	f.gotRequiredBy = requiredBy
	if f.err != nil {
		return nil, f.err
	}
	return f.payload, nil
}

func TestHandleFetchExternalInputSuccess(t *testing.T) {
	t.Parallel()
	svc := &fakeFetchService{payload: []byte("external payload bytes")}
	p := &Plugin{name: "fetch-test-plugin", externalInputs: svc}
	reader := &fakeMemoryReader{stringsByOffset: map[uint64]string{7: "https://example.com/schema.json"}}
	writer := &fakeMemoryWriter{}

	stack := []uint64{7}
	p.handleAggregaFetchExternalInput(context.Background(), reader, writer, stack)

	if svc.gotURL != "https://example.com/schema.json" {
		t.Errorf("service received URL %q, want the guest-passed URL", svc.gotURL)
	}
	if svc.gotRequiredBy != "fetch-test-plugin" {
		t.Errorf("service received requiredBy %q, want the plugin name", svc.gotRequiredBy)
	}
	if stack[0] != 1000 {
		t.Errorf("stack[0] = %d, want the writer's returned offset 1000", stack[0])
	}

	env := writer.lastEnvelope(t)
	if env.Error != "" || env.HostfuncErrorCode != "" {
		t.Fatalf("success path wrote a failure envelope: %+v", env)
	}
	if string(env.Content) != "external payload bytes" {
		t.Errorf("content %q, want the fetched bytes verbatim", env.Content)
	}
	if warnings := p.Warnings(); len(warnings) != 0 {
		t.Errorf("success path must record no warning; got %v", warnings)
	}
}

// TestHandleFetchExternalInputContentCrossesAsRawBytes pins the content's
// wire form, read from the canonical encoding's own rules rather than from
// the decoder: a one-pair map whose value is a CBOR byte string (major type
// 2) holding the fetched bytes exactly. A host that base64-encoded the bytes
// first, or wrote them as a text string, produces different bytes here even
// where its own decoder would round-trip them.
//
// The payloads are chosen so that neither shortcut can pass: one is not valid
// UTF-8, which a text string cannot carry, and one is long enough that its
// length needs a one-byte head of its own.
func TestHandleFetchExternalInputContentCrossesAsRawBytes(t *testing.T) {
	t.Parallel()
	long := bytes.Repeat([]byte("abcdefgh"), 5) // 40 bytes: the length rides in a 0x58 head.
	for _, tc := range []struct {
		name    string
		payload []byte
		head    []byte
	}{
		{"not valid UTF-8", []byte{0xff, 0xfe, 0x00, 0xc3, 0x28}, []byte{0x45}},
		{"a one-byte length head", long, []byte{0x58, byte(len(long))}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := &Plugin{name: "fetch-bytes-plugin", externalInputs: &fakeFetchService{payload: tc.payload}}
			reader := &fakeMemoryReader{stringsByOffset: map[uint64]string{7: "https://example.com/bytes"}}
			writer := &fakeMemoryWriter{}

			p.handleAggregaFetchExternalInput(context.Background(), reader, writer, []uint64{7})

			// a1: map, 1 pair; 67 "content": the key as a 7-byte text string; then
			// the byte-string head and the payload verbatim.
			want := append([]byte{0xa1, 0x67}, "content"...)
			want = append(want, tc.head...)
			want = append(want, tc.payload...)
			if got := writer.lastWritten(t); !bytes.Equal(got, want) {
				t.Errorf("reply bytes = %x\nwant          %x", got, want)
			}
			if env := writer.lastEnvelope(t); !bytes.Equal(env.Content, tc.payload) {
				t.Errorf("decoded content = %x; want the fetched bytes %x", env.Content, tc.payload)
			}
		})
	}
}

// TestHandleFetchExternalInputZeroLengthPayloadIsTheEmptyReply pins how a
// zero-length payload stays distinguishable from a failure. The content
// field is omitted when empty, so the reply carries no field at all — the
// empty map — while every failure carries its error message and code. A
// guest therefore reads success from the absence of the failure fields, not
// from the presence of the content key.
func TestHandleFetchExternalInputZeroLengthPayloadIsTheEmptyReply(t *testing.T) {
	t.Parallel()
	svc := &fakeFetchService{payload: []byte{}}
	p := &Plugin{name: "fetch-empty-plugin", externalInputs: svc}
	reader := &fakeMemoryReader{stringsByOffset: map[uint64]string{7: "https://example.com/empty"}}
	writer := &fakeMemoryWriter{}

	p.handleAggregaFetchExternalInput(context.Background(), reader, writer, []uint64{7})

	// a0: map, 0 pairs.
	if got := writer.lastWritten(t); !bytes.Equal(got, []byte{0xa0}) {
		t.Fatalf("zero-length payload reply = %x; want the empty map a0", got)
	}
	env := writer.lastEnvelope(t)
	if env.Error != "" || env.HostfuncErrorCode != "" {
		t.Errorf("zero-length payload is a success, not a failure: %+v", env)
	}
	if len(env.Content) != 0 {
		t.Errorf("zero-length payload decoded to content %x", env.Content)
	}
	if warnings := p.Warnings(); len(warnings) != 0 {
		t.Errorf("a zero-length payload must record no warning; got %v", warnings)
	}
}

func TestHandleFetchExternalInputNilService(t *testing.T) {
	t.Parallel()
	p := &Plugin{name: "no-service-plugin"} // externalInputs nil: zero-value Loader path
	reader := &fakeMemoryReader{stringsByOffset: map[uint64]string{7: "https://example.com/x"}}
	writer := &fakeMemoryWriter{}

	stack := []uint64{7}
	p.handleAggregaFetchExternalInput(context.Background(), reader, writer, stack)

	env := writer.lastEnvelope(t)
	if len(env.Content) != 0 {
		t.Fatalf("nil-service path must not carry content")
	}
	for _, want := range []string{"external inputs are not available in this context", "https://example.com/x"} {
		if !strings.Contains(env.Error, want) {
			t.Errorf("error envelope missing %q: %s", want, env.Error)
		}
	}
	if env.HostfuncErrorCode != abi.CodeFetchInputUnavailable {
		t.Errorf("hostfunc_error_code = %q; want %q", env.HostfuncErrorCode, abi.CodeFetchInputUnavailable)
	}
	warnings := p.Warnings()
	if len(warnings) != 1 || !strings.Contains(warnings[0], "external inputs are not available in this context") {
		t.Errorf("failure envelope must mirror onto the warning channel; got %v", warnings)
	}
	if stack[0] == 0 {
		t.Errorf("error envelope must still be delivered at a non-zero offset")
	}
}

func TestHandleFetchExternalInputFetchError(t *testing.T) {
	t.Parallel()
	svc := &fakeFetchService{err: errors.New(`"https://example.com/y" is not pinned in the lockfile; re-run with acquisition enabled`)}
	p := &Plugin{name: "frozen-plugin", externalInputs: svc}
	reader := &fakeMemoryReader{stringsByOffset: map[uint64]string{7: "https://example.com/y"}}
	writer := &fakeMemoryWriter{}

	p.handleAggregaFetchExternalInput(context.Background(), reader, writer, []uint64{7})

	env := writer.lastEnvelope(t)
	if !strings.Contains(env.Error, "https://example.com/y") || !strings.Contains(env.Error, "not pinned") {
		t.Errorf("error envelope must carry the service's message verbatim; got %q", env.Error)
	}
	if env.HostfuncErrorCode != abi.CodeFetchFailed {
		t.Errorf("hostfunc_error_code = %q; want %q", env.HostfuncErrorCode, abi.CodeFetchFailed)
	}
	warnings := p.Warnings()
	if len(warnings) != 1 || !strings.Contains(warnings[0], "not pinned") {
		t.Errorf("fetch failure must record a warning carrying the message; got %v", warnings)
	}
}

func TestHandleFetchExternalInputMemoryReadFailure(t *testing.T) {
	// NOT t.Parallel — captureLogOutput modifies process-wide log state.
	buf := captureLogOutput(t)

	p := &Plugin{name: "read-fail-plugin", externalInputs: &fakeFetchService{}}
	reader := &fakeMemoryReader{stringsByOffset: map[uint64]string{}} // empty → read fails
	writer := &fakeMemoryWriter{}

	stack := []uint64{99}
	// Must not panic.
	p.handleAggregaFetchExternalInput(context.Background(), reader, writer, stack)

	if !strings.Contains(buf.String(), "aggrega_fetch_external_input: memory read failed") {
		t.Errorf("log output missing memory-read-failure diagnostic: %q", buf.String())
	}
	env := writer.lastEnvelope(t)
	if !strings.Contains(env.Error, "reading the URL from plugin memory failed") {
		t.Errorf("read failure must still deliver an error envelope; got %+v", env)
	}
	if env.HostfuncErrorCode != abi.CodeFetchRequestUnreadable {
		t.Errorf("hostfunc_error_code = %q; want %q", env.HostfuncErrorCode, abi.CodeFetchRequestUnreadable)
	}
}

func TestHandleFetchExternalInputMemoryWriteFailure(t *testing.T) {
	// NOT t.Parallel — captureLogOutput modifies process-wide log state.
	buf := captureLogOutput(t)

	p := &Plugin{name: "write-fail-plugin", externalInputs: &fakeFetchService{payload: []byte("x")}}
	reader := &fakeMemoryReader{stringsByOffset: map[uint64]string{7: "https://example.com/z"}}
	writer := &fakeMemoryWriter{failWrite: true}

	stack := []uint64{7}
	// Must not panic; the zero offset is the no-envelope signal.
	p.handleAggregaFetchExternalInput(context.Background(), reader, writer, stack)

	if stack[0] != 0 {
		t.Errorf("write failure must return offset 0, got %d", stack[0])
	}
	if !strings.Contains(buf.String(), "aggrega_fetch_external_input: memory write failed") {
		t.Errorf("log output missing memory-write-failure diagnostic: %q", buf.String())
	}
}

// TestHostFunctionsForDeclaredIncludesFetch proves the allowlist name
// routes to a bound closure — a name admitted by the subset gate but
// missing from the builder switch would panic here.
func TestHostFunctionsForDeclaredIncludesFetch(t *testing.T) {
	t.Parallel()
	functions := hostFunctionsForDeclared([]string{"aggrega_fetch_external_input"})
	if len(functions) != 1 {
		t.Fatalf("expected one bound host function, got %d", len(functions))
	}
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
// as a FetchExternalInputResponse.
func (f *fakeMemoryWriter) lastEnvelope(t *testing.T) abi.FetchExternalInputResponse {
	t.Helper()
	if len(f.written) == 0 {
		t.Fatalf("no envelope was written to plugin memory")
	}
	var env abi.FetchExternalInputResponse
	if err := json.Unmarshal(f.written[len(f.written)-1], &env); err != nil {
		t.Fatalf("envelope is not valid JSON: %v (payload %q)", err, f.written[len(f.written)-1])
	}
	return env
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
	if env.Error != "" {
		t.Fatalf("success path wrote an error envelope: %q", env.Error)
	}
	if env.Content == nil {
		t.Fatalf("success envelope carries no content key")
	}
	decoded, err := base64.StdEncoding.DecodeString(*env.Content)
	if err != nil {
		t.Fatalf("content is not standard base64: %v", err)
	}
	if string(decoded) != "external payload bytes" {
		t.Errorf("decoded content %q, want the fetched bytes", decoded)
	}
	if warnings := p.Warnings(); len(warnings) != 0 {
		t.Errorf("success path must record no warning; got %v", warnings)
	}
}

func TestHandleFetchExternalInputZeroLengthPayloadKeepsContentKey(t *testing.T) {
	t.Parallel()
	svc := &fakeFetchService{payload: []byte{}}
	p := &Plugin{name: "fetch-empty-plugin", externalInputs: svc}
	reader := &fakeMemoryReader{stringsByOffset: map[uint64]string{7: "https://example.com/empty"}}
	writer := &fakeMemoryWriter{}

	p.handleAggregaFetchExternalInput(context.Background(), reader, writer, []uint64{7})

	env := writer.lastEnvelope(t)
	if env.Content == nil || *env.Content != "" {
		t.Fatalf("zero-length payload must yield an empty content value, got %+v", env)
	}
	if env.Error != "" {
		t.Errorf("zero-length payload is a success, not %q", env.Error)
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
	if env.Content != nil {
		t.Fatalf("nil-service path must not carry content")
	}
	for _, want := range []string{"external inputs are not available in this context", "https://example.com/x"} {
		if !strings.Contains(env.Error, want) {
			t.Errorf("error envelope missing %q: %s", want, env.Error)
		}
	}
	if env.ErrorCode != abi.CodeFetchInputUnavailable {
		t.Errorf("error_code = %q; want %q", env.ErrorCode, abi.CodeFetchInputUnavailable)
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
	if env.ErrorCode != abi.CodeFetchFailed {
		t.Errorf("error_code = %q; want %q", env.ErrorCode, abi.CodeFetchFailed)
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
	if env.ErrorCode != abi.CodeFetchRequestUnreadable {
		t.Errorf("error_code = %q; want %q", env.ErrorCode, abi.CodeFetchRequestUnreadable)
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
	p := &Plugin{name: "switch-plugin"}
	functions := p.hostFunctionsForDeclared([]string{"aggrega_fetch_external_input"})
	if len(functions) != 1 {
		t.Fatalf("expected one bound host function, got %d", len(functions))
	}
}

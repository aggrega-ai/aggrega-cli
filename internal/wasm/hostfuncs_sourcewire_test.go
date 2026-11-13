// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package wasm

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi/go/pkg/abi"
)

// Wire-level tests for the two source callbacks — aggrega_pack_source and
// aggrega_read_source_file. They cover the properties a round trip through the
// handler cannot establish on its own: which shape the request slot is read as,
// and that the success and failure envelopes travel in one encoding.
//
// The two callbacks share this file because they share one wire property and one
// set of helpers; splitting them would duplicate the fixture builders and the
// envelope decoders, and the second copy is what drifts. The selection-logic
// tests for the packer stay in their own file, which is about what the packer
// packs rather than about how the request and the reply travel.

// packSourceRequest encodes a SourcePackRequest into the request bytes the
// aggrega_pack_source host function body reads from plugin memory.
func packSourceRequest(t *testing.T, op, path string) []byte {
	t.Helper()
	b, err := abi.SourcePackRequest{Op: op, Path: path}.EncodeCBOR()
	if err != nil {
		t.Fatalf("encode SourcePackRequest: %v", err)
	}
	return b
}

// sourceFileRequest encodes a SourceFileRequest into the request bytes the
// aggrega_read_source_file host function body reads from plugin memory.
func sourceFileRequest(t *testing.T, path string) []byte {
	t.Helper()
	b, err := abi.SourceFileRequest{Path: path}.EncodeCBOR()
	if err != nil {
		t.Fatalf("encode SourceFileRequest: %v", err)
	}
	return b
}

// lastSourcePackEnvelope decodes the most recent payload the fake writer
// received as a SourcePackResponse.
func lastSourcePackEnvelope(t *testing.T, w *fakeMemoryWriter) abi.SourcePackResponse {
	t.Helper()
	if len(w.written) == 0 {
		t.Fatalf("no envelope was written to plugin memory")
	}
	var resp abi.SourcePackResponse
	if err := resp.DecodeCBOR(w.written[len(w.written)-1]); err != nil {
		t.Fatalf("envelope is not a decodable SourcePackResponse: %v (payload %x)", err, w.written[len(w.written)-1])
	}
	return resp
}

// lastSourceFileEnvelope decodes the most recent payload the fake writer
// received as a SourceFileResponse.
func lastSourceFileEnvelope(t *testing.T, w *fakeMemoryWriter) abi.SourceFileResponse {
	t.Helper()
	if len(w.written) == 0 {
		t.Fatalf("no envelope was written to plugin memory")
	}
	var resp abi.SourceFileResponse
	if err := resp.DecodeCBOR(w.written[len(w.written)-1]); err != nil {
		t.Fatalf("envelope is not a decodable SourceFileResponse: %v (payload %x)", err, w.written[len(w.written)-1])
	}
	return resp
}

// writeOneFileSource lays out a source root holding a single file and returns
// the root and the file's content.
func writeOneFileSource(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	const content = "the-packed-payload"
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, content
}

// TestHandlePackSource_RequestSlotIsBytesNotText pins the request slot's shape at
// the read seam, which is the one property a round trip through this handler
// cannot establish on its own: a handler and a guest that made the same mistake
// about the slot's shape would agree with each other and pass every round-trip
// assertion.
//
// The fixture is registered ONLY on the reader's bytes channel. A handler that
// read the slot as text would find no text fixture at that offset, take the
// unreadable-request branch, and never pack anything — so a populated projection
// in the reply is the assertion that the bytes read is the one performed.
func TestHandlePackSource_RequestSlotIsBytesNotText(t *testing.T) {
	t.Parallel()
	root, content := writeOneFileSource(t)
	p := &Plugin{name: "pack-source-test", sourceRoot: root}
	reader := &fakeMemoryReader{
		bytesByOffset:   map[uint64][]byte{7: packSourceRequest(t, "file", "a.txt")},
		stringsByOffset: map[uint64]string{}, // deliberately empty: a text read finds nothing here
	}
	writer := &fakeMemoryWriter{}

	stack := []uint64{7}
	p.handleAggregaPackSource(reader, writer, stack)

	env := lastSourcePackEnvelope(t, writer)
	if env.Error != "" {
		t.Fatalf("the request slot was not read as bytes; the handler failed with %q (code %q)", env.Error, env.HostfuncErrorCode)
	}
	if env.SourceProjection == nil || len(env.SourceProjection.Files) != 1 {
		t.Fatalf("reply projection = %+v; want one packed file", env.SourceProjection)
	}
	if got := string(env.SourceProjection.Files[0].Content); got != content {
		t.Errorf("packed content = %q; want %q", got, content)
	}
	if stack[0] != 1000 {
		t.Errorf("stack[0] = %d; want the writer's returned offset 1000", stack[0])
	}
}

// TestHandleReadSourceFile_RequestSlotIsBytesNotText is the content-read sibling
// of the pack-source slot-shape test, and exists for the same reason: the slot's
// shape is invisible to any round trip that gets it wrong on both sides.
func TestHandleReadSourceFile_RequestSlotIsBytesNotText(t *testing.T) {
	t.Parallel()
	root, content := writeOneFileSource(t)
	p := &Plugin{name: "read-source-file-test", sourceRoot: root}
	reader := &fakeMemoryReader{
		bytesByOffset:   map[uint64][]byte{7: sourceFileRequest(t, "a.txt")},
		stringsByOffset: map[uint64]string{}, // deliberately empty: a text read finds nothing here
	}
	writer := &fakeMemoryWriter{}

	stack := []uint64{7}
	p.handleAggregaReadSourceFile(reader, writer, stack)

	env := lastSourceFileEnvelope(t, writer)
	if env.Error != "" {
		t.Fatalf("the request slot was not read as bytes; the handler failed with %q (code %q)", env.Error, env.HostfuncErrorCode)
	}
	if string(env.Content) != content {
		t.Errorf("reply content = %q; want %q", env.Content, content)
	}
	if stack[0] != 1000 {
		t.Errorf("stack[0] = %d; want the writer's returned offset 1000", stack[0])
	}
}

// TestPackSourceEnvelope_BothShapesEncodeInTheSameEncoding pins that the failure
// envelope and the success envelope travel in one encoding. A guest reads one
// reply slot and cannot know before decoding which of the two it holds, so a
// failure envelope in the other encoding would be undecodable by exactly the path
// that has to report the failure.
//
// Both shapes are asserted because each hides the other's defect: a handler that
// encoded only the success shape correctly passes any test built from success
// fixtures alone, and one that encoded only the failure shape correctly passes
// any test built from failure fixtures alone.
//
// The map head byte is asserted separately from the byte comparison, and from the
// encoding's own rules rather than from the encoder: a success envelope carries
// exactly one pair because the two failure fields are omitted when empty, and a
// failure envelope carries exactly two because the projection is. Comparing only
// against the encoder's output would agree with a desynchronised head; reading
// the head is what does not.
func TestPackSourceEnvelope_BothShapesEncodeInTheSameEncoding(t *testing.T) {
	t.Parallel()

	// Success shape: a real pack of a one-file source root.
	root, content := writeOneFileSource(t)
	p := &Plugin{name: "pack-source-test", sourceRoot: root}
	writer := &fakeMemoryWriter{}
	p.handleAggregaPackSource(
		&fakeMemoryReader{bytesByOffset: map[uint64][]byte{7: packSourceRequest(t, "file", "a.txt")}},
		writer, []uint64{7})
	if len(writer.written) == 0 {
		t.Fatalf("success path wrote no envelope")
	}
	gotSuccess := writer.written[len(writer.written)-1]
	wantSuccess, err := abi.SourcePackResponse{SourceProjection: &abi.SourceProjection{
		Files: []abi.SourceFile{{Path: "a.txt", Content: []byte(content)}},
	}}.EncodeCBOR()
	if err != nil {
		t.Fatalf("encode the expected success envelope: %v", err)
	}
	if !bytes.Equal(gotSuccess, wantSuccess) {
		t.Errorf("success envelope bytes = %x; want %x", gotSuccess, wantSuccess)
	}
	// a1: map, 1 pair — the projection alone, both failure fields omitted.
	if gotSuccess[0] != 0xa1 {
		t.Errorf("success envelope head = %#x; want 0xa1 (one pair: the projection alone)", gotSuccess[0])
	}

	// Failure shape: the same handler, no acquired source.
	pf := &Plugin{name: "pack-source-test"} // empty sourceRoot
	wf := &fakeMemoryWriter{}
	pf.handleAggregaPackSource(
		&fakeMemoryReader{bytesByOffset: map[uint64][]byte{7: packSourceRequest(t, "file", "a.txt")}},
		wf, []uint64{7})
	if len(wf.written) == 0 {
		t.Fatalf("failure path wrote no envelope")
	}
	gotFailure := wf.written[len(wf.written)-1]
	wantFailure, err := abi.SourcePackResponse{
		Error:             "aggrega_pack_source: no acquired source is available in this context",
		HostfuncErrorCode: abi.CodeSourceRootUnavailable,
	}.EncodeCBOR()
	if err != nil {
		t.Fatalf("encode the expected failure envelope: %v", err)
	}
	if !bytes.Equal(gotFailure, wantFailure) {
		t.Errorf("failure envelope bytes = %x; want %x", gotFailure, wantFailure)
	}
	// a2: map, 2 pairs — the message and the code, the projection omitted.
	if gotFailure[0] != 0xa2 {
		t.Errorf("failure envelope head = %#x; want 0xa2 (two pairs: message and code, projection omitted)", gotFailure[0])
	}
}

// TestReadSourceFileEnvelope_BothShapesEncodeInTheSameEncoding is the content-read
// sibling, asserted for the same reason and in both directions of the omission
// property: content present with the failure fields omitted, then the failure
// fields present with content omitted.
func TestReadSourceFileEnvelope_BothShapesEncodeInTheSameEncoding(t *testing.T) {
	t.Parallel()

	// Success shape: a real read of a one-file source root.
	root, content := writeOneFileSource(t)
	p := &Plugin{name: "read-source-file-test", sourceRoot: root}
	writer := &fakeMemoryWriter{}
	p.handleAggregaReadSourceFile(
		&fakeMemoryReader{bytesByOffset: map[uint64][]byte{7: sourceFileRequest(t, "a.txt")}},
		writer, []uint64{7})
	if len(writer.written) == 0 {
		t.Fatalf("success path wrote no envelope")
	}
	gotSuccess := writer.written[len(writer.written)-1]
	wantSuccess, err := abi.SourceFileResponse{Content: []byte(content)}.EncodeCBOR()
	if err != nil {
		t.Fatalf("encode the expected success envelope: %v", err)
	}
	if !bytes.Equal(gotSuccess, wantSuccess) {
		t.Errorf("success envelope bytes = %x; want %x", gotSuccess, wantSuccess)
	}
	// a1: map, 1 pair — the content alone, both failure fields omitted.
	if gotSuccess[0] != 0xa1 {
		t.Errorf("success envelope head = %#x; want 0xa1 (one pair: the content alone)", gotSuccess[0])
	}

	// Failure shape: the same handler, no acquired source.
	pf := &Plugin{name: "read-source-file-test"} // empty sourceRoot
	wf := &fakeMemoryWriter{}
	pf.handleAggregaReadSourceFile(
		&fakeMemoryReader{bytesByOffset: map[uint64][]byte{7: sourceFileRequest(t, "a.txt")}},
		wf, []uint64{7})
	if len(wf.written) == 0 {
		t.Fatalf("failure path wrote no envelope")
	}
	gotFailure := wf.written[len(wf.written)-1]
	wantFailure, err := abi.SourceFileResponse{
		Error:             "aggrega_read_source_file: no acquired source is available in this context",
		HostfuncErrorCode: abi.CodeSourceRootUnavailable,
	}.EncodeCBOR()
	if err != nil {
		t.Fatalf("encode the expected failure envelope: %v", err)
	}
	if !bytes.Equal(gotFailure, wantFailure) {
		t.Errorf("failure envelope bytes = %x; want %x", gotFailure, wantFailure)
	}
	// a2: map, 2 pairs — the message and the code, the content omitted.
	if gotFailure[0] != 0xa2 {
		t.Errorf("failure envelope head = %#x; want 0xa2 (two pairs: message and code, content omitted)", gotFailure[0])
	}
}

// TestHandlePackSource_BadRequest proves a malformed request fails in-band rather
// than trapping, and nothing is packed. The fixture is a text document, which is
// the shape the request slot does not carry: it is a well-formed JSON object and
// still undecodable here, so the assertion also shows the handler is not silently
// tolerant of the other encoding.
func TestHandlePackSource_BadRequest(t *testing.T) {
	t.Parallel()
	root, _ := writeOneFileSource(t)
	p := &Plugin{name: "pack-source-test", sourceRoot: root}
	reader := &fakeMemoryReader{bytesByOffset: map[uint64][]byte{7: []byte(`{"op":"file","path":"a.txt"}`)}}
	writer := &fakeMemoryWriter{}

	p.handleAggregaPackSource(reader, writer, []uint64{7})

	env := lastSourcePackEnvelope(t, writer)
	if env.SourceProjection != nil {
		t.Fatalf("a malformed request produced a projection: %+v", env.SourceProjection)
	}
	if env.HostfuncErrorCode != abi.CodeRequestUndecodable {
		t.Errorf("hostfunc_error_code = %q; want %q", env.HostfuncErrorCode, abi.CodeRequestUndecodable)
	}
}

// TestHandleReadSourceFile_BadRequest is the content-read sibling: a textual
// request is undecodable here too, so a guest left on the other encoding gets the
// undecodable-request diagnosis rather than some downstream shape error.
func TestHandleReadSourceFile_BadRequest(t *testing.T) {
	t.Parallel()
	root, _ := writeOneFileSource(t)
	p := &Plugin{name: "read-source-file-test", sourceRoot: root}
	reader := &fakeMemoryReader{bytesByOffset: map[uint64][]byte{7: []byte(`{"path":"a.txt"}`)}}
	writer := &fakeMemoryWriter{}

	p.handleAggregaReadSourceFile(reader, writer, []uint64{7})

	env := lastSourceFileEnvelope(t, writer)
	if len(env.Content) != 0 {
		t.Fatalf("a malformed request produced content: %q", env.Content)
	}
	if env.HostfuncErrorCode != abi.CodeRequestUndecodable {
		t.Errorf("hostfunc_error_code = %q; want %q", env.HostfuncErrorCode, abi.CodeRequestUndecodable)
	}
}

// TestHandlePackSource_UnreadableRequest proves an unreadable request slot fails
// in-band with the unreadable-request code rather than trapping.
func TestHandlePackSource_UnreadableRequest(t *testing.T) {
	t.Parallel()
	p := &Plugin{name: "pack-source-test"}
	writer := &fakeMemoryWriter{}

	p.handleAggregaPackSource(&fakeMemoryReader{errOnOffset: 7}, writer, []uint64{7})

	env := lastSourcePackEnvelope(t, writer)
	if env.HostfuncErrorCode != abi.CodeRequestUnreadable {
		t.Errorf("hostfunc_error_code = %q; want %q", env.HostfuncErrorCode, abi.CodeRequestUnreadable)
	}
}

// TestHandleReadSourceFile_UnreadableRequest is the content-read sibling of the
// unreadable-slot assertion.
func TestHandleReadSourceFile_UnreadableRequest(t *testing.T) {
	t.Parallel()
	p := &Plugin{name: "read-source-file-test"}
	writer := &fakeMemoryWriter{}

	p.handleAggregaReadSourceFile(&fakeMemoryReader{errOnOffset: 7}, writer, []uint64{7})

	env := lastSourceFileEnvelope(t, writer)
	if env.HostfuncErrorCode != abi.CodeRequestUnreadable {
		t.Errorf("hostfunc_error_code = %q; want %q", env.HostfuncErrorCode, abi.CodeRequestUnreadable)
	}
}

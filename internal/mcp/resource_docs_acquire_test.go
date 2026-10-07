// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// coldDocsWorkspace returns a server pointed at an EMPTY workspace — no bundle
// cached — whose bundle target is a store holding the fixture bundle. It is the
// state a real installation starts in, which the warm-cache fixture beside it
// deliberately is not.
//
// Only the target is substituted. The cache-presence probe, the pull, the
// media-type check, the cache write and the read are the production path.
func coldDocsWorkspace(t *testing.T) (*server, string) {
	t.Helper()
	ws := t.TempDir()

	store := memory.New()
	if _, err := oci.Push(context.Background(), store, docsTestVersion, oci.MediaTypeDocs, docsFixtureBundle(t), nil); err != nil {
		t.Fatalf("Push: %v", err)
	}

	return &server{
		out:               io.Discard,
		logw:              io.Discard,
		serverVersion:     docsTestVersion,
		startDir:          ws,
		docsBundleRef:     docsTestRef,
		docsBundleVersion: docsTestVersion,
		docsTarget: func(*toolWorkspace, string, string, func(string)) (oras.ReadOnlyTarget, docscache.VerifyAtPull, error) {
			return store, nil, nil
		},
	}, ws
}

// runServer drives a server a test has already pointed at its own workspace and
// bundle seams through the entry the command layer enters, and returns the
// framed responses.
//
// It enters at the run rather than at a handler because the acquisition belongs
// to the run: a test that called a handler directly would be asserting against a
// message, and which message a client sends first is exactly what the placement
// must not depend on. The script reaches EOF at once, so the run acquires,
// answers whatever the script carries, and returns.
//
// The responses come back in the order the wire carried them, which is not the
// order the script sent the requests in. A test asking about a particular
// request reads it with responseFor.
func runServer(t *testing.T, s *server, requests ...string) []rpcResponse {
	t.Helper()
	var out bytes.Buffer
	s.out = &out
	script := strings.Join(requests, "\n")
	if script != "" {
		script += "\n"
	}
	if err := s.run(context.Background(), strings.NewReader(script)); err != nil {
		t.Fatalf("run: %v", err)
	}
	return parseFrames(t, out.String())
}

// parseFrames parses the newline-delimited JSON-RPC frames a run wrote to its
// stdout.
func parseFrames(t *testing.T, stdout string) []rpcResponse {
	t.Helper()
	var responses []rpcResponse
	for _, line := range strings.Split(stdout, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var resp rpcResponse
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Fatalf("stdout line is not a valid JSON-RPC frame: %q: %v", line, err)
		}
		responses = append(responses, resp)
	}
	return responses
}

// frameCounter is a stdout that records how many frames have been written to it,
// so a test can ask what had already been answered at the moment the acquisition
// ran. Every frame is written from the run's own loop, so the count is read
// without synchronisation.
type frameCounter struct {
	frames int
	buf    bytes.Buffer
}

func (f *frameCounter) Write(p []byte) (int, error) {
	f.frames++
	return f.buf.Write(p)
}

// TestDocsBundleAcquiredAtTheStartOfTheRunAndServedAfterwards is the end-to-end
// proof that the documentation resources WORK, not merely that their conditions
// became reachable: a cold workspace, a run, and then a real page served inline.
//
// The order is the assertion. The read is attempted BEFORE the run too, and must
// fail — otherwise the fixture would already be warm and the trigger would be
// proven by nothing. The run carries no messages at all, which is the point: a
// client that sends nothing still leaves the cache warm behind it.
func TestDocsBundleAcquiredAtTheStartOfTheRunAndServedAfterwards(t *testing.T) {
	t.Parallel()
	s, ws := coldDocsWorkspace(t)

	// The passing control's opposite: cold, the resource is not found. Without
	// this the test below could pass against a workspace that was never cold.
	if _, rerr := readResource(t, s, "langref://entities/component"); rerr == nil {
		t.Fatal("a cold workspace must answer resource-not-found before the run")
	} else if rerr.Code != codeResourceNotFound {
		t.Fatalf("cold read: got code %d, want resource-not-found %d", rerr.Code, codeResourceNotFound)
	}

	if resps := runServer(t, s); len(resps) != 0 {
		t.Fatalf("a run carrying no messages answered %d of them", len(resps))
	}

	// The cache the run filled is the one the read reads.
	tw, err := s.resolveToolWorkspace("", nil)
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}
	if cached, cerr := docscache.Cached(tw.root, tw.cacheDir, docsTestRef, docsTestVersion); cerr != nil || !cached {
		t.Fatalf("the run did not populate the bundle cache under %s (%v)", ws, cerr)
	}

	rr, rerr := readResource(t, s, "langref://entities/component")
	if rerr != nil {
		t.Fatalf("resources/read after the run: %v", rerr)
	}
	if len(rr.Contents) != 1 {
		t.Fatalf("got %d content blocks, want 1", len(rr.Contents))
	}
	if got := rr.Contents[0].Text; got != langrefBody {
		t.Errorf("served page text = %q; want %q", got, langrefBody)
	}
	if got := rr.Contents[0].MimeType; got != "text/markdown" {
		t.Errorf("served page media type = %q; want text/markdown", got)
	}

	// The authoring family reads from the same acquired bundle, so one
	// acquisition serves both resource schemes.
	ar, rerr := readResource(t, s, "authoring://getting-started")
	if rerr != nil {
		t.Fatalf("resources/read authoring after the run: %v", rerr)
	}
	if got := ar.Contents[0].Text; got != authoringBody {
		t.Errorf("served authoring text = %q; want %q", got, authoringBody)
	}
}

// TestDocsBundleAcquiredBeforeAMessageThatIsNotInitialize is the placement's own
// witness: the run's first and only message is one that reaches no handshake at
// all, and the acquisition has already run by the time it is answered.
//
// The ordering is asserted where it happens rather than after the fact — the
// target seam records how many frames stdout had taken when it was reached, so a
// count of zero says the acquisition preceded the answer rather than merely
// having occurred somewhere inside the run.
func TestDocsBundleAcquiredBeforeAMessageThatIsNotInitialize(t *testing.T) {
	t.Parallel()
	s, _ := coldDocsWorkspace(t)

	var out frameCounter
	s.out = &out

	framesAtAcquisition := -1
	inner := s.docsTarget
	s.docsTarget = func(tw *toolWorkspace, ref, version string, warn func(string)) (oras.ReadOnlyTarget, docscache.VerifyAtPull, error) {
		framesAtAcquisition = out.frames
		return inner(tw, ref, version, warn)
	}

	if err := s.run(context.Background(),
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n")); err != nil {
		t.Fatalf("run: %v", err)
	}

	if framesAtAcquisition < 0 {
		t.Fatal("a run whose only message is not initialize never attempted the acquisition")
	}
	if framesAtAcquisition != 0 {
		t.Errorf("the acquisition ran after %d frames had been answered; it must precede the first",
			framesAtAcquisition)
	}

	resps := parseFrames(t, out.buf.String())
	if len(resps) != 1 {
		t.Fatalf("want 1 response, got %d", len(resps))
	}
	if resps[0].Error != nil {
		t.Fatalf("ping returned an error envelope: %+v", resps[0].Error)
	}

	tw, err := s.resolveToolWorkspace("", nil)
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}
	if cached, cerr := docscache.Cached(tw.root, tw.cacheDir, docsTestRef, docsTestVersion); cerr != nil || !cached {
		t.Errorf("the bundle cache is empty after a run that attempted the acquisition (%v)", cerr)
	}
}

// TestDocsBundleAcquiredBeforeAFirstMessageInThePerRequestRevision is the same
// witness in the other revision, and it is the one that could not be written
// while the server spoke only one: a client speaking the per-request revision
// sends no handshake at all, so nothing about its first message could ever have
// triggered the acquisition.
//
// The first and only message is a tools/list stating its own revision. As with
// the sibling above, the ordering is asserted where it happens — the target
// seam records how many frames stdout had taken when it was reached — so a
// count of zero says the acquisition preceded the answer rather than merely
// having occurred somewhere inside the run.
func TestDocsBundleAcquiredBeforeAFirstMessageInThePerRequestRevision(t *testing.T) {
	t.Parallel()
	s, _ := coldDocsWorkspace(t)

	var out frameCounter
	s.out = &out

	framesAtAcquisition := -1
	inner := s.docsTarget
	s.docsTarget = func(tw *toolWorkspace, ref, version string, warn func(string)) (oras.ReadOnlyTarget, docscache.VerifyAtPull, error) {
		framesAtAcquisition = out.frames
		return inner(tw, ref, version, warn)
	}

	if err := s.run(context.Background(),
		strings.NewReader(perRequestRequest(1, "tools/list", "")+"\n")); err != nil {
		t.Fatalf("run: %v", err)
	}

	if framesAtAcquisition < 0 {
		t.Fatal("a run whose only message states its own revision never attempted the acquisition")
	}
	if framesAtAcquisition != 0 {
		t.Errorf("the acquisition ran after %d frames had been answered; it must precede the first",
			framesAtAcquisition)
	}

	resps := parseFrames(t, out.buf.String())
	if len(resps) != 1 {
		t.Fatalf("want 1 response, got %d", len(resps))
	}
	if resps[0].Error != nil {
		t.Fatalf("tools/list returned an error envelope: %+v", resps[0].Error)
	}

	tw, err := s.resolveToolWorkspace("", nil)
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}
	if cached, cerr := docscache.Cached(tw.root, tw.cacheDir, docsTestRef, docsTestVersion); cerr != nil || !cached {
		t.Errorf("the bundle cache is empty after a run that attempted the acquisition (%v)", cerr)
	}
}

// TestDocsBundleAcquisitionRunsOncePerSession pins the trigger to one attempt.
// A cold cache and an unreachable registry look identical from the trigger, so a
// second attempt would pay the connect timeout again with nothing changed.
func TestDocsBundleAcquisitionRunsOncePerSession(t *testing.T) {
	t.Parallel()
	s, _ := coldDocsWorkspace(t)

	calls := 0
	inner := s.docsTarget
	s.docsTarget = func(tw *toolWorkspace, ref, version string, warn func(string)) (oras.ReadOnlyTarget, docscache.VerifyAtPull, error) {
		calls++
		return inner(tw, ref, version, warn)
	}

	for range 3 {
		runServer(t, s)
	}
	if calls != 1 {
		t.Errorf("the bundle target was resolved %d times; the trigger must run once per session", calls)
	}
}

// TestDocsBundleAcquisitionFailureNeverFailsTheRun pins the degradation: an
// acquisition that cannot complete leaves the session usable and the
// documentation resources answering not-found, which is the behaviour a
// disconnected or unpublished installation gets.
func TestDocsBundleAcquisitionFailureNeverFailsTheRun(t *testing.T) {
	t.Parallel()
	s, _ := coldDocsWorkspace(t)
	var logged bytes.Buffer
	s.logw = &logged
	s.docsTarget = func(*toolWorkspace, string, string, func(string)) (oras.ReadOnlyTarget, docscache.VerifyAtPull, error) {
		// An empty store resolves nothing, which is what an unpublished bundle
		// looks like from here.
		return memory.New(), nil, nil
	}

	resps := runServer(t, s,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"probe"}}}`)
	if len(resps) != 1 {
		t.Fatalf("want 1 response, got %d", len(resps))
	}
	if resps[0].Error != nil {
		t.Fatalf("a failed acquisition must not fail the session; got %+v", resps[0].Error)
	}
	var res initializeResult
	if err := json.Unmarshal(resps[0].Result, &res); err != nil {
		t.Fatalf("unmarshal initialize result: %v", err)
	}
	if res.ServerInfo.Name != serverName {
		t.Errorf("serverInfo.name = %q, want %q", res.ServerInfo.Name, serverName)
	}
	if !strings.Contains(logged.String(), "not acquired") {
		t.Errorf("the reason must reach the log stream; got %q", logged.String())
	}
	if _, rerr := readResource(t, s, "langref://entities/component"); rerr == nil {
		t.Error("with no bundle acquired the documentation resource must answer not-found")
	}
}

// TestDocsBundleAcquisitionIsSkippedWhenAlreadyCached pins the "zero per-request
// roundtrip" half: a workspace whose cache is already warm resolves no target at
// all, so a warm session never touches a registry.
func TestDocsBundleAcquisitionIsSkippedWhenAlreadyCached(t *testing.T) {
	t.Parallel()
	s := seedDocsWorkspace(t)
	resolved := false
	s.docsTarget = func(*toolWorkspace, string, string, func(string)) (oras.ReadOnlyTarget, docscache.VerifyAtPull, error) {
		resolved = true
		return memory.New(), nil, nil
	}
	runServer(t, s)
	if resolved {
		t.Error("a warm cache must not resolve a bundle target")
	}
}

// TestReadDocsResourceNeverResolvesATarget pins the offline read contract from
// the caller's side: whatever the read does, it does not acquire. ReadPage takes
// no target and structurally cannot network; this covers the layer above it,
// where a fetch-on-miss could be added without changing ReadPage at all.
func TestReadDocsResourceNeverResolvesATarget(t *testing.T) {
	t.Parallel()
	s, _ := coldDocsWorkspace(t)
	s.docsTarget = func(*toolWorkspace, string, string, func(string)) (oras.ReadOnlyTarget, docscache.VerifyAtPull, error) {
		t.Error("resources/read must not resolve a bundle target")
		return memory.New(), nil, nil
	}
	for _, uri := range []string{"langref://entities/component", "authoring://getting-started"} {
		if _, rerr := readResource(t, s, uri); rerr == nil {
			t.Errorf("%s: expected resource-not-found on a cold cache", uri)
		}
	}
}

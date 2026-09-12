// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"bytes"
	"context"
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

// TestDocsBundleAcquiredAtTheHandshakeAndServedAfterwards is the end-to-end proof
// that the documentation resources WORK, not merely that their conditions became
// reachable: a cold workspace, a handshake, and then a real page served inline.
//
// The order is the assertion. The read is attempted BEFORE the handshake too, and
// must fail — otherwise the fixture would already be warm and the trigger would
// be proven by nothing.
func TestDocsBundleAcquiredAtTheHandshakeAndServedAfterwards(t *testing.T) {
	t.Parallel()
	s, ws := coldDocsWorkspace(t)

	// The passing control's opposite: cold, the resource is not found. Without
	// this the test below could pass against a workspace that was never cold.
	if _, rerr := readResource(t, s, "langref://entities/component"); rerr == nil {
		t.Fatal("a cold workspace must answer resource-not-found before the handshake")
	} else if rerr.Code != codeResourceNotFound {
		t.Fatalf("cold read: got code %d, want resource-not-found %d", rerr.Code, codeResourceNotFound)
	}

	if _, rerr := s.handleInitialize(context.Background(), nil); rerr != nil {
		t.Fatalf("initialize: %v", rerr)
	}

	// The cache the handshake filled is the one the read reads.
	tw, err := s.resolveToolWorkspace("", nil)
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}
	if !docscache.Cached(tw.root, tw.cacheDir, docsTestRef, docsTestVersion) {
		t.Fatalf("the handshake did not populate the bundle cache under %s", ws)
	}

	rr, rerr := readResource(t, s, "langref://entities/component")
	if rerr != nil {
		t.Fatalf("resources/read after the handshake: %v", rerr)
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
		t.Fatalf("resources/read authoring after the handshake: %v", rerr)
	}
	if got := ar.Contents[0].Text; got != authoringBody {
		t.Errorf("served authoring text = %q; want %q", got, authoringBody)
	}
}

// TestDocsBundleAcquisitionRunsOncePerSession pins the trigger to one attempt.
// A cold cache and an unreachable registry look identical from the trigger, so a
// per-handshake retry would pay the connect timeout again with nothing changed.
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
		if _, rerr := s.handleInitialize(context.Background(), nil); rerr != nil {
			t.Fatalf("initialize: %v", rerr)
		}
	}
	if calls != 1 {
		t.Errorf("the bundle target was resolved %d times; the trigger must run once per session", calls)
	}
}

// TestDocsBundleAcquisitionFailureNeverFailsTheHandshake pins the degradation:
// an acquisition that cannot complete leaves the session usable and the
// documentation resources answering not-found, which is the behaviour a
// disconnected or unpublished installation gets.
func TestDocsBundleAcquisitionFailureNeverFailsTheHandshake(t *testing.T) {
	t.Parallel()
	s, _ := coldDocsWorkspace(t)
	var logged bytes.Buffer
	s.logw = &logged
	s.docsTarget = func(*toolWorkspace, string, string, func(string)) (oras.ReadOnlyTarget, docscache.VerifyAtPull, error) {
		// An empty store resolves nothing, which is what an unpublished bundle
		// looks like from here.
		return memory.New(), nil, nil
	}

	res, rerr := s.handleInitialize(context.Background(), nil)
	if rerr != nil {
		t.Fatalf("a failed acquisition must not fail the handshake; got %v", rerr)
	}
	if _, ok := res.(initializeResult); !ok {
		t.Fatalf("initialize returned %T, want initializeResult", res)
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
	if _, rerr := s.handleInitialize(context.Background(), nil); rerr != nil {
		t.Fatalf("initialize: %v", rerr)
	}
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

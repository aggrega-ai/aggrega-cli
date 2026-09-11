// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/publishops"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing/signingtest"
)

// The publication proofs assert what reached the REGISTRY, never the returned
// error alone. A withheld reply looks identical whether the publication was made
// or abandoned, so each test drives the operation against an in-memory OCI store
// and then asks that store what it holds.
//
// That observable is the one worth having here, because publication is the least
// reversible thing in this codebase: published bytes are frozen and the registry
// offers no overwrite path, so an operation that could not observe a withdrawal
// would leave content published that nobody can retract.
//
// Every fixture store is a memory.Store created per test and every workspace is a
// temporary one, so nothing here reaches a real registry or the developer's own
// configuration.
//
// No test carries a timing bound. The context is cancelled BEFORE the call, so
// where the stop lands is fixed by the interruption point rather than by machine
// speed.

// withdrawn returns a context already carrying a withdrawal, so the first
// interruption point an operation reaches observes it.
func withdrawn(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	return ctx
}

// publishedTags reports whether the store holds the given tag. It is the
// observable: a tag the store cannot resolve is content that was never published.
func publishedTag(t *testing.T, store *memory.Store, tag string) bool {
	t.Helper()
	_, exists, err := oci.Resolve(t.Context(), store, tag)
	if err != nil {
		return false
	}
	return exists
}

// TestAWithdrawnEntityPublicationPublishesNothing proves the stop reaches the
// work: the tag a completed publication lands is absent from the registry after
// a withdrawn one.
func TestAWithdrawnEntityPublicationPublishesNothing(t *testing.T) {
	const tag = "1.0.0"

	fixture := func(t *testing.T) (pushConfig, *memory.Store) {
		t.Helper()
		root := t.TempDir()
		entityPath := filepath.Join(root, tag, "entity.yaml")
		writeEntityFixture(t, entityPath, guardComponentBody)
		store := memory.New()
		return pushConfig{
			Workspace:  resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
			EntityPath: entityPath,
			Registry:   "example.com",
			PushTarget: store,
			Out:        &bytes.Buffer{},
		}, store
	}

	t.Run("a withdrawn publication lands no tag", func(t *testing.T) {
		cfg, store := fixture(t)

		res, err := publishops.Push(withdrawn(t), cfg)

		// t.Error rather than t.Fatal: the registry assertion below is the
		// load-bearing half, and a Fatal here would skip it on exactly the failure
		// it exists to characterise.
		if err == nil {
			t.Error("a withdrawn publication returned no error, so nothing distinguishes it from one that landed")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("the publication refused with %v; want an error carrying context.Canceled", err)
		}
		if res.Digest != "" {
			t.Errorf("a withdrawn publication reported a published digest %q", res.Digest)
		}
		if publishedTag(t, store, tag) {
			t.Errorf("the withdrawn publication landed tag %q at the registry anyway.\n"+
				"The interruption point is not ahead of the first byte, so frozen content was published after the caller gave up.", tag)
		}
	})

	t.Run("control: an uncancelled publication does land it", func(t *testing.T) {
		cfg, store := fixture(t)

		if _, err := publishops.Push(t.Context(), cfg); err != nil {
			t.Fatalf("the control publication refused, so it establishes no baseline: %v", err)
		}
		if !publishedTag(t, store, tag) {
			t.Fatalf("the control publication landed no tag %q, so its absence above is not evidence of a stop", tag)
		}
	})
}

// TestAWithdrawnArtifactPublicationPublishesNothing is the same proof for the
// standalone-artifact path, which builds its own reference and reaches the
// registry through three separate calls rather than one.
func TestAWithdrawnArtifactPublicationPublishesNothing(t *testing.T) {
	const tag = "2.0.0"

	fixture := func(t *testing.T) (pushArtifactConfig, *memory.Store) {
		t.Helper()
		root := t.TempDir()
		filePath := filepath.Join(root, "bundle.tgz")
		writeEntityFixture(t, filePath, "artifact-bytes")
		store := memory.New()
		return pushArtifactConfig{
			Workspace:  resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
			FilePath:   filePath,
			Name:       "docs/aggrega-docs",
			Tag:        tag,
			Registry:   "example.com",
			PushTarget: store,
			Out:        &bytes.Buffer{},
		}, store
	}

	t.Run("a withdrawn artifact publication lands no tag", func(t *testing.T) {
		cfg, store := fixture(t)

		res, err := publishops.PushArtifact(withdrawn(t), cfg)

		if err == nil {
			t.Error("a withdrawn artifact publication returned no error")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("the artifact publication refused with %v; want an error carrying context.Canceled", err)
		}
		if res.Digest != "" {
			t.Errorf("a withdrawn artifact publication reported a published digest %q", res.Digest)
		}
		if publishedTag(t, store, tag) {
			t.Errorf("the withdrawn artifact publication landed tag %q at the registry anyway", tag)
		}
	})

	t.Run("control: an uncancelled artifact publication does land it", func(t *testing.T) {
		cfg, store := fixture(t)

		if _, err := publishops.PushArtifact(t.Context(), cfg); err != nil {
			t.Fatalf("the control artifact publication refused, so it establishes no baseline: %v", err)
		}
		if !publishedTag(t, store, tag) {
			t.Fatalf("the control artifact publication landed no tag %q, so its absence above is not evidence of a stop", tag)
		}
	})
}

// TestAWithdrawnSigningBatchSignsNothing proves the stop reaches the per-
// reference loop.
//
// The observable is how many of the batch's references were signed — work done,
// counted off the batch's own record. A signature is attached at the registry as
// a sibling manifest, so a reference signed after the caller withdrew is content
// published with nobody waiting for it.
func TestAWithdrawnSigningBatchSignsNothing(t *testing.T) {
	tags := []string{"1.0.0", "1.1.0", "1.2.0"}

	fixture := func(t *testing.T) signConfig {
		t.Helper()
		store := memory.New()
		var refs []string
		subjects := map[string]string{}
		for i, tag := range tags {
			body := fmt.Sprintf("published content %d", i)
			if _, err := oci.Push(t.Context(), store, tag, oci.MediaTypeComponent, []byte(body), nil); err != nil {
				t.Fatalf("seed subject %s: %v", tag, err)
			}
			ref := fmt.Sprintf("hub.aggrega.ai/component/core/thing-%d:%s", i, tag)
			refs = append(refs, ref)
			subjects[ref] = tag
		}
		return signConfig{
			Workspace:   resolveTempWorkspace(t, tempWorkspaceOptions{}),
			References:  refs,
			SubjectRefs: subjects,
			Target:      store,
			Signer:      &signingtest.FakeSigner{},
			IDToken:     "supplied-token",
			EnvLookup:   func(string) string { return "" },
			Out:         &bytes.Buffer{},
		}
	}

	t.Run("a withdrawn batch signs none of its references", func(t *testing.T) {
		res, err := publishops.Sign(withdrawn(t), fixture(t))

		if err == nil {
			t.Error("a withdrawn signing batch returned no error")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("the batch refused with %v; want an error carrying context.Canceled", err)
		}
		if len(res.Signed) != 0 {
			t.Errorf("the withdrawn batch signed %d of %d reference(s): %+v\n"+
				"The stop did not reach the loop, so signatures were attached at the registry after the caller gave up.",
				len(res.Signed), len(tags), res.Signed)
		}
	})

	t.Run("control: an uncancelled batch signs all of them", func(t *testing.T) {
		res, err := publishops.Sign(t.Context(), fixture(t))
		if err != nil {
			t.Fatalf("the control batch refused, so it establishes no baseline: %v", err)
		}
		if len(res.Signed) != len(tags) {
			t.Fatalf("the control batch signed %d of %d reference(s), so the zero above is not evidence of a stop",
				len(res.Signed), len(tags))
		}
	})
}

// TestAWithdrawnDeprecationAttachesNoMarker proves the interruption point sits
// ahead of either arm of the deprecation.
//
// The observable is the marker count the operation records. A deprecation marker
// is published content that every consumer of the artifact then reads, so one
// attached after the caller withdrew is a withdrawal announced to the world
// because a request was interrupted.
func TestAWithdrawnDeprecationAttachesNoMarker(t *testing.T) {
	fixture := func(t *testing.T) deprecateConfig {
		t.Helper()
		return deprecateConfig{
			Workspace:  resolveTempWorkspace(t, tempWorkspaceOptions{}),
			Reference:  "reg.test/component/core/legacy:1.0.0",
			Severity:   "warning",
			Reason:     "superseded",
			Target:     freshDeprecateSubject(t),
			SubjectRef: "1.0.0",
			Signer:     keyedTestSigner(t),
			Out:        &bytes.Buffer{},
			IsTTY:      func() bool { return false },
		}
	}

	t.Run("a withdrawn deprecation attaches nothing", func(t *testing.T) {
		res, err := publishops.Deprecate(withdrawn(t), fixture(t))

		if err == nil {
			t.Error("a withdrawn deprecation returned no error")
		}
		if !errors.Is(err, context.Canceled) {
			t.Errorf("the deprecation refused with %v; want an error carrying context.Canceled", err)
		}
		if len(res.Marked) != 0 {
			t.Errorf("the withdrawn deprecation attached %d marker(s): %+v\n"+
				"A marker is published content, so one attached here is a retraction announced after the caller gave up.",
				len(res.Marked), res.Marked)
		}
	})

	t.Run("control: an uncancelled deprecation does attach one", func(t *testing.T) {
		res, err := publishops.Deprecate(t.Context(), fixture(t))
		if err != nil {
			t.Fatalf("the control deprecation refused, so it establishes no baseline: %v", err)
		}
		if len(res.Marked) != 1 {
			t.Fatalf("the control deprecation attached %d marker(s); want 1, otherwise the zero above is not evidence of a stop",
				len(res.Marked))
		}
	})
}

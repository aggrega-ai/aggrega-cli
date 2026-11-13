// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package oci_test

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// memoStep is one read a row makes: under which context, of what, and how many
// requests it should send the registry.
type memoStep struct {
	// under names the context: "plain" holds no memo; "memo" is the row's memo;
	// "ahead" reads ahead into it; "ended" is the row's memo after its context ended;
	// "another" is a second memo, standing for a second run in the same process.
	under string
	// read is "pull" (the published tag), "tags", or "missing" (a pull of a tag the
	// registry does not hold).
	read string
	// requests is how many requests the step should send.
	requests int64
	// fails says the read should return an error.
	fails bool
}

// TestARegistryReadMemoHandsEachKeptAnswerToOneRead drives the memo of registry
// reads through sequences of reads and counts the requests each sends: a read
// ahead keeps its answer, one read takes it and the next asks the registry again;
// two reads ahead of one thing ask once; the tag listing and the pull of one
// reference are kept apart; a failure is kept like an answer; a read under a
// context that has ended takes nothing; and a memo is its run's own, so a second
// run in the same process reads the registry again.
//
// Planted defects that make it fail: drop the delete in registryReadMemo.take, and
// "a kept answer is taken once" sends no request on its third read; key the memo
// by reference and tag without the shape, and "the shape is part of the key" takes
// the listing's answer for the pull; hold one memo for the process instead of one
// per WithRegistryReadMemo, and "a second run reads the registry again" sends none.
func TestARegistryReadMemoHandsEachKeptAnswerToOneRead(t *testing.T) {
	cases := []struct {
		name  string
		steps []memoStep
	}{
		{"with no memo every read asks the registry", []memoStep{
			{under: "plain", read: "pull", requests: 2},
			{under: "plain", read: "pull", requests: 2},
		}},
		{"a kept answer is taken once", []memoStep{
			{under: "ahead", read: "pull", requests: 2},
			{under: "memo", read: "pull", requests: 0},
			{under: "memo", read: "pull", requests: 2},
		}},
		{"two reads ahead of one thing ask once", []memoStep{
			{under: "ahead", read: "tags", requests: 1},
			{under: "ahead", read: "tags", requests: 0},
			{under: "memo", read: "tags", requests: 0},
			{under: "memo", read: "tags", requests: 1},
		}},
		{"the shape is part of the key", []memoStep{
			{under: "ahead", read: "tags", requests: 1},
			{under: "memo", read: "pull", requests: 2},
			{under: "memo", read: "tags", requests: 0},
		}},
		{"a failure is kept like an answer", []memoStep{
			{under: "ahead", read: "missing", requests: 1, fails: true},
			{under: "memo", read: "missing", requests: 0, fails: true},
		}},
		{"a read under an ended context takes nothing", []memoStep{
			{under: "ahead", read: "pull", requests: 2},
			{under: "ended", read: "pull", requests: 0, fails: true},
			{under: "memo", read: "pull", requests: 0},
		}},
		{"a second run reads the registry again", []memoStep{
			{under: "ahead", read: "pull", requests: 2},
			{under: "another", read: "pull", requests: 2},
			{under: "memo", read: "pull", requests: 0},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int64
			inner := registry.New(registry.Logger(log.New(io.Discard, "", 0)))
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				requests.Add(1)
				inner.ServeHTTP(w, req)
			}))
			t.Cleanup(srv.Close)
			reference := strings.TrimPrefix(srv.URL, "http://") + "/component/demo/memo"
			repo, err := oci.NewRemoteRepository(reference, &http.Client{})
			if err != nil {
				t.Fatalf("NewRemoteRepository: %v", err)
			}
			body := []byte("apiVersion: aggrega.ai/v1beta1\nkind: Component\nmetadata:\n  name: demo/memo\n")
			if _, err := oci.Push(context.Background(), repo, "1.0.0", oci.MediaTypeComponent, body, nil); err != nil {
				t.Fatalf("Push: %v", err)
			}

			memo := oci.WithRegistryReadMemo(context.Background())
			ended, end := context.WithCancel(memo)
			end()
			contexts := map[string]context.Context{
				"plain":   context.Background(),
				"memo":    memo,
				"ahead":   oci.PrefetchingRegistryReads(memo),
				"ended":   ended,
				"another": oci.WithRegistryReadMemo(context.Background()),
			}
			for i, step := range tc.steps {
				ctx := contexts[step.under]
				before := requests.Load()
				var readErr error
				switch step.read {
				case "pull":
					_, readErr = oci.MemoizedPull(ctx, reference, repo, "1.0.0")
				case "missing":
					_, readErr = oci.MemoizedPull(ctx, reference, repo, "9.9.9")
				case "tags":
					_, readErr = oci.MemoizedListTags(ctx, reference, repo)
				}
				if got := requests.Load() - before; got != step.requests {
					t.Errorf("step %d (%s %s): sent %d requests; want %d", i+1, step.under, step.read, got, step.requests)
				}
				if (readErr != nil) != step.fails {
					t.Errorf("step %d (%s %s): error = %v; want failure %v", i+1, step.under, step.read, readErr, step.fails)
				}
			}
		})
	}
}

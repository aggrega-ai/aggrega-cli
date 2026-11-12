// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package registryops

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
)

// endlessListing is a registry whose repository listing never ends: every page
// names one repository it has not named before and links to a next page. Each
// page takes pause to arrive. It records the wait on an answer the walk's
// requests were made under.
type endlessListing struct {
	pause      time.Duration
	answerWait time.Duration
}

func (l *endlessListing) Repositories(ctx context.Context, _ string, fn func(repos []string) error) error {
	l.answerWait = egress.AnswerWaitFor(ctx)
	for page := 0; ; page++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(l.pause):
		}
		if err := fn([]string{fmt.Sprintf("plugin/cli/r%06d", page)}); err != nil {
			return err
		}
	}
}

// TestAListingThatDoesNotEndIsStoppedByTheWalksBound walks a listing that never
// ends under a short bound and reads the refusal: it names the bound and how far
// the listing got, it yields no repository, and the walk's requests waited on
// their answers for as long as the walk may run.
func TestAListingThatDoesNotEndIsStoppedByTheWalksBound(t *testing.T) {
	t.Parallel()
	const bound = 300 * time.Millisecond
	listing := &endlessListing{pause: 20 * time.Millisecond}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second) // liveness ceiling, not a budget
	defer cancel()

	repos, err := enumerateRepositoriesWithin(ctx, listing, "", CatalogScope, bound)
	if !errors.Is(err, ErrRepositoryListingTooLong()) {
		t.Fatalf("want the listing-too-long refusal, got %v (repositories %v)", err, repos)
	}
	if repos != nil {
		t.Errorf("a walk stopped part way yields no repository; got %d", len(repos))
	}
	if !strings.Contains(err.Error(), "stopped after 300ms") || !strings.Contains(err.Error(), "repositories named so far: ") {
		t.Errorf("the refusal must name the bound and how far the listing got; got %q", err.Error())
	}
	if listing.answerWait != bound {
		t.Errorf("the walk's requests waited %s on an answer; want the walk's own bound, %s", listing.answerWait, bound)
	}
}

// TestAWalkItsCallerStoppedReportsTheStopNotTheBound pins that a caller's own
// withdrawal is reported as the stop it is, never as the walk running too long.
func TestAWalkItsCallerStoppedReportsTheStopNotTheBound(t *testing.T) {
	t.Parallel()
	listing := &endlessListing{pause: 20 * time.Millisecond}
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(100*time.Millisecond, cancel)

	_, err := enumerateRepositoriesWithin(ctx, listing, "", CatalogScope, time.Minute)
	if err == nil || errors.Is(err, ErrRepositoryListingTooLong()) {
		t.Fatalf("want the caller's stop, got %v", err)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("the stop must stay matchable as the caller's withdrawal; got %v", err)
	}
}

// TestTheWalksBoundIsTheOneEveryCallerGets pins the bound the exported walk
// applies, so a change to it is a decision rather than an accident.
func TestTheWalksBoundIsTheOneEveryCallerGets(t *testing.T) {
	t.Parallel()
	if RepositoryListingDeadline != 5*time.Minute {
		t.Errorf("RepositoryListingDeadline = %s; the documented bound is 5m0s", RepositoryListingDeadline)
	}
}

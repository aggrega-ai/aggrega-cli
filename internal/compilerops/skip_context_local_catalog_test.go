// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compilerops

import (
	stderrors "errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// localCatalogNoteFixture stands in for the workspace's rendered account. The
// text is not the subject here — which failures carry it is — so it is a marker
// rather than a copy of the real sentence, which would make this test fail on a
// wording change it has no opinion about.
const localCatalogNoteFixture = "\n  note: the account of where local entities are read from"

// TestLocalCatalogNoteAttachesOnlyToAnUnresolvedReference is the guard that
// keeps the account on the failure it explains.
//
// 🔴 The rows that matter are the ones that do NOT carry it. Every failure below
// is raised at the same step of the same command, so "the step that raises it"
// cannot separate them — only the failure's own subject can. A reader whose
// Release does not match its kind's schema, told where entities are read from,
// goes looking through directories for a file that is already open in front of
// them.
//
// The reference-carrying decoration is absent from the table, and that is a
// BOUND rather than an omission: the type wraps its failure in an unexported
// field, so no package but its own can build one. Nor is it reached indirectly
// here — every walk failure observed while this was written carried the
// unresolvable-reference condition as well as the decoration, so the first row
// would have matched either way. What is pinned below is the condition; the
// decoration is matched alongside it because that type is raised for exactly one
// reference the walk could not resolve, whatever the lookup underneath it
// returned, and a guard that read only the condition would stop explaining a
// miss the moment a lookup failed for a different reason.
func TestLocalCatalogNoteAttachesOnlyToAnUnresolvedReference(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "a reference that names an entity nothing resolved",
			err:  fmt.Errorf("closure compute: %w", closure.ErrUnresolvableURI()),
			want: true,
		},
		{
			name: "the same condition several frames down",
			err:  fmt.Errorf("validate: %w", fmt.Errorf("closure compute: %w", closure.ErrUnresolvableURI())),
			want: true,
		},
		{
			name: "a document that does not match its own kind's schema",
			err:  fmt.Errorf("document envelope validation failed: %w", envelope.ErrEntityEnvelope()),
			want: false,
		},
		{
			name: "a version-selection conflict among the requirements the walk accumulated",
			err:  fmt.Errorf("closure compute: %w", closure.ErrMVSConflict()),
			want: false,
		},
		{
			name: "a failure carrying no registered condition at all",
			err:  stderrors.New("the render stopped"),
			want: false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := AnnotateSkipContext(tc.err, nil, localCatalogNoteFixture)
			carries := strings.Contains(got.Error(), localCatalogNoteFixture)
			if carries != tc.want {
				t.Fatalf("the account %s this failure; want it %s\n%s",
					carriedOrNot(carries), carriedOrNot(tc.want), got.Error())
			}
			if tc.want {
				return
			}
			// A failure the account does not explain is handed back UNTOUCHED, not
			// merely undecorated: a decoration with an empty note would still change
			// which frame a formatter renders from.
			if got != tc.err {
				t.Errorf("the failure was wrapped even though nothing was added to it: %T", got)
			}
		})
	}
}

// carriedOrNot renders the two verdicts so the failure message reads as a
// sentence rather than as two booleans.
func carriedOrNot(b bool) string {
	if b {
		return "reached"
	}
	return "did not reach"
}

// TestLocalCatalogNoteAndRefusalsBothTravel pins that the two notes are
// independent. They explain different things — a file that was read and
// rejected, and which directories were read at all — and a workspace can be in
// both states at once, so one arriving must not displace the other.
func TestLocalCatalogNoteAndRefusalsBothTravel(t *testing.T) {
	t.Parallel()
	failure := fmt.Errorf("closure compute: %w", closure.ErrUnresolvableURI())
	refused := catalog.SkippedEntity{
		Path: "/cat/recipes/helm/server.yaml",
		Err:  stderrors.New("the constraint the author has to fix"),
	}

	got := AnnotateSkipContext(failure, []catalog.SkippedEntity{refused}, localCatalogNoteFixture).Error()
	for _, want := range []string{
		"catalog files skipped at load",
		"the constraint the author has to fix",
		localCatalogNoteFixture,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the report does not carry %q:\n%s", want, got)
		}
	}
	// The account comes last, because the refusals are about documents this
	// workspace DID read and the account is about where it read them from.
	if i, j := strings.Index(got, "catalog files skipped at load"), strings.Index(got, localCatalogNoteFixture); i > j {
		t.Errorf("the account precedes the refusals; the refusals are the nearer cause:\n%s", got)
	}
}

// TestNoNoteAndNoRefusalsLeavesTheFailureAlone pins the no-op. A caller whose
// workspace declared it reads no local catalog, whose load refused nothing, gets
// back exactly what it passed in — not a decoration carrying nothing, which
// would still change which frame a formatter renders from.
func TestNoNoteAndNoRefusalsLeavesTheFailureAlone(t *testing.T) {
	t.Parallel()
	failure := fmt.Errorf("closure compute: %w", closure.ErrUnresolvableURI())
	if got := AnnotateSkipContext(failure, nil, ""); got != failure {
		t.Errorf("AnnotateSkipContext returned %T; want the failure it was handed, unchanged", got)
	}
	if got := AnnotateSkipContext(nil, nil, localCatalogNoteFixture); got != nil {
		t.Errorf("AnnotateSkipContext(nil, ...) = %v; want nil — there is no failure to explain", got)
	}
}

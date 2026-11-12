// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package compilerops

import (
	"context"
	stderrors "errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// localCatalogNoteFixture stands in for the workspace's rendered account. The
// text is not the subject here — which failures carry it is — so it is a marker
// rather than a copy of the real sentence, which would make this test fail on a
// wording change it has no opinion about.
const localCatalogNoteFixture = "\n  note: the account of where local entities are read from"

// attachedFeatureRelease attaches one Feature, by the reference the rows below
// resolve, to a component of a Release. It is the smallest document whose walk
// records a reference with a kind hint and then asks a lookup for it.
const attachedFeatureRelease = `
apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: prod
  version: 1.0.0
spec:
  boundaries:
    primary:
      components:
        api:
          features:
            backup:
              type: local/nightly-backup:1.0.0
`

// answeringLookup answers every catalog-entity lookup with one fixed outcome, so
// the walk's failure is the one each row sets up and nothing else.
type answeringLookup struct {
	res closure.Resolution
	err error
}

func (l answeringLookup) ResolveCatalogEntity(context.Context, string, string) (closure.Resolution, error) {
	return l.res, l.err
}

func (answeringLookup) ResolveAggregate(context.Context, string) (closure.Resolution, error) {
	return closure.Resolution{NotFound: true}, nil
}

func (answeringLookup) ResolveLibrary(context.Context, closure.LibraryAxis, string) (closure.LibraryResolution, error) {
	return closure.LibraryResolution{Resolution: closure.Resolution{NotFound: true}}, nil
}

func (answeringLookup) ResolvePlugin(context.Context, string, string) (closure.Resolution, error) {
	return closure.Resolution{NotFound: true}, nil
}

// walkFailure drives the real closure walk over attachedFeatureRelease with a
// lookup answering as given, and returns the failure the walk raised. The
// reference-carrying decoration the walk wraps its failures in has no exported
// constructor, so walking is the one way a test holds the failure a command
// holds.
func walkFailure(t *testing.T, lookup closure.Lookup) error {
	t.Helper()
	var rel types.Release
	if err := yaml.Unmarshal([]byte(attachedFeatureRelease), &rel); err != nil {
		t.Fatalf("unmarshal the fixture Release: %v", err)
	}
	_, err := closure.ComputeClosure(context.Background(), &rel, nil, types.NormalizedAggregate{},
		workspaceconfig.Config{}, lookup)
	if err == nil {
		t.Fatal("the walk resolved the attached Feature; every row here is a failure")
	}
	var unresolved *closure.UnresolvedEntityError
	if !stderrors.As(err, &unresolved) || unresolved.URI != "local/nightly-backup:1.0.0" {
		t.Fatalf("the walk's failure does not carry the attached reference; got %v", err)
	}
	return err
}

// walkStageFailure is the failure ResolveClosure returns when the closure walk
// refuses with err: the walk's stage, carrying err's own text and err itself.
// The rows below that need a resolution failure without driving a walk start
// from it, so what they hand the decoration has the shape a command holds.
func walkStageFailure(err error) error {
	return &ClosureError{Stage: ClosureStageComputeClosure, msg: err.Error(), err: err}
}

// refusedFeature is the refusal record a tolerant load keeps for a Feature
// document it read and refused, declaring the identity given.
func refusedFeature(path, name, version string) catalog.SkippedEntity {
	return catalog.SkippedEntity{
		Path: path, Kind: "Feature", Name: name, Version: version,
		Err: fmt.Errorf("unmarshal Feature: %w", envelope.ErrEntityEnvelope()),
	}
}

// TestLocalCatalogNoteAttachesOnlyWhereItIsTheRemedy is the guard that keeps the
// account on the failure it explains: a reference that resolved to nothing,
// declared by no document the catalog read.
//
// 🔴 The rows that matter are the ones that do NOT carry it, and each is a
// different way of being wrong about it. The walk decorates EVERY lookup failure
// with the reference it was resolving, so a lookup that found the entity and
// could not accept it — its bytes no longer match their pin, its own files cannot
// be read — carries the same decoration a miss does; only the
// unresolvable-reference condition says nothing was found. And a reference that
// resolved to nothing because the document declaring it was read and refused
// needs that document fixed, not moved.
//
// The positive controls keep each negative row honest: the same walk with a miss
// and no refusal carries the account, and so does a miss beside a refusal of some
// other entity. A guard that withheld the account always would fail both.
func TestLocalCatalogNoteAttachesOnlyWhereItIsTheRemedy(t *testing.T) {
	t.Parallel()
	miss := answeringLookup{res: closure.Resolution{NotFound: true}}
	const refusedPath = "/cat/feature/local/nightly-backup/1.0.0/feature.yaml"
	cases := []struct {
		name    string
		err     func(t *testing.T) error
		skipped []catalog.SkippedEntity
		want    bool
	}{
		{
			name: "a reference every tier answered it holds nothing for",
			err:  func(t *testing.T) error { return walkFailure(t, miss) },
			want: true,
		},
		{
			name:    "the same miss beside a refused document declaring another entity",
			err:     func(t *testing.T) error { return walkFailure(t, miss) },
			skipped: []catalog.SkippedEntity{refusedFeature("/cat/feature/other.yaml", "local/other", "1.0.0")},
			want:    true,
		},
		{
			name:    "the same miss where the document declaring it was read and refused",
			err:     func(t *testing.T) error { return walkFailure(t, miss) },
			skipped: []catalog.SkippedEntity{refusedFeature(refusedPath, "local/nightly-backup", "1.0.0")},
			want:    false,
		},
		{
			name: "the same miss where two documents declaring it were read and refused",
			err:  func(t *testing.T) error { return walkFailure(t, miss) },
			skipped: []catalog.SkippedEntity{
				refusedFeature(refusedPath, "local/nightly-backup", "1.0.0"),
				refusedFeature("/cat/copy/feature.yaml", "local/nightly-backup", "1.0.0"),
			},
			want: false,
		},
		{
			name: "a lookup that found the entity and its bytes no longer match their pin",
			err: func(t *testing.T) error {
				return walkFailure(t, answeringLookup{err: fmt.Errorf(
					"%w: pinned entity %q: registry now serves other bytes", oci.ErrFetchIntegrity(), "local/nightly-backup")})
			},
			want: false,
		},
		{
			name: "a lookup that found the entity and could not read its files",
			err: func(t *testing.T) error {
				return walkFailure(t, answeringLookup{err: fmt.Errorf("read %s: %w", "/cat/chart/linked-dir",
					&fs.PathError{Op: "read", Path: "/cat/chart/linked-dir", Err: stderrors.New("is a directory")})})
			},
			want: false,
		},
		{
			name: "the bare condition, raised by a path that recorded no reference",
			err: func(*testing.T) error {
				return fmt.Errorf("validate: %w", walkStageFailure(closure.ErrUnresolvableURI()))
			},
			want: true,
		},
		{
			name: "a document that does not match its own kind's schema",
			err: func(*testing.T) error {
				return fmt.Errorf("document envelope validation failed: %w", envelope.ErrEntityEnvelope())
			},
			want: false,
		},
		{
			name: "a version-selection conflict among the requirements the walk accumulated",
			err:  func(*testing.T) error { return walkStageFailure(closure.ErrMVSConflict()) },
			want: false,
		},
		{
			name: "a failure carrying no registered condition at all",
			err:  func(*testing.T) error { return stderrors.New("the render stopped") },
			want: false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			failure := tc.err(t)
			got := AnnotateSkipContext(failure, tc.skipped, localCatalogNoteFixture)
			carries := strings.Contains(got.Error(), localCatalogNoteFixture)
			if carries != tc.want {
				t.Fatalf("the account %s this failure; want it %s\n%s",
					carriedOrNot(carries), carriedOrNot(tc.want), got.Error())
			}
			if tc.want || len(tc.skipped) > 0 {
				return
			}
			// A failure the account does not explain, with no refusal to list, is
			// handed back UNTOUCHED, not merely undecorated: a decoration with an
			// empty note would still change which frame a formatter renders from.
			if got != failure {
				t.Errorf("the failure was wrapped even though nothing was added to it: %T", got)
			}
		})
	}
}

// TestLocalCatalogAdviceIsTheOnePredicate holds the predicate a by-name lookup
// calls directly to the same answers the decoration reaches through it, over the
// reference as that lookup holds it: a name and the hint derived from the kind a
// document declares.
func TestLocalCatalogAdviceIsTheOnePredicate(t *testing.T) {
	t.Parallel()
	declaring := refusedFeature("/cat/feature.yaml", "local/nightly-backup", "1.0.0")
	cases := []struct {
		name    string
		note    string
		skipped []catalog.SkippedEntity
		uri     string
		kind    string
		want    string
	}{
		{"no refusal recorded", localCatalogNoteFixture, nil, "local/nightly-backup", "Feature", localCatalogNoteFixture},
		{"a refusal declaring the name", localCatalogNoteFixture, []catalog.SkippedEntity{declaring}, "local/nightly-backup", "Feature", ""},
		{"a refusal declaring the name, asked with its version", localCatalogNoteFixture, []catalog.SkippedEntity{declaring}, "local/nightly-backup:1.0.0", "Feature", ""},
		{"a refusal declaring it at another version", localCatalogNoteFixture, []catalog.SkippedEntity{declaring}, "local/nightly-backup:2.0.0", "Feature", localCatalogNoteFixture},
		{"a refusal declaring the name as another kind", localCatalogNoteFixture, []catalog.SkippedEntity{declaring}, "local/nightly-backup", "Component", localCatalogNoteFixture},
		{"a kind no refusal can be matched under", localCatalogNoteFixture, []catalog.SkippedEntity{declaring}, "local/nightly-backup", "Manifest", localCatalogNoteFixture},
		{"a workspace that declared it reads no local catalog", "", nil, "local/nightly-backup", "Feature", ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := LocalCatalogAdvice(tc.note, tc.skipped, tc.uri, KindHintFor(tc.kind)); got != tc.want {
				t.Errorf("LocalCatalogAdvice = %q; want %q", got, tc.want)
			}
		})
	}
}

// TestKindHintForMapsBackThroughTheCatalog pins that a derived hint is kept only
// where the catalog's own hint vocabulary maps it back to the kind, so the
// derivation can never hand the matcher a hint it reads as something else.
func TestKindHintForMapsBackThroughTheCatalog(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ kind, want string }{
		{"Component", "component"},
		{"Feature", "feature"},
		{"Recipe", "recipe"},
		{"FeatureRecipe", "feature_recipe"},
		{"Plugin", "plugin"},
		{"Aggregate", "aggregate"},
		{"Manifest", ""},
		{"Release", ""},
		{"SchemaLibrary", ""},
		{"", ""},
	} {
		if got := KindHintFor(tc.kind); got != tc.want {
			t.Errorf("KindHintFor(%q) = %q; want %q", tc.kind, got, tc.want)
		}
		if got := KindHintFor(tc.kind); got != "" && catalog.EntityKindForHint(got) != tc.kind {
			t.Errorf("KindHintFor(%q) = %q, which the catalog reads as %q", tc.kind, got, catalog.EntityKindForHint(got))
		}
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
	failure := walkStageFailure(closure.ErrUnresolvableURI())
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
	failure := walkStageFailure(closure.ErrUnresolvableURI())
	if got := AnnotateSkipContext(failure, nil, ""); got != failure {
		t.Errorf("AnnotateSkipContext returned %T; want the failure it was handed, unchanged", got)
	}
	if got := AnnotateSkipContext(nil, nil, localCatalogNoteFixture); got != nil {
		t.Errorf("AnnotateSkipContext(nil, ...) = %v; want nil — there is no failure to explain", got)
	}
}

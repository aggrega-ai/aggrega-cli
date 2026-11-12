// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package analyzer_test

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// TestAViolationIsFiledUnderTheDocumentItWasWrittenIn pins the category a
// violation converts to against the document its site recorded.
//
// The category answers which authoring surface the reader fixes, and the graph's
// rules report about both: a connection, or an attachment written on one, is drawn
// by the developer in the Aggregate or by the operator on the Release. So the
// category is the one the kind-to-surface mapping gives the document the violation
// was written in, exactly as a finding naming its document is filed when the
// composition is flattened. Each row moves one variable — the document — over one
// otherwise identical violation, and the expectation is read from that mapping
// rather than restated, so the row asserts the routing and not a copy of the table.
//
// The unrecorded row is the control for every rule whose site records no document:
// it keeps the developer's surface. The policy row pins that a verdict stays
// filed under the policy surface whatever a document field says.
func TestAViolationIsFiledUnderTheDocumentItWasWrittenIn(t *testing.T) {
	t.Parallel()

	surfaceOf := func(t *testing.T, document string) cerrors.Category {
		t.Helper()
		kind, named := constraint.AuthoringDocumentKind(document)
		if !named {
			t.Fatalf("the framework names no entity kind for the document %q, so the row asserts nothing", document)
		}
		category, covered := cerrors.ClassifyEntityKind(string(kind))
		if !covered {
			t.Fatalf("the mapping covers no surface for the kind %q, so the row asserts nothing", kind)
		}
		return category
	}

	for _, row := range []struct {
		name     string
		kind     analyzer.AnalysisErrorKind
		document string
		want     func(t *testing.T) cerrors.Category
	}{
		{
			name:     "written on the Release",
			kind:     analyzer.AnalysisErrorLocated,
			document: constraint.AuthoringDocumentRelease,
			want:     func(t *testing.T) cerrors.Category { return surfaceOf(t, constraint.AuthoringDocumentRelease) },
		},
		{
			name:     "written in the Aggregate",
			kind:     analyzer.AnalysisErrorLocated,
			document: constraint.AuthoringDocumentAggregate,
			want:     func(t *testing.T) cerrors.Category { return surfaceOf(t, constraint.AuthoringDocumentAggregate) },
		},
		{
			name: "written where the site recorded no document",
			kind: analyzer.AnalysisErrorLocated,
			want: func(*testing.T) cerrors.Category { return cerrors.DeveloperError },
		},
		{
			name:     "a policy verdict",
			kind:     analyzer.AnalysisErrorPolicy,
			document: constraint.AuthoringDocumentRelease,
			want:     func(*testing.T) cerrors.Category { return cerrors.PolicyError },
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			e := &analyzer.AnalysisError{
				Kind:       row.kind,
				Message:    "required attachment slot unfilled",
				OriginFile: "release.yaml",
				Field:      ".spec.boundaries.cluster.components.gateway.uses.models.gpt-c",
				Document:   row.document,
			}
			if got, want := e.ToCompilerError().Category(), row.want(t); got != want {
				t.Errorf("a violation %s converts under %q; want %q", row.name, got, want)
			}
		})
	}

	// The two documents land on different surfaces, which is what makes the rows
	// above able to fail: were the mapping to file both the same way, a conversion
	// ignoring the document would pass every row.
	if surfaceOf(t, constraint.AuthoringDocumentRelease) == surfaceOf(t, constraint.AuthoringDocumentAggregate) {
		t.Fatal("the Release and the Aggregate map to one surface, so no row here can tell a conversion " +
			"that reads the document from one that ignores it")
	}
}

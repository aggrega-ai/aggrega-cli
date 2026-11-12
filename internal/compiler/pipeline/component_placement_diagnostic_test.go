// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pipeline

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// The proofs in this file pin WHICH diagnostic a component the Release placed
// in no boundary receives, against a component type whose sizing contract
// declares a required key.
//
// Two rules can speak about that document and only one of them is useful to its
// author. The sizing contract is measured before the composition is flattened,
// and the sizing a Release never wrote is empty — the same empty a Release
// that placed the component and wrote no sizing produces. A rule reading that
// content alone cannot tell the two apart, so it would report a missing sizing
// key for a component the Release never placed and stop the build before the
// placement rule was ever asked. The author would then be told to add a key
// under an entry that is not there.
//
// What decides it is authorship: the sizing rule measures the components the
// Release wrote entries against, and a component it wrote no entry against is
// the placement rule's to report. The proofs below fix that from the outside, at
// the halt a build actually produces, because the two rules sit at different
// stages and no test inside either package can see which one speaks first.

// The catalog edit these proofs need. The fixture's application-facing component
// type declares a sizing contract whose keys all carry defaults, so a required
// key has to be written in for either rule to have anything to say.
var webUIRequiresASizingKey = catalogSplice{
	file:        "base/components/web-ui.yaml",
	anchor:      "  schema:\n    infra:\n",
	replacement: "  schema:\n    infra:\n      region: { type: string, required: true }\n",
}

// The Release's whole boundary block for the component under proof, quoted as
// the fixture writes it. It is replaced wholesale per row: the component is
// declared by the Aggregate and placed only here, so removing this block is
// what leaves it placed nowhere.
const frontendBoundaryBlock = `    frontend-ns:
      directory: ./dist/frontend
      annotations:
        aggrega.ai/namespace: "frontend-zone"
      components:
        frontend:
          infra:
            replicas: 2
            port: 3003
          features:
            cross-namespace-routing:
              type: core/k8s-dns-resolver
              infra:
                target_var: "API_HOST"
`

// The same boundary, placing the component and writing no sizing block at all.
// This is the document the required sizing key exists to catch.
const frontendPlacedWithNoSizing = `    frontend-ns:
      directory: ./dist/frontend
      annotations:
        aggrega.ai/namespace: "frontend-zone"
      components:
        frontend:
          features:
            cross-namespace-routing:
              type: core/k8s-dns-resolver
              infra:
                target_var: "API_HOST"
`

// The same boundary again, writing the key the contract requires.
const frontendPlacedWithTheKey = `    frontend-ns:
      directory: ./dist/frontend
      annotations:
        aggrega.ai/namespace: "frontend-zone"
      components:
        frontend:
          infra:
            region: "eu-west-1"
          features:
            cross-namespace-routing:
              type: core/k8s-dns-resolver
              infra:
                target_var: "API_HOST"
`

// releaseWithBoundary writes the fixture Release document with its boundary
// block replaced, and returns the path to the copy. An empty replacement leaves
// the component placed in no boundary.
//
// It refuses a replacement that changed nothing, so a fixture edit that stops
// matching fails loudly rather than compiling the untouched document and passing
// for the wrong reason.
func releaseWithBoundary(t *testing.T, boundary string) string {
	t.Helper()
	source, err := os.ReadFile(invocationReleasePath)
	if err != nil {
		t.Fatalf("read the Release document: %v", err)
	}
	if !bytes.Contains(source, []byte(frontendBoundaryBlock)) {
		t.Fatal("the Release fixture no longer carries the boundary block these proofs replace; the edit would be silently inert")
	}
	out := bytes.Replace(source, []byte(frontendBoundaryBlock), []byte(boundary), 1)
	path := filepath.Join(t.TempDir(), "release.yaml")
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatalf("write the edited Release document: %v", err)
	}
	return path
}

// soleViolation pulls the one finding out of a halt, failing when the halt does
// not carry the named condition's own identity.
//
// It reads the structured slice rather than the summary text, because the
// locator a front end positions on is carried per violation and the summary only
// counts them.
func soleViolation(t *testing.T, err error, sentinel error) *resolver.ResolverError {
	t.Helper()
	if err == nil {
		t.Fatal("the document under proof did not halt the build")
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("the halt does not carry the expected condition's identity: %v", err)
	}
	var multi *resolver.MultiResolverError
	if !errors.As(err, &multi) {
		t.Fatalf("the halt must carry its violations; got %T: %v", err, err)
	}
	if len(multi.Violations) != 1 {
		t.Fatalf("expected exactly one violation; got %d: %+v", len(multi.Violations), multi.Violations)
	}
	return multi.Violations[0]
}

// TestAComponentPlacedNowhereIsReportedByThePlacementRule pins the halt an
// unplaced component earns, with and without a required sizing key in play.
//
// The row without the required key is the control that gives the row with it
// something to be equal to: the two documents differ only in the catalog, the
// placement fault is identical, and the finding must be identical too. Without
// it, a proof that the sizing rule stays silent could not distinguish a
// placement report that survived from one that had been replaced by a different
// report about the same document.
func TestAComponentPlacedNowhereIsReportedByThePlacementRule(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		// requireASizingKey writes a required key into the component type's
		// sizing contract, which is what puts the earlier rule in play at all.
		requireASizingKey bool
	}{
		{name: "the component type declares no required sizing key"},
		{name: "the component type declares one", requireASizingKey: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var splices []catalogSplice
			if tc.requireASizingKey {
				splices = append(splices, webUIRequiresASizingKey)
			}
			aggregate, err := os.ReadFile(invocationAggregatePath)
			if err != nil {
				t.Fatalf("read the Aggregate document: %v", err)
			}
			halt := compileDocuments(t,
				splicedCatalog(t, splices...), releaseWithBoundary(t, ""), aggregate)

			finding := soleViolation(t, halt, topology.ErrUnplacedComponent())
			const wantField = ".spec.components.frontend"
			if finding.Field != wantField {
				t.Errorf("the finding must position at the component the Release has to place, %q; got %q",
					wantField, finding.Field)
			}
			// The remedy is the whole point: the author is told which key to
			// write, in a document they can edit, rather than told that a sizing
			// key is missing from an entry that does not exist.
			for _, want := range []string{"placed in no boundary", "components", "aggregates"} {
				if !bytes.Contains([]byte(finding.Error()), []byte(want)) {
					t.Errorf("the finding must name %q; got %q", want, finding.Error())
				}
			}
		})
	}
}

// TestARequiredSizingKeyIsMeasuredForAComponentTheReleasePlaced is the other
// half, and it is what keeps the proof above from being satisfied by a rule that
// reports nothing.
//
// A Release that places the component and writes no sizing block at all is the
// input a required sizing key is declared for, and it must still halt on the
// contract rather than build.
func TestARequiredSizingKeyIsMeasuredForAComponentTheReleasePlaced(t *testing.T) {
	t.Parallel()

	aggregate, err := os.ReadFile(invocationAggregatePath)
	if err != nil {
		t.Fatalf("read the Aggregate document: %v", err)
	}
	halt := compileDocuments(t,
		splicedCatalog(t, webUIRequiresASizingKey),
		releaseWithBoundary(t, frontendPlacedWithNoSizing),
		aggregate)

	finding := soleViolation(t, halt, resolver.ErrSchemaConformance())
	const wantField = ".spec.components.frontend.infra.region"
	if finding.Field != wantField {
		t.Errorf("the finding must position at the sizing key the Release has to write, %q; got %q",
			wantField, finding.Field)
	}
}

// TestAReleaseWritingTheRequiredSizingKeyBuilds is the control both proofs
// above are read against.
//
// Without it, a pipeline that refused every one of these documents would look
// like two successes: the only difference between this row and the one beside it
// is the single key the Release writes, and the contract is declared
// identically in both.
func TestAReleaseWritingTheRequiredSizingKeyBuilds(t *testing.T) {
	t.Parallel()

	aggregate, err := os.ReadFile(invocationAggregatePath)
	if err != nil {
		t.Fatalf("read the Aggregate document: %v", err)
	}
	if err := compileDocuments(t,
		splicedCatalog(t, webUIRequiresASizingKey),
		releaseWithBoundary(t, frontendPlacedWithTheKey),
		aggregate); err != nil {
		t.Fatalf("a Release writing the required sizing key must build; got %v", err)
	}
}

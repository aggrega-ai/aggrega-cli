// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// TestReleaseEdgeConfigReportsEveryOffendingEntry proves the rule over the
// Release's authored per-edge surface reports EVERY entry that reached no
// connection, with the authored locator of each.
//
// Reporting every one is the property the declaration buys and the one a
// single-pass walk could not have: the rule is invoked once per authored entry
// and the framework accumulates what each invocation raised, so an author fixing
// a Release with two such entries sees both in one build instead of one per
// build. A test asserting only that the build halted would pass on a rule that
// reported the first and stopped.
//
// The locator is asserted as well as the count, because the two are separate
// claims: a rule can be invoked for every entry and still position all of its
// findings at the document. Each locator reaches past the consuming component
// into the `uses` container, the socket and the alias — the entry an author
// edits. The file the Release was read from travels on the objection rather than
// on the finding, and is asserted where a subject is in hand, in the enumeration
// proof below.
func TestReleaseEdgeConfigReportsEveryOffendingEntry(t *testing.T) {
	t.Parallel()

	const release = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      components:
        api:
          uses:
            databases:
              spare:
                infra:
                  storage_class: fast
            caches:
              sessions:
                infra: {}
        db: {}
`
	// The second entry's infra is written as an empty mapping, which carries no
	// key, so only the first entry is a fault. That is the fixture's own positive
	// control on the predicate's first clause: a rule that stopped reading whether
	// the entry configures anything would report two.
	err := buildWith(t, flatAggregate, release, "")
	if !errors.Is(err, ErrUnboundReleaseEdgeConfig()) {
		t.Fatalf("configuration reaching no connection must halt; got %v", err)
	}
	t.Logf("the build refused with: %v", err)

	var multi *resolver.MultiResolverError
	if !errors.As(err, &multi) {
		t.Fatalf("want the halt to carry its per-entry findings; got %T", err)
	}
	if len(multi.Violations) != 1 {
		t.Fatalf("want one finding for the one configured entry reaching no connection; got %d: %v",
			len(multi.Violations), multi.Violations)
	}
	if got, want := multi.Violations[0].Field, `.spec.boundaries.prod.components.api.uses.databases.spare`; got != want {
		t.Errorf("locator = %q; want %q.\n"+
			"INVARIANT: the framework positions a finding at the authored entry, which is the line an "+
			"author edits.\n"+
			"CONSEQUENCE: a locator stopping at the component sends an author to a block holding several "+
			"entries and says nothing about which of them is at fault.", got, want)
	}
}

// TestReleaseEdgeConfigReportsBothOffendersInOneBuild is the assertion above's
// other half: two entries at fault produce two findings from one build.
//
// It is a separate fixture rather than another row, because the property is about
// what ONE build reports and a one-fault fixture cannot show it however it is
// asserted.
func TestReleaseEdgeConfigReportsBothOffendersInOneBuild(t *testing.T) {
	t.Parallel()

	const release = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      components:
        api:
          uses:
            databases:
              spare:
                infra:
                  storage_class: fast
            caches:
              sessions:
                infra:
                  pool_size: 4
        db: {}
`
	err := buildWith(t, flatAggregate, release, "")
	if !errors.Is(err, ErrUnboundReleaseEdgeConfig()) {
		t.Fatalf("configuration reaching no connection must halt; got %v", err)
	}
	var multi *resolver.MultiResolverError
	if !errors.As(err, &multi) {
		t.Fatalf("want the halt to carry its per-entry findings; got %T", err)
	}
	if len(multi.Violations) != 2 {
		t.Fatalf("want one finding per offending entry in a single build; got %d: %v",
			len(multi.Violations), multi.Violations)
	}
	want := []string{
		`.spec.boundaries.prod.components.api.uses.databases.spare`,
		`.spec.boundaries.prod.components.api.uses.caches.sessions`,
	}
	for i, v := range multi.Violations {
		if v.Field != want[i] {
			t.Errorf("finding %d locator = %q; want %q", i, v.Field, want[i])
		}
	}
	t.Logf("the build refused with: %v", err)
}

// TestReleaseEdgeConfigNamesTheAbsentComponentUnchanged pins the other arm of the
// same condition: an entry whose consuming component the composition does not
// hold is reported by the guard inside the flattening, with its own sentence.
//
// It is pinned rather than left to the rule above because the two arms are one
// condition reported at two positions, and the arm reached inside the flattening
// is the one no rule declared over the finished composition can reach: the build
// halts before that composition exists. A change moving the second arm out of the
// guard would leave this arm silently unreported, and a test asserting only the
// condition's identity would pass on it.
func TestReleaseEdgeConfigNamesTheAbsentComponentUnchanged(t *testing.T) {
	t.Parallel()

	const release = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      components:
        api: {}
        db: {}
        ghost:
          uses:
            databases:
              spare:
                infra:
                  storage_class: fast
`
	err := buildWith(t, flatAggregate, release, "")
	if !errors.Is(err, ErrUnboundReleaseEdgeConfig()) {
		t.Fatalf("configuration on a component the composition does not hold must halt; got %v", err)
	}
	const want = `release edge ghost/databases/spare sets infra on "ghost", which is not a component in the topology`
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("the sentence this arm reports must be unchanged.\n got: %v\nwant it to contain: %s", err, want)
	}
	t.Logf("the build refused with: %v", err)
}

// TestReleaseEdgeConfigurationTraversalEnumeratesALegalRelease proves the axis
// is walked over a Release the build accepts, and how many entries it reached.
//
// It is the assertion the halting proofs cannot make. A traversal enumerating
// nothing halts nothing, and a rule invoked zero times reports nothing — which is
// indistinguishable, from the outside, from a rule invoked for every entry and
// finding none at fault. So this drives a Release that BUILDS, and reads the
// count back off the same traversal entry the production guard calls with the
// same presentation the build produced.
//
// The traversal entry is [constraint.RunOnReleaseEdgeConfigurations] and the
// production call reaching it is guardReleaseEdgeConfiguration, which Build asks
// once the composition is built.
func TestReleaseEdgeConfigurationTraversalEnumeratesALegalRelease(t *testing.T) {
	t.Parallel()

	const release = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      components:
        api:
          uses:
            databases:
              main:
                infra:
                  storage_class: fast
        db: {}
`
	result, err := buildResultWith(t, flatAggregate, release, "")
	if err != nil {
		t.Fatalf("a Release whose configuration reaches its connection must build; got %v", err)
	}

	axis, ok := result.StageCoordinates.(constraint.ReleaseEdgeConfigurationEnumerator)
	if !ok {
		t.Fatalf("the presentation the build produced does not supply this axis (%T), so the rule "+
			"declared over it could never be invoked", result.StageCoordinates)
	}

	var seen []string
	var files, locators []string
	counting := constraint.Check[constraint.TopologyView, constraint.ReleaseEdgeConfigurationSubject]{
		Sentinel: ErrUnboundReleaseEdgeConfig(),
		Severity: constraint.SeverityFatal,
		Run: func(
			_ constraint.TopologyView,
			s constraint.ReleaseEdgeConfigurationSubject,
		) []constraint.Objection {
			seen = append(seen, strings.Join(
				[]string{s.BoundaryPath(), s.NodeID(), s.SocketName(), s.AliasName()}, "/"))
			files = append(files, s.SourceFile())
			locators = append(locators, s.Locator())
			return nil
		},
	}
	if _, err := constraint.RunOnReleaseEdgeConfigurations(
		nil, axis, result.StageView, counting); err != nil {
		t.Fatalf("the traversal must not fail over a Release the build accepted; got %v", err)
	}

	want := []string{"prod/api/databases/main"}
	if len(seen) != len(want) || seen[0] != want[0] {
		t.Fatalf("the traversal enumerated %v; want %v.\n"+
			"INVARIANT: the rule declared over this axis is invoked once per entry the Release wrote.\n"+
			"CONSEQUENCE: a traversal enumerating nothing reports nothing, which reads exactly like a "+
			"rule that ran and found every entry sound.", seen, want)
	}
	// The provenance the framework supplies, asserted where a subject is in hand:
	// the file lives on the objection and the finding does not carry it, so this
	// is the one place it can be measured rather than inferred.
	if got, want := files[0], "release.yaml"; got != want {
		t.Errorf("source file = %q; want %q", got, want)
	}
	if got, want := locators[0], `.spec.boundaries.prod.components.api.uses.databases.main`; got != want {
		t.Errorf("locator = %q; want %q", got, want)
	}
	t.Logf("the traversal enumerated %d subject(s) on a Release the build accepted: %v, at %v in %v",
		len(seen), seen, locators, files)
}

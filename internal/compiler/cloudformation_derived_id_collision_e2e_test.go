// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package compiler_test — end-to-end proof that instances arriving at one
// derived resource logical ID halt the build loudly, naming every instance
// whose name met another's, rather than emitting one resource where several
// components were declared.
//
// A CloudFormation logical ID admits letters and digits and nothing else, so a
// Recipe deriving one from a component instance's name has to drop whatever
// separators that name carried. Dropping them is lossy: two instance names
// differing only in a separator, or only in the case of a letter, arrive at the
// same ID. That loss cannot be designed away here — an ID carrying an escape
// would itself be outside the alphabet — so the requirement is not that the
// derivation keep every name apart. It is that two names arriving at one ID can
// never do so quietly.
//
// The fixture's Recipe derives its resource logical ID from the instance name
// with a single template function that reads separators as word boundaries and
// normalizes case. Its four colliding instances differ ONLY in which separator
// they use and in the case of their letters, so the derivation is the single
// variable and nothing about the Recipe, the Manifest or the boundary moves
// between them.
//
// Two properties of the fixture carry more weight than they look:
//
// Its resource body interpolates nothing per-instance, so what the instances
// contribute is as close to indistinguishable as a fragment can be. Comparing
// the bodies therefore cannot separate two contributors, which is what makes
// each contributor's own claim on the name the thing that catches this.
//
// A fifth instance whose name shares neither shape arrives at an ID of its own
// and is declared in the same stack deliberately. It is what shows the halt
// names the instances that actually met and leaves out one that met nobody — a
// halt naming every instance in the stack would give an author four names to
// consider and no way to tell which two to change.

package compiler_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

const (
	cfnDerivedIDCollisionRelease = "testdata/cloudformation-derived-id-collision/release.yaml"
	cfnDerivedIDDistinctRelease  = "testdata/cloudformation-derived-id-collision/release-distinct.yaml"
	cfnDerivedIDCatalog          = "testdata/cloudformation-derived-id-collision/catalog/base"

	// The logical ID all four colliding instances arrive at, and the key space it
	// sits in. The space is asserted because a collision is bounded by it: two
	// names meeting in different spaces have not met at all.
	cfnDerivedIDCollidedName = "MyId"
	cfnDerivedIDKeySpace     = "resource-logical-ids"
)

// cfnDerivedIDCollidingInstances are the instance names that arrive at one
// logical ID, written exactly as the Aggregate declares them. They differ only
// in which separator they use and in the case of their letters.
func cfnDerivedIDCollidingInstances() []string {
	return []string{"my-id", "my_id", "My-Id", "MY_ID"}
}

// cfnDerivedIDLoneInstance is the instance that arrives at a logical ID of its
// own, so it must not appear in the halt.
const cfnDerivedIDLoneInstance = "orders"

func cfnDerivedIDInput(release string) pipelinetest.Input {
	return pipelinetest.Input{
		ReleasePath:  release,
		CatalogPaths: []string{cfnDerivedIDCatalog},
	}
}

// cfnDerivedIDFindings returns the individual findings a halt carries, read
// through the set the report wraps rather than searched for as a substring of
// it. The report's own message names what the findings have in common and none
// of the instances, so reading the set is what makes the per-instance
// assertions below possible at all.
func cfnDerivedIDFindings(t *testing.T, err error) []string {
	t.Helper()
	var envelope *cerrors.CompilerError
	if !errors.As(err, &envelope) {
		t.Fatalf("the halt is not a categorised diagnostic: %v", err)
	}
	joined, isMulti := envelope.Unwrap().(interface{ Unwrap() []error })
	if !isMulti {
		t.Fatalf("the report wraps a single error rather than the set the rule raised: %T", envelope.Unwrap())
	}
	members := joined.Unwrap()
	texts := make([]string, 0, len(members))
	for _, m := range members {
		texts = append(texts, m.Error())
	}
	return texts
}

// TestCloudFormationDerivedIDCollision_HaltsNamingEveryInstanceThatMet is the
// control the alphabet's lossiness requires: four instance names differing only
// by separator and case, arriving at one resource logical ID, halting the build
// with every one of them named.
//
// It asserts the halt's own subject rather than merely that the build failed.
// The fixture is otherwise valid, so an assertion satisfied by any error would
// keep passing once some unrelated gate rejected it first and would silently
// stop covering the collision. The instances are asserted individually because
// naming a subset would assert a choice the compiler has not made: nothing about
// a collision makes one of several derivations the intended one, so an author
// needs all of them to decide which to rename.
//
// The error is asserted rather than a guest fault tolerated. This provider runs
// inside a WebAssembly guest, so a panic there would reach the host through the
// runtime's error channel and would also satisfy a "the build failed" check —
// which is why that check is not the one made here.
func TestCloudFormationDerivedIDCollision_HaltsNamingEveryInstanceThatMet(t *testing.T) {
	t.Parallel()
	result, err := pipelinetest.BuildE(t, cfnDerivedIDInput(cfnDerivedIDCollisionRelease))
	if err == nil {
		keys := make([]string, 0, len(result.Artifacts))
		for k := range result.Artifacts {
			keys = append(keys, k)
		}
		t.Fatalf("the build emitted %v; four instances arriving at the one logical id %q must halt it rather than emit one resource where four components were declared",
			keys, cfnDerivedIDCollidedName)
	}
	if !errors.Is(err, renderer.ErrDerivedKeyCollision()) {
		t.Fatalf("the build failed for a different reason than the derived-name collision: %v", err)
	}

	findings := cfnDerivedIDFindings(t, err)
	whole := strings.Join(findings, "\n")
	t.Logf("the collision halts with %d finding(s):\n%s", len(findings), whole)

	// One finding per instance that met another's name, and no more: the rule
	// reports per contributor rather than per name, because a contributor is what
	// an author can go and change and a name is not.
	if len(findings) != len(cfnDerivedIDCollidingInstances()) {
		t.Errorf("the halt carries %d finding(s), want one per colliding instance (%d):\n%s",
			len(findings), len(cfnDerivedIDCollidingInstances()), whole)
	}

	// Every colliding instance is named, as the Aggregate document spells it.
	for _, instance := range cfnDerivedIDCollidingInstances() {
		if !strings.Contains(whole, `"`+instance+`"`) {
			t.Errorf("the halt does not name the instance %q, so an author cannot tell it is one of the names to change:\n%s",
				instance, whole)
		}
	}

	// The name they arrived at, and the space it sits in.
	for _, want := range []string{cfnDerivedIDCollidedName, cfnDerivedIDKeySpace} {
		if !strings.Contains(whole, want) {
			t.Errorf("the halt does not name %q:\n%s", want, whole)
		}
	}

	// The instance that met nobody is absent. A halt naming it would send an
	// author looking for a collision that does not involve it, and there is no
	// edit to its name that would answer such a report.
	if strings.Contains(whole, `"`+cfnDerivedIDLoneInstance+`"`) {
		t.Errorf("the halt names %q, which arrived at a logical id of its own and met nobody:\n%s",
			cfnDerivedIDLoneInstance, whole)
	}
}

// TestCloudFormationDerivedIDCollision_DistinctNamesBuildUnchanged is the
// over-fire control. It reads the emitted bytes rather than the build's exit
// alone: what matters is that each store reaches a resource of its own, since a
// check that halted nothing but quietly folded two stores into one would pass a
// build-succeeded assertion.
func TestCloudFormationDerivedIDCollision_DistinctNamesBuildUnchanged(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, cfnDerivedIDInput(cfnDerivedIDDistinctRelease))
	stack, raw := findCfnStack(t, result.Artifacts, "stack")

	resources := cfnSectionKeys(stack, "Resources")
	want := []string{"Ledger", "Orders", "Payments", "Reporting"}
	if len(resources) != len(want) {
		t.Fatalf("the assembled stack carries resources %v, want one per declared store %v\n--- stack ---\n%s",
			resources, want, raw)
	}
	present := make(map[string]bool, len(resources))
	for _, id := range resources {
		present[id] = true
	}
	for _, id := range want {
		if !present[id] {
			t.Errorf("the assembled stack carries no resource %q, so that store is absent from the template\n--- stack ---\n%s",
				id, raw)
		}
	}

	// Every emitted name lies inside the alphabet a logical id admits, asserted
	// over each name-keyed section rather than over the one under test, so a
	// section the fixture gains later is covered without anyone remembering it.
	for _, section := range []string{"Resources", "Outputs", "Parameters"} {
		for _, id := range cfnSectionKeys(stack, section) {
			for _, r := range id {
				alnum := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
				if !alnum {
					t.Errorf("the emitted %s name %q carries %q — a logical id admits letters and digits only\n--- stack ---\n%s",
						section, id, r, raw)
				}
			}
		}
	}
}

// TestCloudFormationDerivedIDCollision_DistinctBuildIsDeterministic asserts the
// control build is byte-identical across repeated runs, which is the standing
// output contract.
func TestCloudFormationDerivedIDCollision_DistinctBuildIsDeterministic(t *testing.T) {
	pipelinetest.AssertDeterministic(t, cfnDerivedIDInput(cfnDerivedIDDistinctRelease))
}

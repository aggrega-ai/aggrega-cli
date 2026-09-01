// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package topology

import (
	"errors"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// assertGroupIsACondition pins that a group whose findings all come from one
// rule declares that rule's identity, and that its header therefore opens with
// the condition's registered sentence rather than with a count.
//
// The header is read off the group's own rendering rather than off a terminal
// transcript on purpose: a wrap on the way to the terminal can reduce a group to
// one of its findings, so a transcript assertion would be measuring the wrap.
// What this pins is the group.
func assertGroupIsACondition(t *testing.T, err error, condition error) {
	t.Helper()
	var group *resolver.MultiResolverError
	if !errors.As(err, &group) {
		t.Fatalf("the halt is not a grouped report: %T (%v)", err, err)
	}
	if group.Sentinel == nil {
		t.Fatal("the group declares no identity, so its header can only count what it found")
	}
	if !errors.Is(group.Sentinel, condition) {
		t.Errorf("the group declares a different condition\n  got : %v\n  want: %v", group.Sentinel, condition)
	}
	sentence, ok := cerrors.MessageOf(condition)
	if !ok || sentence == "" {
		t.Fatal("the condition carries no registered sentence")
	}
	if !strings.HasPrefix(group.Error(), sentence) {
		t.Errorf("the group's header does not open with the registered sentence\n  sentence: %q\n  header  : %q", sentence, group.Error())
	}
	// A group carrying no identity opens with its subject instead, which is what
	// makes the assertion above discriminating rather than decorative.
	rollup := &resolver.MultiResolverError{Entity: group.Entity, Field: group.Field, Summary: group.Summary}
	if strings.HasPrefix(rollup.Error(), sentence) {
		t.Error("the same group opens with the sentence even with no identity declared; the assertion above proves nothing")
	}
}

// TestSubModulePlacementGroupDeclaresItsCondition pins the group the sub-module
// placement rule reports as a condition: one rule produced every finding, so one
// sentence is true of all of them and names the fix.
func TestSubModulePlacementGroupDeclaresItsCondition(t *testing.T) {
	t.Parallel()
	const root = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: demo
spec:
  aggregates:
    mod:
      type: app/mod:1.0.0
`
	const module = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: app/mod
  version: 1.0.0
spec:
  components:
    worker:
      type: app/db:1.0.0
`
	const release = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      directory: ./out
      components: {}
`
	err := buildWith(t, root, release, module)
	if err == nil {
		t.Fatal("a module no boundary places must halt")
	}
	assertGroupIsACondition(t, err, ErrUnplacedSubModule())
}

// TestComponentPlacementGroupDeclaresItsCondition is the same property one
// coordinate in, and is a separate test because the two rules are asked at
// separate call sites over separate subjects.
func TestComponentPlacementGroupDeclaresItsCondition(t *testing.T) {
	t.Parallel()
	const root = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: demo
spec:
  components:
    reader:
      type: app/db:1.0.0
    writer:
      type: app/db:1.0.0
`
	const release = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      directory: ./out
      components: {}
`
	err := buildWith(t, root, release, "")
	if err == nil {
		t.Fatal("components no boundary places must halt")
	}
	assertGroupIsACondition(t, err, ErrUnplacedComponent())
}

// TestBoundaryComponentResolutionGroupDeclaresItsCondition covers the relation
// read from its other end: every finding is an entry addressing nothing, which
// is one condition with one fix.
func TestBoundaryComponentResolutionGroupDeclaresItsCondition(t *testing.T) {
	t.Parallel()
	const root = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: demo
spec:
  components:
    reader:
      type: app/db:1.0.0
`
	const release = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: demo-prod
spec:
  boundaries:
    prod:
      directory: ./out
      components:
        reader: {}
        ghost: {}
`
	err := buildWith(t, root, release, "")
	if err == nil {
		t.Fatal("a boundary entry addressing nothing must halt")
	}
	assertGroupIsACondition(t, err, ErrUnresolvedBoundaryComponent())
}

// TestSubAggregateConformanceGroupIsARollUp is the negative half, and it is what
// keeps the three tests above from being read as "every group takes an
// identity". The sub-aggregate conformance pass reports findings from several
// rules, so no one sentence is true of all of them and tells the reader what to
// do — and a group in that position declares none, opening with its subject
// instead.
func TestSubAggregateConformanceGroupIsARollUp(t *testing.T) {
	t.Parallel()
	rollup := &resolver.MultiResolverError{
		Entity:     "sub-aggregate conformance",
		Summary:    "2 sub-aggregate guard violation(s)",
		Violations: nil,
	}
	if rollup.Sentinel != nil {
		t.Fatal("fixture no longer models a roll-up")
	}
	if got, want := rollup.Error(), "sub-aggregate conformance: 2 sub-aggregate guard violation(s)"; got != want {
		t.Errorf("a roll-up must open with its subject\n  got : %q\n  want: %q", got, want)
	}
}

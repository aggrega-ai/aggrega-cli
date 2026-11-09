// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"errors"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
	"go.yaml.in/yaml/v3"
)

// twoUnknownPillarKeys writes a catalog whose two Components each declare a key
// naming no data pillar, so both grouping passes have two findings to carry.
//
// Two rather than one is deliberate: a single finding cannot distinguish a group
// that reaches its findings from one that happens to reach the only finding
// there was.
func twoUnknownPillarKeys(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFixture(t, dir, "first.yaml", `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/datastore/first
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    bogusPillarKey:
      host: { type: string }
`)
	writeFixture(t, dir, "second.yaml", `apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: core/datastore/second
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    alsoBogus:
      port: { type: integer }
`)
	return dir
}

// componentsNamingNoPillar is the same two faults as parsed entities, for the
// closure pass, which takes the entities its caller already walked rather than a
// directory.
func componentsNamingNoPillar(t *testing.T) (map[string]types.Component, map[string]string) {
	t.Helper()
	comps := map[string]types.Component{}
	srcs := map[string]string{}
	for name, body := range map[string]string{
		"core/datastore/first":  "bogusPillarKey:\n  host: { type: string }\n",
		"core/datastore/second": "alsoBogus:\n  port: { type: integer }\n",
	} {
		var comp types.Component
		if err := yaml.Unmarshal([]byte(body), &comp.Spec.Schema); err != nil {
			t.Fatalf("fixture schema for %s: %v", name, err)
		}
		comp.Metadata.Name = name
		comps[name] = comp
		srcs[name] = name + ".yaml"
	}
	return comps, srcs
}

// assertGroupReachesItsFindings walks the group in both directions the report is
// useless without: outward, to the identity a reader quotes, and inward, to each
// individual finding a caller matches.
//
// Both halves are asserted because either alone passes on a broken group. A
// group with a wrap argument and nothing reading it satisfies "the envelope has a
// chain"; a group reaching one finding satisfies "errors.Is matched". Only the
// pair says the findings are reachable.
func assertGroupReachesItsFindings(t *testing.T, group error, findings []error) {
	t.Helper()
	if group == nil {
		t.Fatal("the pass reported no group at all")
	}
	if _, ok := cerrors.ShortCodeOf(group); !ok {
		t.Errorf("no condition is reachable from the group; ShortCodeOf reported none\n  group: %v", group)
	}
	for i, finding := range findings {
		if !errors.Is(group, finding) {
			t.Errorf("finding %d is not reachable from the group: %v", i, finding)
		}
	}
}

// TestCatalogLoadGroupCarriesItsFindings pins that the catalog-load pillar pass
// attaches what it reports. Before the findings were attached, the same group
// carried its findings as joined text only: every assertion below failed while
// the rendered message looked complete, which is the shape this pins against.
func TestCatalogLoadGroupCarriesItsFindings(t *testing.T) {
	t.Parallel()
	_, err := LoadAll("", twoUnknownPillarKeys(t))
	if err == nil {
		t.Fatal("a catalog whose Components name no data pillar must not load")
	}
	assertGroupReachesItsFindings(t, err, []error{validator.ErrUnknownSchemaPillar()})

	// The group states the pass and counts what it found, and does not restate a
	// finding: the findings are on the chain, and a second copy in the text is
	// what would print them twice on one diagnostic.
	var ce *cerrors.CompilerError
	if !errors.As(err, &ce) {
		t.Fatalf("the group is not the canonical envelope: %T", err)
	}
	if want := "catalog load: 2 schema validation error(s)"; ce.Message() != want {
		t.Errorf("group message\n  got : %q\n  want: %q", ce.Message(), want)
	}
}

// TestClosureGroupCarriesItsFindings is the closure-scoped counterpart. It is a
// separate test rather than a row because the two passes are separate functions
// with separate callers, and a group that reaches its findings on one of them
// says nothing about the other.
func TestClosureGroupCarriesItsFindings(t *testing.T) {
	t.Parallel()
	comps, srcs := componentsNamingNoPillar(t)
	err := ValidateClosurePillars(comps, srcs, nil, nil, nil, nil)
	if err == nil {
		t.Fatal("closure validation must refuse a Component naming no data pillar")
	}
	assertGroupReachesItsFindings(t, err, []error{validator.ErrUnknownSchemaPillar()})

	var ce *cerrors.CompilerError
	if !errors.As(err, &ce) {
		t.Fatalf("the group is not the canonical envelope: %T", err)
	}
	if want := "closure validation: 2 schema validation error(s)"; ce.Message() != want {
		t.Errorf("group message\n  got : %q\n  want: %q", ce.Message(), want)
	}
}

// TestGroupWithNoIdentityUnderneathReportsNoCode is the other direction of the
// same walk, and it is the half a "the code now resolves" assertion cannot
// reach: a group whose findings name no registered condition reports no code and
// does not fail doing so.
func TestGroupWithNoIdentityUnderneathReportsNoCode(t *testing.T) {
	t.Parallel()
	plain := errors.New("a finding that names no registered condition")
	group := cerrors.Unidentified(
		cerrors.CatalogError,
		cerrors.SourceLocation{},
		"a pass: 1 error(s)",
		nil,
		errors.Join(plain),
	)
	if code, ok := cerrors.ShortCodeOf(group); ok {
		t.Errorf("a group over unidentified findings reported the code %q", code)
	}
	if !errors.Is(group, plain) {
		t.Error("the finding is unreachable from the group")
	}
}

// TestClosureGroupRemedyStaysOnTheGroup pins that the operator-actionable line a
// caller supplies belongs to the pass rather than to any one finding: it is one
// suggestion about the whole pass, so it sits in the group's own message and
// nothing beneath it repeats it.
func TestClosureGroupRemedyStaysOnTheGroup(t *testing.T) {
	t.Parallel()
	comps, srcs := componentsNamingNoPillar(t)
	err := ValidateClosurePillars(
		comps, srcs, nil, nil, nil,
		func() string { return "run the fetch step first" },
	)
	if err == nil {
		t.Fatal("closure validation must refuse a Component naming no data pillar")
	}
	var ce *cerrors.CompilerError
	if !errors.As(err, &ce) {
		t.Fatalf("the group is not the canonical envelope: %T", err)
	}
	if !strings.HasSuffix(ce.Message(), "\n  run the fetch step first") {
		t.Errorf("the group's message does not carry the supplied remedy: %q", ce.Message())
	}
}

// TestSubAggregateParameterGroupDeclaresItsCondition pins the fourth grouping
// site as a condition. Every finding is one unfulfilled slot of one contract, so
// one sentence is true of all of them and names the fix — which is what makes
// the group a condition rather than a pass counting what it found.
func TestSubAggregateParameterGroupDeclaresItsCondition(t *testing.T) {
	t.Parallel()
	const module = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: app/mod
  version: 1.0.0
spec:
  parameters:
    replicas:
      type: integer
    tier:
      type: string
      enum: [gold, silver]
  components:
    worker:
      type: app/db:1.0.0
`
	_, _, _, err := resolveSubAggregateParameters(
		"app/mod", []byte(module), "mod.yaml", "mod.yaml", types.Aggregate{},
		map[string]any{"replicas": "not-an-integer", "tier": "bronze"},
	)
	if err == nil {
		t.Fatal("a sub-module whose parameter contract is unfulfilled must halt")
	}
	var group *resolver.MultiResolverError
	if !errors.As(err, &group) {
		t.Fatalf("the halt is not a grouped report: %T (%v)", err, err)
	}
	if group.Sentinel == nil {
		t.Fatal("the group declares no identity, so its header can only count what it found")
	}
	if !errors.Is(group.Sentinel, resolver.ErrParameterContractViolation()) {
		t.Errorf("the group declares a different condition: %v", group.Sentinel)
	}
	sentence, ok := cerrors.MessageOf(resolver.ErrParameterContractViolation())
	if !ok {
		t.Fatal("the condition carries no registered sentence")
	}
	if !strings.HasPrefix(group.Error(), sentence) {
		t.Errorf("the group's header does not open with the registered sentence\n  sentence: %q\n  header  : %q", sentence, group.Error())
	}
	// The same group with no identity declared opens with its subject, which is
	// what makes the assertion above discriminating.
	rollup := &resolver.MultiResolverError{Entity: group.Entity, Field: group.Field, Summary: group.Summary}
	if strings.HasPrefix(rollup.Error(), sentence) {
		t.Error("the header opens with the sentence even with no identity declared")
	}
	t.Logf("group header: %s", group.Error())
	t.Logf("the same group with no identity declared: %s", rollup.Error())
}

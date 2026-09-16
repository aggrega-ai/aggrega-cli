// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// The proofs in this file cover the rule about two subsystems declaring a
// component under one name. They compile the fixture workspace for real, with an
// Aggregate written to carry the fault and nothing else, so what halts is a
// property of the compiled document rather than of an assertion.
//
// The document below is authored here rather than spliced into the committed
// fixture because the shape under proof is a different top-level arrangement of
// that document — subsystems instead of a flat component block — and a splice of
// that size is a rewrite wearing a splice's name. It keeps the fixture's
// component types, its module import and its dependency wiring, so every other
// rule on the way sees exactly what the committed fixture gives them, and it
// keeps the deployment's placement satisfied, which the placement rules ahead of
// this one require.

// twoSubsystemsOneName declares `backend` in two subsystems. Both take the
// identity `backend`, because a subsystem contributes nothing to it.
const twoSubsystemsOneName = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: fullstack-app
  version: "1.0.0"
spec:
  aggregates:
    data:
      type: db-module
  subsystems:
    web:
      components:
        frontend:
          type: core/compute/web-ui
          uses:
            apis:
              my-api:
                target: backend
                inject:
                  env:
                    - in: API_HOST
                      from: host
                    - in: API_PORT
                      from: service_port
    serving:
      components:
        backend:
          type: core/compute/api-server
          uses:
            databases:
              - data.main-db
    legacy:
      components:
        backend:
          type: core/compute/api-server
          uses:
            databases:
              - data.main-db
`

// twoSubsystemsDistinctNames is the control: the same subsystem split, with the
// third subsystem dropped so no name is declared twice.
//
// It differs from the document above in the `legacy` subsystem and in nothing
// else, which is what makes it a control for the duplicate name rather than for
// the subsystem arrangement. Renaming the second declaration instead would leave
// a component the deployment places nowhere, and the placement rule ahead of this
// one would halt on that — a different fault, proving nothing about this one.
const twoSubsystemsDistinctNames = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: fullstack-app
  version: "1.0.0"
spec:
  aggregates:
    data:
      type: db-module
  subsystems:
    web:
      components:
        frontend:
          type: core/compute/web-ui
          uses:
            apis:
              my-api:
                target: backend
                inject:
                  env:
                    - in: API_HOST
                      from: host
                    - in: API_PORT
                      from: service_port
    serving:
      components:
        backend:
          type: core/compute/api-server
          uses:
            databases:
              - data.main-db
`

// compileAggregateBytes drives the real pre-render pipeline over the fixture
// workspace with the supplied Aggregate, and hands the caller the error rather
// than failing on it.
//
// It exists for the reason its sibling helper does: the halt is the thing under
// proof, so a helper contracting on "a compilation that reached its outputs" is
// exactly what a proof here must not use.
func compileAggregateBytes(t *testing.T, aggregate string) (*stepsOneToFourOutput, error) {
	t.Helper()
	rel, err := resolver.LoadRelease(invocationReleasePath)
	if err != nil {
		t.Fatalf("load release: %v", err)
	}
	manifest, err := resolver.LoadManifest(invocationManifestPath)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	cat, err := catalog.LoadAll("", invocationCatalogPath)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		t.Fatalf("catalog extends: %d error(s); first: %v", len(extErrs), extErrs[0])
	}
	var captured *stepsOneToFourOutput
	runErr := runStepsOneToFour(
		context.Background(),
		Inputs{
			Release:           rel,
			Manifest:          manifest,
			AggregateRawBytes: []byte(aggregate),
			AggregatePath:     invocationAggregatePath,
			ReleasePath:       invocationReleasePath,
			WorkspaceConfig:   defaultWorkspaceConfigForTest(),
			PluginPins:        pluginpath.NoPins(),
			CompilerVersion:   invocationVersion,
		},
		cat.CatalogLookup(),
		cat.RecipeLookupWithManifest(manifest).Recipe,
		cat.SubAggregateLoader(),
		func(out *stepsOneToFourOutput) error { captured = out; return nil },
	)
	return captured, runErr
}

// TestTwoSubsystemsDeclaringOneNameHaltsTheBuild is the invocation-and-content
// proof: a real compilation of a document an author can write, halting through
// the rule, with the condition, its short code, the sentence a reader is shown
// and the framework's locator all read off the halt itself.
func TestTwoSubsystemsDeclaringOneNameHaltsTheBuild(t *testing.T) {
	t.Parallel()

	_, err := compileAggregateBytes(t, twoSubsystemsOneName)
	if err == nil {
		t.Fatal("an Aggregate declaring one component name in two subsystems compiled without halting")
	}
	if !errors.Is(err, topology.ErrSubsystemComponentIdentityCollision()) {
		t.Fatalf("the halt does not carry the subsystem-identity-collision sentinel: %v", err)
	}
	short, ok := cerrors.ShortCodeOf(err)
	if !ok || short != "E0294" {
		t.Errorf("short code = %q (present %t), want %q", short, ok, "E0294")
	}

	var multi *resolver.MultiResolverError
	if !errors.As(err, &multi) {
		t.Fatalf("the halt must carry its violations; got %T: %v", err, err)
	}
	// One finding, not two. The pair is one fault about one identity, so a rule
	// reporting it once per colliding declaration would tell the author the same
	// thing twice.
	var reported []*resolver.ResolverError
	for _, violation := range multi.Violations {
		if errors.Is(violation.Err, topology.ErrSubsystemComponentIdentityCollision()) {
			reported = append(reported, violation)
		}
	}
	if len(reported) != 1 {
		t.Fatalf("violations carrying this condition = %d, want 1: %+v", len(reported), multi.Violations)
	}

	// The locator is the framework's, built from the authored key rather than
	// supplied by the rule: a dotted path into the document the author opens.
	const wantLocator = ".spec.subsystems.legacy.components.backend"
	if reported[0].Field != wantLocator {
		t.Errorf("locator = %q, want %q", reported[0].Field, wantLocator)
	}
	// The sentence names the identity and both subsystems that declared it, so an
	// author holding only the printed halt can find both declarations. It is read
	// off the rendered finding rather than off the occurrence's own values,
	// because the registered sentence and the remedy are composed there.
	rendered := reported[0].Error()
	for _, want := range []string{
		"components in different subsystems take the same identity",
		"since a subsystem contributes nothing to a component's identity",
		wantLocator,
		`"backend"`,
		`declared under subsystems "serving, legacy"`,
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("the rendered finding does not carry %q: %q", want, rendered)
		}
	}
	t.Logf("rendered finding: %s", rendered)
	// The remedy is the deployment author's own document, so the category has to
	// send them there rather than to the catalog.
	if got := reported[0].ToCompilerError().Category(); got != cerrors.DeveloperError {
		t.Errorf("category = %v, want %v", got, cerrors.DeveloperError)
	}
}

// TestDistinctNamesInTwoSubsystemsCompile is the control. Without it the proof
// above would pass on a rule that raised for every composition.
func TestDistinctNamesInTwoSubsystemsCompile(t *testing.T) {
	t.Parallel()

	out, err := compileAggregateBytes(t, twoSubsystemsDistinctNames)
	if err != nil {
		t.Fatalf("subsystems declaring distinct names halted the build: %v", err)
	}
	if out == nil {
		t.Fatal("the pipeline completed without invoking the continuation")
	}
}

// TestTheSubsystemWalkNoLongerRaisesTheMergeCondition is the raise-removal
// proof.
//
// The condition about a module's components meeting an entry already in the set
// keeps its own identity and its own raise, and that raise is what a document
// like the one above used to reach. Asserting its ABSENCE from this halt is what
// makes the split observable: one identity for one fault, and a reader matching
// the merge condition no longer catches an authoring mistake in a subsystem
// tree.
func TestTheSubsystemWalkNoLongerRaisesTheMergeCondition(t *testing.T) {
	t.Parallel()

	_, err := compileAggregateBytes(t, twoSubsystemsOneName)
	if err == nil {
		t.Fatal("an Aggregate declaring one component name in two subsystems compiled without halting")
	}
	if errors.Is(err, topology.ErrSubAggregateNameCollision()) {
		t.Errorf("the subsystem walk still raises the merge condition: %v", err)
	}
	if code, ok := cerrors.ShortCodeOf(err); ok && code == "E0073" {
		t.Errorf("the halt still reports the merge condition's short code: %v", err)
	}
}

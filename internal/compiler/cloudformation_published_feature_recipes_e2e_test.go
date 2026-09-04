// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// End-to-end coverage of the two published cloudformation FeatureRecipes, built
// against the sibling catalog through the real compiled Wasm guest and asserted
// against the emitted bytes.
//
// The two are covered together because they are the two attachment SCOPES the
// provider realizes, and each one's distinguishing property is only observable
// against the other's:
//
//   - The alarm realization is component-scope, so it renders once per attached
//     component and derives its resource name from the host and the attachment
//     name together.
//   - The settings realization is boundary-scope, so it renders once per boundary
//     however many components that boundary holds, and derives its resource name
//     from the boundary alone.
//
// A fixture proving only that either one renders would leave both of those
// properties untested, so what is asserted here is the count and the derived
// name rather than mere presence — and the boundary-scope count is taken in a
// boundary holding more than one component, because in a boundary holding one
// the two scopes are indistinguishable.
//
// The component-scope realization joins two authored fields with a space before
// folding, and the fold reads a space as a word boundary exactly as it reads a
// hyphen. So the boundary between the pair is gone by the time the name exists,
// and two components sharing neither an instance name nor an attachment name can
// still reach one resource name. That is asserted twice over: directly against
// the fold, and through a real build that halts on it.
//
// These tests depend on both aggrega-cli AND aggrega-hub submodules being
// initialized in the umbrella checkout (the catalog paths cross the submodule
// boundary into aggrega-hub). They skip when the sibling catalog is absent.

package compiler_test

import (
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/alphanumeric"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// cfnFeatureRecipeCatalogPaths adds the two published Features and their two
// published cloudformation FeatureRecipes to the shared set, so both attachment
// scopes resolve from the sibling hub rather than from a fixture-local copy. A
// local copy would render identically on the day it was written and would stop
// being a statement about the published entity the moment either moved.
func cfnFeatureRecipeCatalogPaths() []string {
	return append(cfnCatalogPaths(),
		cfnHubCatalog+"/feature/example/storage-alarm/1.0.0/feature.yaml",
		cfnHubCatalog+"/feature-recipe/cloudformation/storage-alarm/1.0.0/recipe.yaml",
		cfnHubCatalog+"/feature/core/shared-config/1.0.0/feature.yaml",
		cfnHubCatalog+"/feature-recipe/cloudformation/shared-config/1.0.0/recipe.yaml",
	)
}

func cfnFeatureRecipeFlavoursInput(t *testing.T) pipelinetest.Input {
	t.Helper()
	skipIfNoSiblingCatalog(t)
	return pipelinetest.Input{
		ReleasePath:  "testdata/cloudformation-published-feature-recipes/release-flavours.yaml",
		CatalogPaths: cfnFeatureRecipeCatalogPaths(),
	}
}

func cfnFeatureRecipeFoldCollisionInput(t *testing.T) pipelinetest.Input {
	t.Helper()
	skipIfNoSiblingCatalog(t)
	return pipelinetest.Input{
		ReleasePath:  "testdata/cloudformation-published-feature-recipes/release-fold-collision.yaml",
		CatalogPaths: cfnFeatureRecipeCatalogPaths(),
	}
}

// TestCloudFormationFoldReadsAPairJoinAsAWordBoundary is the premise the
// collision fixture rests on, asserted against the fold itself rather than
// inferred from the halt it causes.
//
// The component-scope realization derives its resource name by joining the host
// instance name and the attachment name with a space and folding the joined
// string. The fold admits ASCII letters and digits and treats every other
// character as a word boundary, so the space carries no more meaning than the
// hyphens already inside either field — which means the pair's own boundary is
// not recoverable from the result. Two pairs differing only in where that
// boundary falls therefore reach one name.
//
// The general non-injectivity of the fold is pinned where the fold lives. What
// is asserted here is narrower and is a property of the CALL SHAPE rather than
// of the fold: joining two fields with a character the fold discards means a
// name derived from a pair cannot tell the pair apart. That is invisible from
// either field's own test, because neither field varies.
//
// 🔴 This asserts a COLLISION. It fails if the pair join is ever changed to
// survive the fold — a separator inside the alphabet, or a derivation that
// escapes rather than drops. That is the intent: such a change is a deliberate
// change of contract for every name already deployed from this realization, and
// it must not slip in behind a green suite.
func TestCloudFormationFoldReadsAPairJoinAsAWordBoundary(t *testing.T) {
	t.Parallel()

	// Exactly the join the realization performs: the two authored fields with a
	// single space between them.
	join := func(instance, attachment string) string {
		return instance + " " + attachment
	}

	left := join("payments-db", "storage-headroom")
	right := join("payments", "db-storage-headroom")

	if left == right {
		t.Fatalf("the two joined strings must differ for this to say anything: both are %q", left)
	}

	gotLeft := alphanumeric.CapitalizedWords(left)
	gotRight := alphanumeric.CapitalizedWords(right)
	t.Logf("fold(%q) = %q", left, gotLeft)
	t.Logf("fold(%q) = %q", right, gotRight)

	if gotLeft != gotRight {
		t.Fatalf("two pairs differing only in where the pair boundary falls no longer reach one name: "+
			"fold(%q) = %q but fold(%q) = %q. If the pair join was deliberately changed to survive the "+
			"fold, every name this realization has already derived changes with it",
			left, gotLeft, right, gotRight)
	}

	// The colliding name is stated so the build-level assertion below and this
	// one are anchored to the same string rather than to each other.
	if gotLeft != cfnFeatureRecipeCollidedAlarmID {
		t.Fatalf("the pair folds to %q, not to the %q this fixture is built around",
			gotLeft, cfnFeatureRecipeCollidedAlarmID)
	}

	// The two host names fold apart, which is what makes the resource name space
	// the ONLY one the collision fixture reaches: a build in which the hosts also
	// collided would halt for two reasons and the assertion would stop being
	// about the pair join.
	if a, b := alphanumeric.CapitalizedWords("payments-db"), alphanumeric.CapitalizedWords("payments"); a == b {
		t.Fatalf("the two host names fold to one name %q, so the fixture no longer isolates the pair join", a)
	}
}

// cfnFeatureRecipeCollidedAlarmID is the resource logical id both alarm
// attachments in the collision fixture derive.
const cfnFeatureRecipeCollidedAlarmID = "PaymentsDbStorageHeadroom"

// TestCloudFormationPublishedFeatureRecipes_BothScopesRender builds both
// attachment scopes in one workspace and asserts the exact set of resource
// logical ids each boundary's assembled stack carries.
//
// The set is asserted rather than counted, and per boundary rather than in
// total: a count agrees with itself when one resource is missing and another
// unexpected one has appeared, and a total cannot see a boundary-scope resource
// that rendered in the wrong boundary. Naming what is missing and what is
// unexpected is what makes a failure readable.
//
// The load-bearing entries are the two derived names. The alarm's id composes
// its host AND its attachment name, so the two hosts' alarms are two resources.
// The settings parameter's id composes the boundary alone, so the boundary
// holding two components still carries one — which is the property that
// separates a boundary-scope realization from a component-scope one, and it is
// unobservable in a boundary holding a single component.
func TestCloudFormationPublishedFeatureRecipes_BothScopesRender(t *testing.T) {
	in := cfnFeatureRecipeFlavoursInput(t)

	result := pipelinetest.Build(t, in)

	dataStack, dataRaw := findCfnStack(t, result.Artifacts, "dist/data")
	appStack, appRaw := findCfnStack(t, result.Artifacts, "dist/app")
	t.Logf("assembled data-boundary stack:\n%s", dataRaw)
	t.Logf("assembled app-boundary stack:\n%s", appRaw)

	// The data boundary holds one component, which carries the alarm attachment,
	// and the boundary attachment renders once for the boundary.
	assertCfnResourceSet(t, "data", dataStack, []string{
		"PaymentsDb",
		"PaymentsDbStorageHeadroom",
		"DataSharedConfig",
	})

	// The app boundary holds TWO components. The alarm attachment is carried by
	// one of them, so there is one alarm; the boundary attachment renders ONCE
	// for the boundary rather than once per component, so there is one settings
	// parameter and not two.
	assertCfnResourceSet(t, "app", appStack, []string{
		"OrdersDb",
		"OrdersDbStorageHeadroom",
		"ReportingApp",
		"AppSharedConfig",
	})
}

// TestCloudFormationPublishedSharedConfig_RendersOncePerBoundary reads the
// boundary-scope realization's own output in both boundaries.
//
// Two things are asserted that the resource-set test structurally cannot: that
// the parameter each boundary carries is named for THAT boundary and carries
// THAT boundary's own resolved settings — so a realization rendering once
// globally and being copied would be caught — and that exactly one resource in
// the two-component boundary carries the settings parameter's own path, which is
// the count taken by the resource's identity rather than by its type. Counting
// by type would be wrong here: the application component's own Recipe emits a
// parameter of the same type, so a type count in this boundary is two for a
// correct build.
func TestCloudFormationPublishedSharedConfig_RendersOncePerBoundary(t *testing.T) {
	in := cfnFeatureRecipeFlavoursInput(t)

	result := pipelinetest.Build(t, in)

	dataStack, _ := findCfnStack(t, result.Artifacts, "dist/data")
	appStack, _ := findCfnStack(t, result.Artifacts, "dist/app")

	for _, tc := range []struct {
		boundary  string
		stack     map[string]any
		logicalID string
		wantValue string
	}{
		// The tier is authored per boundary in the Release, and the two
		// boundaries author DIFFERENT values. Each boundary's parameter must
		// carry its own resolved value: identical values would pass a presence
		// check while saying nothing about per-boundary resolution, and a
		// realization that rendered once and was copied would show up here.
		{"data", dataStack, "DataSharedConfig", "tier=production-data"},
		{"app", appStack, "AppSharedConfig", "tier=production-app"},
	} {
		t.Run(tc.boundary, func(t *testing.T) {
			resources := mustCfnMap(t, tc.stack, "Resources")
			param := mustCfnMap(t, resources, tc.logicalID)
			if param["Type"] != "AWS::SSM::Parameter" {
				t.Fatalf("%s.Type = %#v, want AWS::SSM::Parameter", tc.logicalID, param["Type"])
			}
			props := mustCfnMap(t, param, "Properties")

			wantName := "/" + tc.boundary + "/shared-config"
			if props["Name"] != wantName {
				t.Errorf("%s.Properties.Name = %#v, want %q — the parameter path is derived from the boundary, so a wrong one means it rendered for another boundary",
					tc.logicalID, props["Name"], wantName)
			}
			if props["Value"] != tc.wantValue {
				t.Errorf("%s.Properties.Value = %#v, want %q — this boundary's own resolved settings",
					tc.logicalID, props["Value"], tc.wantValue)
			}

			// The count, taken by the resource's own derived path rather than by
			// its type. One is the boundary-scope contract; a component-scope
			// realization in this boundary would want one per component.
			carrying := 0
			for name, res := range resources {
				entry, ok := res.(map[string]any)
				if !ok {
					t.Fatalf("Resources[%q] is not an object; got %#v", name, res)
				}
				entryProps, ok := entry["Properties"].(map[string]any)
				if !ok {
					continue
				}
				if entryProps["Name"] == wantName {
					carrying++
				}
			}
			if carrying != 1 {
				t.Errorf("%d resource(s) in the %q boundary carry the settings path %q; a boundary-scope realization renders exactly one however many components the boundary holds. every resource: %v",
					carrying, tc.boundary, wantName, sortedCfnKeys(resources))
			}
		})
	}
}

// TestCloudFormationPublishedStorageAlarm_EveryFlavour reads the
// component-scope realization's own output in both flavours it ships.
//
// The realization branches on two optional operator fields, and the two
// boundaries author them in opposite directions — one sets both, the other sets
// neither. So each branch's rendered shape is asserted against a build that
// actually took it: the notification action present and absent, and the
// evaluation count authored and fallen back to. A fixture authoring only one
// direction would leave the other branch rendering unread.
//
// The dimension that identifies which instance the alarm watches is asserted
// too, because it is the one value in the alarm that must agree with a name
// derived by a DIFFERENT recipe: the host component's own Recipe derives the
// database resource's logical id, and the alarm reads it by that id. The two
// derivations agreeing is what makes the alarm point at anything at all.
func TestCloudFormationPublishedStorageAlarm_EveryFlavour(t *testing.T) {
	in := cfnFeatureRecipeFlavoursInput(t)

	result := pipelinetest.Build(t, in)

	dataStack, _ := findCfnStack(t, result.Artifacts, "dist/data")
	appStack, _ := findCfnStack(t, result.Artifacts, "dist/app")

	for _, tc := range []struct {
		flavour             string
		stack               map[string]any
		alarmID             string
		watchedHostID       string
		wantThreshold       float64
		wantEvaluations     float64
		wantNotifyTarget    string
		wantNotifyActionSet bool
	}{
		{
			// Both optional operator fields authored.
			flavour:             "operator-sized",
			stack:               dataStack,
			alarmID:             "PaymentsDbStorageHeadroom",
			watchedHostID:       "PaymentsDb",
			wantThreshold:       21474836480,
			wantEvaluations:     3,
			wantNotifyTarget:    "arn:aws:sns:eu-west-1:123456789012:payments-oncall",
			wantNotifyActionSet: true,
		},
		{
			// Neither authored: the realization's own fallback evaluation count
			// stands and no notification action is emitted at all.
			flavour:             "bare",
			stack:               appStack,
			alarmID:             "OrdersDbStorageHeadroom",
			watchedHostID:       "OrdersDb",
			wantThreshold:       10737418240,
			wantEvaluations:     2,
			wantNotifyActionSet: false,
		},
	} {
		t.Run(tc.flavour, func(t *testing.T) {
			resources := mustCfnMap(t, tc.stack, "Resources")
			alarm := mustCfnMap(t, resources, tc.alarmID)
			if alarm["Type"] != "AWS::CloudWatch::Alarm" {
				t.Fatalf("%s.Type = %#v, want AWS::CloudWatch::Alarm", tc.alarmID, alarm["Type"])
			}
			props := mustCfnMap(t, alarm, "Properties")

			if props["Threshold"] != tc.wantThreshold {
				t.Errorf("%s.Properties.Threshold = %#v, want %v — the headroom is developer intent and reaches the alarm from the Aggregate",
					tc.alarmID, props["Threshold"], tc.wantThreshold)
			}
			if props["EvaluationPeriods"] != tc.wantEvaluations {
				t.Errorf("%s.Properties.EvaluationPeriods = %#v, want %v",
					tc.alarmID, props["EvaluationPeriods"], tc.wantEvaluations)
			}

			// The notification branch, asserted in both directions. Absence is
			// asserted as absence of the key rather than as an empty value: an
			// emitted empty action list is a different rendered artifact from no
			// action list, and only one of the two is what the realization says
			// it does.
			actions, present := props["AlarmActions"]
			if tc.wantNotifyActionSet {
				if !present {
					t.Fatalf("%s carries no AlarmActions, but this attachment authored a notification target",
						tc.alarmID)
				}
				list, ok := actions.([]any)
				if !ok || len(list) != 1 {
					t.Fatalf("%s.Properties.AlarmActions = %#v, want a single-entry list", tc.alarmID, actions)
				}
				if list[0] != tc.wantNotifyTarget {
					t.Errorf("%s.Properties.AlarmActions[0] = %#v, want %q", tc.alarmID, list[0], tc.wantNotifyTarget)
				}
			} else if present {
				t.Errorf("%s carries AlarmActions %#v, but this attachment authored no notification target — the realization emits no action list at all rather than an empty one",
					tc.alarmID, actions)
			}

			// The cross-recipe agreement: the alarm's dimension reads the host
			// resource by the logical id the HOST's own Recipe derived. The two
			// derivations are in different files and agree only because both fold
			// the same instance name.
			dims, ok := props["Dimensions"].([]any)
			if !ok || len(dims) != 1 {
				t.Fatalf("%s.Properties.Dimensions = %#v, want a single dimension", tc.alarmID, props["Dimensions"])
			}
			dim, ok := dims[0].(map[string]any)
			if !ok {
				t.Fatalf("%s.Properties.Dimensions[0] is not an object; got %#v", tc.alarmID, dims[0])
			}
			if dim["Name"] != "DBInstanceIdentifier" {
				t.Errorf("%s dimension name = %#v, want DBInstanceIdentifier", tc.alarmID, dim["Name"])
			}
			ref, ok := dim["Value"].(map[string]any)
			if !ok || ref["Ref"] != tc.watchedHostID {
				t.Errorf("%s watches %#v, want a Ref to the host resource %q its host Recipe derived — the alarm and the database name the same instance or the alarm watches nothing",
					tc.alarmID, dim["Value"], tc.watchedHostID)
			}

			// The published entry the attachment declares reaches the assembled
			// stack's Outputs, named for the host component and the entry. That
			// projection is what makes the alarm addressable by a consumer.
			outputs := mustCfnMap(t, tc.stack, "Outputs")
			found := ""
			for name := range outputs {
				if strings.Contains(name, "alarm") {
					found = name
					break
				}
			}
			if found == "" {
				t.Errorf("no Output carries the attachment's published alarm entry; outputs: %v", sortedCfnKeys(outputs))
			} else {
				t.Logf("%s publishes its alarm identity as Output %q", tc.alarmID, found)
			}
		})
	}
}

// TestCloudFormationPublishedStorageAlarm_FoldCollisionHalts is the negative
// case, and it is the one assertion in this file that could not be written
// against either recipe in isolation.
//
// Two components whose instance names differ and whose attachment names differ
// reach one alarm resource name, because the space joining the pair is folded
// away exactly as the hyphens inside either field are. Both fragments assemble
// into one stack, so the two names genuinely meet inside one Resources map —
// where one would displace the other and the build would emit a stack carrying
// one alarm for two declared attachments.
//
// It halts instead, and the halt is asserted against the derived-name-collision
// condition specifically rather than against any error: the fixture reaches a
// working provider through a working manifest, so a build that failed for an
// unrelated reason would satisfy a bare error check while proving nothing about
// the collision. The key space is asserted for the same reason — two names
// meeting in different spaces have not met.
func TestCloudFormationPublishedStorageAlarm_FoldCollisionHalts(t *testing.T) {
	in := cfnFeatureRecipeFoldCollisionInput(t)

	_, err := pipelinetest.BuildE(t, in)
	if err == nil {
		t.Fatal("two attachments whose pair joins fold to one resource name built cleanly; " +
			"the assembled stack would carry one alarm where two were declared")
	}
	if !errors.Is(err, renderer.ErrDerivedKeyCollision()) {
		t.Fatalf("the build failed for a different reason than the derived-name collision: %v", err)
	}

	findings := cfnFeatureRecipeFindings(t, err)
	t.Logf("the halt carries %d finding(s):\n%s", len(findings), strings.Join(findings, "\n"))

	// The name the two met on, and the space it met in.
	for _, f := range findings {
		if !strings.Contains(f, `"`+cfnFeatureRecipeCollidedAlarmID+`"`) {
			t.Errorf("a finding does not name the resource id the two attachments met on (%q)\ngot: %s",
				cfnFeatureRecipeCollidedAlarmID, f)
		}
		if !strings.Contains(f, "resource-logical-ids") {
			t.Errorf("a finding does not name the resource name space; a collision is bounded by its space\ngot: %s", f)
		}
	}

	// One finding per contributor, each naming BOTH of its coordinates. The
	// attachment key alone would name two different attachments identically
	// here — they differ — but the HOST is what disambiguates which of the two
	// authored pairs a finding is about, and an author needs both to know which
	// file to open.
	for _, want := range []string{
		`derived by component "payments-db", carrying attachment "storage-headroom"`,
		`derived by component "payments", carrying attachment "db-storage-headroom"`,
	} {
		found := false
		for _, f := range findings {
			if strings.Contains(f, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no finding names the contributor %s\nfindings:\n%s", want, strings.Join(findings, "\n"))
		}
	}
	if len(findings) != 2 {
		t.Errorf("expected one finding per contributor that met on the name, got %d\nfindings:\n%s",
			len(findings), strings.Join(findings, "\n"))
	}
}

// TestCloudFormationPublishedFeatureRecipes_Deterministic asserts the build
// carrying both attachment scopes is byte-identical across repeated runs (the
// Deterministic Output invariant).
func TestCloudFormationPublishedFeatureRecipes_Deterministic(t *testing.T) {
	in := cfnFeatureRecipeFlavoursInput(t)
	pipelinetest.AssertDeterministic(t, in)
}

// cfnFeatureRecipeFindings unwraps a halt into the individual findings the rule
// raised, read through the wrapped value rather than searched for as a substring
// of the report's own text.
//
// A halt carrying more than one finding wraps a joined set and each member
// carries its own detail; a halt carrying exactly one puts that finding's detail
// in the categorised report's own message instead. Both shapes are read, because
// a helper admitting only the set would fail a single-finding case for the shape
// of its wrapper rather than for its content — and this file asserts the finding
// COUNT, so it must be able to observe a count of one.
func cfnFeatureRecipeFindings(t *testing.T, err error) []string {
	t.Helper()
	var envelope *cerrors.CompilerError
	if !errors.As(err, &envelope) {
		t.Fatalf("the halt is not a categorised diagnostic: %v", err)
	}
	if joined, isMulti := envelope.Wrap.(interface{ Unwrap() []error }); isMulti {
		members := joined.Unwrap()
		texts := make([]string, 0, len(members))
		for _, m := range members {
			texts = append(texts, m.Error())
		}
		return texts
	}
	return []string{envelope.Error()}
}

// assertCfnResourceSet compares the resource logical ids a stack carries against
// the exact set expected, naming what is missing and what is unexpected.
//
// A set difference rather than a count: both sides short by one still agree on a
// count, so a count passes on a stack that lost one resource and gained another
// — which is precisely what a mis-scoped attachment realization produces.
func assertCfnResourceSet(t *testing.T, boundary string, stack map[string]any, want []string) {
	t.Helper()
	resources := mustCfnMap(t, stack, "Resources")

	wanted := make(map[string]bool, len(want))
	for _, w := range want {
		wanted[w] = true
	}

	var missing, unexpected []string
	for _, w := range want {
		if _, ok := resources[w]; !ok {
			missing = append(missing, w)
		}
	}
	for got := range resources {
		if !wanted[got] {
			unexpected = append(unexpected, got)
		}
	}
	sort.Strings(missing)
	sort.Strings(unexpected)

	if len(missing) > 0 {
		t.Errorf("the %q boundary's stack is missing resource(s) %v; every resource it carries: %v",
			boundary, missing, sortedCfnKeys(resources))
	}
	if len(unexpected) > 0 {
		t.Errorf("the %q boundary's stack carries unexpected resource(s) %v; an attachment realization rendering at the wrong scope shows up here rather than as a wrong count",
			boundary, unexpected)
	}
}

// sortedCfnKeys returns a decoded object's keys in a stable order, so a failure
// message reads the same way on every run.
func sortedCfnKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lintops

// What a linter plugin is handed, read where it arrives: at a guest. These tests
// run a guest that answers every input with one finding carrying the input's own
// bytes, dispatched through the host's linter dispatcher, so what they read back
// is the payload the guest received — encoded by the dispatcher itself — rather
// than a structure the host assembled on the way to encoding it. Each plants
// parameter values in the documents a run reads, asserts that none reaches the
// guest, and asserts that the rest of those documents does, so a run cannot pass
// by handing the guest nothing.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// echoGuestPath builds the echoing guest kept beside these tests and returns the
// built artifact's path.
func echoGuestPath(t *testing.T) string {
	t.Helper()
	src, err := filepath.Abs(filepath.Join("testdata", "payload-echo-linter"))
	if err != nil {
		t.Fatalf("abs guest source: %v", err)
	}
	return wasmtest.EnsureLocalGuestBuilt(t, src, "payload-echo-linter.wasm")
}

// echoDispatch is the production linter dispatch run at the echoing guest alone,
// in place of whatever attachments the workspace resolved. Only which plugin runs
// is substituted: the inputs, their encoding and the call are the dispatcher's
// own.
func echoDispatch(t *testing.T) linterDispatch {
	t.Helper()
	guest, err := os.ReadFile(echoGuestPath(t))
	if err != nil {
		t.Fatalf("read echo guest: %v", err)
	}
	echo := []LinterAttachment{{Plugin: pluginpath.ResolvedPlugin{Bytes: guest}}}
	return func(ctx context.Context, _ []LinterAttachment, inputs []validator.LinterInput) ([][]abi.Diagnostic, error) {
		return dispatchLinterAttachments(ctx, echo, inputs)
	}
}

// echoedPayloads returns, per finding a run collected, the payload the echoing
// guest received for it: the guest returns each input as its finding's message.
func echoedPayloads(t *testing.T, collect *FindingCollector) map[string][]string {
	t.Helper()
	if !collect.Reached() {
		t.Fatal("the run never reached the point of reporting, so no payload was read")
	}
	byRule := map[string][]string{}
	for _, f := range collect.Report().Findings {
		var in validator.LinterInput
		if err := json.Unmarshal([]byte(f.Message), &in); err != nil {
			t.Fatalf("a finding's message is not the payload the guest was handed: %v\n%s", err, f.Message)
		}
		byRule[f.RuleID] = append(byRule[f.RuleID], f.Message)
	}
	if len(byRule) == 0 {
		t.Fatal("the guest answered no input, so the absences below would prove nothing")
	}
	return byRule
}

// assertNoneReached fails for every planted value any payload carries.
func assertNoneReached(t *testing.T, byRule map[string][]string, planted []string) {
	t.Helper()
	for rule, payloads := range byRule {
		for _, payload := range payloads {
			for _, value := range planted {
				if strings.Contains(payload, value) {
					t.Errorf("the payload for rule %q carries the parameter value %q; no parameter value may reach a linter plugin:\n%s",
						rule, value, payload)
				}
			}
		}
	}
}

// assertPlanted fails unless every planted value is written in doc, so that its
// absence from a payload is the conversion's doing rather than the fixture's.
func assertPlanted(t *testing.T, doc string, planted []string) {
	t.Helper()
	for _, value := range planted {
		if !strings.Contains(doc, value) {
			t.Fatalf("the planted value %q is not in the document, so its absence would prove nothing", value)
		}
	}
}

// plantedAggregate is an Aggregate carrying a parameter value in each place one
// is written — a declaration's default, a default nested under a declaration's
// properties, and a value passed to an imported module — each spelled from mark,
// beside content that is no parameter value: a component description naming the
// mark, which reads a parameter as template text.
func plantedAggregate(version, mark string) string {
	return `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: echo-app
  version: "` + version + `"
spec:
  parameters:
    region:
      type: string
      description: the region the app runs in
      default: ` + mark + `-DEFAULT
    database:
      type: object
      properties:
        password:
          type: string
          default: ` + mark + `-NESTED-DEFAULT
  aggregates:
    edge:
      type: probe-module
      parameters:
        zone: ` + mark + `-MODULE-VALUE
  components:
    web:
      type: core/compute/web-ui
      description: "` + mark + `-CONTENT in {{ .parameters.region }}"
`
}

// plantedValues names the parameter values plantedAggregate writes for mark.
func plantedValues(mark string) []string {
	return []string{mark + "-DEFAULT", mark + "-NESTED-DEFAULT", mark + "-MODULE-VALUE"}
}

// keptAggregateContent is what an Aggregate planted with mark must still hand a
// rule: its other content, its parameter declarations without their defaults, and
// its module entry without the values passed to it. Each is written as the
// encoder writes it, keys sorted.
func keptAggregateContent(mark string) []string {
	return []string{
		mark + "-CONTENT in {{ .parameters.region }}",
		`"region":{"description":"the region the app runs in","type":"string"}`,
		`"database":{"properties":{"password":{"type":"string"}},"type":"object"}`,
		`"edge":{"type":"probe-module"}`,
	}
}

// plantedDraftAggregate is an Aggregate the schema refuses: its parameter
// declarations carry a key outside the declaration vocabulary at a parameter's own
// level and under properties, items and valuesType, each holding a value spelled
// from mark, beside a component description naming the mark.
func plantedDraftAggregate(mark string) string {
	return `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: echo-draft
  version: "1.0.0"
spec:
  parameters:
    api:
      type: object
      description: the upstream
      token: ` + mark + `-UNKNOWN-TOP
      properties:
        key:
          type: string
          passwd: ` + mark + `-UNKNOWN-PROPERTY
    hosts:
      type: array
      items:
        type: string
        secret: ` + mark + `-UNKNOWN-ITEMS
    labels:
      type: object
      valuesType:
        type: string
        example: ` + mark + `-UNKNOWN-VALUESTYPE
  components:
    web:
      type: core/compute/web-ui
      description: "` + mark + `-CONTENT"
`
}

// plantedRelease is a Release supplying a parameter value spelled from mark,
// beside a boundary whose directory names the mark and is no parameter value.
func plantedRelease(mark string) string {
	return `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: echo-release
  environment: test
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  parameters:
    region: ` + mark + `-RELEASE-VALUE
  boundaries:
    app:
      directory: ./dist/` + mark + `-CONTENT
`
}

// writeDoc writes body to a fresh file and returns its path.
func writeDoc(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

// TestEntityLintHandsTheGuestNoParameterValue lints an Aggregate, a Release and a
// draft Aggregate the schema refuses each as one entity, through the entity scope
// every surface drives, at the echoing guest named the way the command line's
// --plugin names one, and reads every payload the guest received: one per rule the
// scope dispatches. The scope validates nothing against the schema before the
// guest runs, which is why a draft's keys outside the declaration vocabulary are
// planted here and not only in the conversion's own test.
func TestEntityLintHandsTheGuestNoParameterValue(t *testing.T) {
	guest := echoGuestPath(t)
	const mark = "ENTITY-SCOPE-7c41"
	cases := []struct {
		name    string
		doc     string
		planted []string
		kept    []string
	}{
		{
			name:    "an Aggregate's defaults and the values it passes a module",
			doc:     plantedAggregate("1.0.0", mark),
			planted: plantedValues(mark),
			kept:    keptAggregateContent(mark),
		},
		{
			name:    "a Release's parameters",
			doc:     plantedRelease(mark),
			planted: []string{mark + "-RELEASE-VALUE"},
			kept:    []string{"./dist/" + mark + "-CONTENT"},
		},
		{
			name: "a draft's keys outside the declaration vocabulary, at every level",
			doc:  plantedDraftAggregate(mark),
			planted: []string{
				mark + "-UNKNOWN-TOP", mark + "-UNKNOWN-PROPERTY", mark + "-UNKNOWN-ITEMS", mark + "-UNKNOWN-VALUESTYPE",
			},
			kept: []string{
				mark + "-CONTENT",
				`"api":{"description":"the upstream","properties":{"key":{"type":"string"}},"type":"object"}`,
				`"hosts":{"items":{"type":"string"},"type":"array"}`,
				`"labels":{"type":"object","valuesType":{"type":"string"}}`,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertPlanted(t, tc.doc, tc.planted)
			collect := &FindingCollector{}
			var diag bytes.Buffer
			err := RunEntity(t.Context(), Config{
				EntityPath: writeDoc(t, "entity.yaml", tc.doc),
				PluginPath: guest,
				Workspace:  resolveTempWorkspace(t),
				Out:        io.Discard,
				DiagOut:    &diag,
				Collect:    collect,
			})
			if err != nil {
				t.Fatalf("the entity lint must run to its report: %v\n%s", err, diag.String())
			}
			byRule := echoedPayloads(t, collect)
			for _, rule := range defaultRuleIDs(false) {
				if len(byRule[rule]) != 1 {
					t.Errorf("rule %q is dispatched once per entity; the guest answered it %d time(s)", rule, len(byRule[rule]))
				}
			}
			assertNoneReached(t, byRule, tc.planted)
			for rule, payloads := range byRule {
				for _, payload := range payloads {
					for _, fragment := range tc.kept {
						if !strings.Contains(payload, fragment) {
							t.Errorf("the payload for rule %q lost %s, which is no parameter value:\n%s", rule, fragment, payload)
						}
					}
				}
			}
		})
	}
}

// TestPrePublishLintHandsTheGuestNoParameterValueOfThePreviousVersion publishes an
// Aggregate to a registry, lints its next version against it, and reads the
// payload the schema-evolution rule received: the version compared against must
// arrive with its parameter values withheld exactly as the proposed one does,
// while still carrying the declarations that comparison reads.
func TestPrePublishLintHandsTheGuestNoParameterValueOfThePreviousVersion(t *testing.T) {
	guest := echoGuestPath(t)
	const (
		previousMark = "PREVIOUS-VERSION-2e96"
		proposedMark = "PROPOSED-VERSION-b05d"
	)
	previous := plantedAggregate("1.0.0", previousMark)
	proposed := plantedAggregate("1.1.0", proposedMark)
	assertPlanted(t, previous, plantedValues(previousMark))
	assertPlanted(t, proposed, plantedValues(proposedMark))

	target := &memoryRegistryTarget{Store: memory.New()}
	pushFixtureToTarget(t, target, "aggregate", "echo-app", "1.0.0", []byte(previous))

	collect := &FindingCollector{}
	var diag bytes.Buffer
	err := RunEntity(t.Context(), Config{
		EntityPath:  writeDoc(t, "aggregate.yaml", proposed),
		PrePublish:  true,
		PluginPath:  guest,
		FetchTarget: target,
		Workspace:   resolveTempWorkspace(t),
		Out:         io.Discard,
		DiagOut:     &diag,
		Collect:     collect,
	})
	if err != nil {
		t.Fatalf("the pre-publish lint must run to its report: %v\n%s", err, diag.String())
	}
	byRule := echoedPayloads(t, collect)
	assertNoneReached(t, byRule, append(plantedValues(previousMark), plantedValues(proposedMark)...))

	evolution := byRule[ruleID(errSchemaEvolution)]
	if len(evolution) != 1 {
		t.Fatalf("the schema-evolution rule is dispatched once; the guest answered it %d time(s)", len(evolution))
	}
	var in validator.LinterInput
	if err := json.Unmarshal([]byte(evolution[0]), &in); err != nil {
		t.Fatalf("decode the schema-evolution payload: %v", err)
	}
	if len(in.Previous) == 0 {
		t.Fatal("the schema-evolution payload carries no previous version, so its withholding was never exercised")
	}
	for _, fragment := range keptAggregateContent(previousMark) {
		if !strings.Contains(string(in.Previous), fragment) {
			t.Errorf("the previous version lost %s, which is no parameter value:\n%s", fragment, in.Previous)
		}
	}
	for _, fragment := range keptAggregateContent(proposedMark) {
		if !strings.Contains(string(in.Proposed), fragment) {
			t.Errorf("the proposed version lost %s, which is no parameter value:\n%s", fragment, in.Proposed)
		}
	}
}

// TestReleaseLintHandsTheGuestNoParameterValue runs the release-scoped pass over a
// Release extending a base, each supplying a parameter, deploying an Aggregate
// that gives a default and passes a module a value, with a rule reading the whole
// Release among the rules dispatched. Every payload the guest receives — the
// per-entity ones and the one carrying the whole Release — must carry none of the
// four values, while the release-scoped one still carries the Aggregate's
// declarations and the folded Release.
func TestReleaseLintHandsTheGuestNoParameterValue(t *testing.T) {
	rw, releasePath := seedProbeWorkspaceWith(t, "", map[string]string{
		"aggregate.yaml":    probeAggregateWithModule,
		probeModulePath:     probeModule,
		"base-release.yaml": probeBaseReleaseWithModule,
	})
	planted := map[string]string{
		childParameterMarker:   "release.yaml",
		baseParameterMarker:    "base-release.yaml",
		aggregateDefaultMarker: "aggregate.yaml",
		moduleParameterMarker:  "aggregate.yaml",
	}
	values := make([]string, 0, len(planted))
	for value, file := range planted {
		body, err := os.ReadFile(filepath.Join(filepath.Dir(releasePath), file))
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		assertPlanted(t, string(body), []string{value})
		values = append(values, value)
	}

	collect := &FindingCollector{}
	var diag bytes.Buffer
	if err := runRelease(t.Context(), rw, releasePath, nil, io.Discard, &diag, collect, releaseRun{
		rules:    []string{probeRule},
		dispatch: echoDispatch(t),
	}); err != nil {
		t.Fatalf("the release-scoped pass must run to its report: %v\n%s", err, diag.String())
	}
	byRule := echoedPayloads(t, collect)
	assertNoneReached(t, byRule, values)

	whole := byRule[probeRule]
	if len(whole) != 1 {
		t.Fatalf("a rule reading a whole Release is dispatched once; the guest answered it %d time(s)", len(whole))
	}
	if len(byRule) < 2 {
		t.Error("the pass dispatched no per-entity rule, so the per-entity payloads were never read")
	}
	var in validator.LinterInput
	if err := json.Unmarshal([]byte(whole[0]), &in); err != nil {
		t.Fatalf("decode the release-scoped payload: %v", err)
	}
	for _, fragment := range []string{
		`"parameters":{"api_key":{"required":true,"type":"string"},"region":{"type":"string"}}`,
		`"edge":{"type":"probe-module"}`,
		`"description":"serves callers holding {{ .parameters.api_key }}"`,
	} {
		if !strings.Contains(string(in.Proposed), fragment) {
			t.Errorf("the Aggregate handed over lost %s, which is no parameter value:\n%s", fragment, in.Proposed)
		}
	}
	if !strings.Contains(string(in.Release), `"port":8080`) {
		t.Errorf("the Release handed over lost the boundary its base declares, so it was not the folded Release:\n%s", in.Release)
	}
}

// TestConventionsPassHandsTheGuestNoParameterValueWhateverItWalks hands the body
// the whole-catalog pass and a Release's per-entity pass both run an Aggregate and
// a Release directly. The catalog walk admits neither kind today, so this is the
// proof that the body withholds their values on its own, rather than only because
// the walk keeps them out.
func TestConventionsPassHandsTheGuestNoParameterValueWhateverItWalks(t *testing.T) {
	const mark = "CONVENTIONS-PASS-51f8"
	aggregate := plantedAggregate("1.0.0", mark)
	release := plantedRelease(mark)
	planted := append(plantedValues(mark), mark+"-RELEASE-VALUE")
	assertPlanted(t, aggregate+release, planted)

	collect := &FindingCollector{}
	var diag bytes.Buffer
	if err := lintConventionsFiles(t.Context(), resolveTempWorkspace(t), nil, nil,
		[]string{writeDoc(t, "aggregate.yaml", aggregate), writeDoc(t, "release.yaml", release)},
		conventionsRun{dispatch: echoDispatch(t)}, io.Discard, &diag, collect); err != nil {
		t.Fatalf("the conventions pass must run to its report: %v\n%s", err, diag.String())
	}
	byRule := echoedPayloads(t, collect)
	assertNoneReached(t, byRule, planted)
	sawAggregate, sawRelease := false, false
	for _, payloads := range byRule {
		for _, payload := range payloads {
			sawAggregate = sawAggregate || strings.Contains(payload, mark+"-CONTENT in {{ .parameters.region }}")
			sawRelease = sawRelease || strings.Contains(payload, "./dist/"+mark+"-CONTENT")
		}
	}
	if !sawAggregate || !sawRelease {
		t.Errorf("both documents must reach the guest for their absences to mean anything; aggregate=%v release=%v", sawAggregate, sawRelease)
	}
}

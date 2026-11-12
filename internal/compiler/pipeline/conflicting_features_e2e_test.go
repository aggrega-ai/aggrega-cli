// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// End-to-end proof that two Features one of which declares a conflict with the
// other halt the build wherever their attachments meet on one component, and
// only there.
//
// Every row compiles a real workspace through the real pipeline. The catalog is
// committed and shared; each row writes its own Aggregate and Release beside the
// shared Manifest, because the rows differ only in WHERE each Feature is
// attached, and that is the whole of what the rule is about.

package pipeline_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

const conflictingFeaturesDir = "testdata/conflicting-features/"

// conflictingWorkspace writes one row's Aggregate and Release beside a copy of the
// shared Manifest, and returns the input that compiles it against the committed
// catalog.
func conflictingWorkspace(t *testing.T, aggregateSpec, releaseSpec string) pipelinetest.Input {
	t.Helper()
	dir := t.TempDir()
	manifest, err := os.ReadFile(conflictingFeaturesDir + "manifest.yaml")
	if err != nil {
		t.Fatalf("read the shared manifest: %v", err)
	}
	documents := map[string]string{
		"manifest.yaml": string(manifest),
		"aggregate.yaml": "apiVersion: aggrega.ai/v1beta1\nkind: Aggregate\n" +
			"metadata:\n  name: conflict-app\n  version: \"1.0.0\"\nspec:\n" + aggregateSpec,
		"release.yaml": "apiVersion: aggrega.ai/v1beta1\nkind: Release\n" +
			"metadata:\n  name: conflict-release\n  environment: local\n  version: \"1.0.0\"\n" +
			"spec:\n  aggregate: conflict-app\n  manifest: conflicting-features-manifest\n" + releaseSpec,
	}
	for name, body := range documents {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	catalog, err := filepath.Abs(conflictingFeaturesDir + "catalog/base")
	if err != nil {
		t.Fatalf("resolve the catalog path: %v", err)
	}
	return pipelinetest.Input{
		ReleasePath:  filepath.Join(dir, "release.yaml"),
		CatalogPaths: []string{catalog},
	}
}

// buildConflicting compiles one row and returns what the build returned, failing
// the test if the compilation panicked rather than returning — a recovered panic
// would otherwise read as whatever error the recovery produced.
func buildConflicting(t *testing.T, in pipelinetest.Input) (result *pipeline.Result, err error) {
	t.Helper()
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Fatalf("the build panicked instead of returning: %v", recovered)
		}
	}()
	return pipelinetest.BuildE(t, in)
}

// conflictFindings is every finding the halt carries that reports this rule, in
// the order the build joined them.
//
// A halt joins one diagnostic per finding and may be wrapped on its way out, so
// the error tree is walked to its innermost diagnostics rather than read at the
// top: a match at the top alone would count a halt carrying two findings as one.
// A diagnostic counts only where nothing beneath it is itself a matching
// diagnostic, so one wrapping the whole halt is not mistaken for a finding.
func conflictFindings(err error) []error {
	var findings []error
	var walk func(error) bool
	walk = func(e error) bool {
		if e == nil {
			return false
		}
		if joined, ok := e.(interface{ Unwrap() []error }); ok {
			found := false
			for _, child := range joined.Unwrap() {
				if walk(child) {
					found = true
				}
			}
			return found
		}
		if walk(errors.Unwrap(e)) {
			return true
		}
		if diagnostic, ok := e.(*cerrors.CompilerError); ok &&
			errors.Is(diagnostic, analyzer.ErrConflictingFeaturesReachOneComponent()) {
			findings = append(findings, diagnostic)
			return true
		}
		return false
	}
	walk(err)
	return findings
}

// The Aggregate declares web before db in every row that holds both, so node
// order — the order a pair meeting on both is reported in — is web, then db.
const (
	webOnly = "  components:\n    web:\n      type: core/web\n"

	webAndDB = "  components:\n    web:\n      type: core/web\n    db:\n      type: core/web\n"

	webCarryingB = "  components:\n    web:\n      type: core/web\n" +
		"      features:\n        b: { type: core/conflict-b }\n"

	edgeHoldingWeb = "  boundaries:\n    edge:\n      directory: ./dist/edge\n" +
		"      components:\n        web: {}\n"
)

// TestConflictingFeaturesHaltWhereTheirReachesMeet is the reach proof: the same
// two Features, one declaring a conflict with the other, attached on every
// surface an attachment can be written on. A row halts exactly when the cascade
// lands both in one component's merged set, and each halt names both Features,
// where each was attached, and the component where they meet.
func TestConflictingFeaturesHaltWhereTheirReachesMeet(t *testing.T) {
	t.Parallel()

	for _, row := range []struct {
		name      string
		aggregate string
		release   string
		// halts is how many findings the build must carry: one per component the
		// two Features both reach, and none where they reach no common one.
		halts int
		// mentions are the values every finding must name, in the order a reader
		// meets them.
		mentions []string
		// locator is the authored key the first finding is positioned at.
		locator string
	}{
		{
			name: "both on one component in the Aggregate",
			aggregate: "  components:\n    web:\n      type: core/web\n" +
				"      features:\n        a: { type: core/conflict-a }\n        b: { type: core/conflict-b }\n",
			release: edgeHoldingWeb,
			halts:   1,
			mentions: []string{
				`"core/conflict-a"`, `attached in the Aggregate on component "web"`,
				`conflicts with "core/conflict-b"`, `on component "web"`,
			},
			locator: ".spec.components.web",
		},
		{
			name:      "one on a Release boundary, the other on a component inside it",
			aggregate: webCarryingB,
			release: "  boundaries:\n    edge:\n      directory: ./dist/edge\n" +
				"      features:\n        a: { type: core/conflict-a }\n" +
				"      components:\n        web: {}\n",
			halts: 1,
			mentions: []string{
				`"core/conflict-a"`, `attached in the Release on boundary "edge"`,
				`conflicts with "core/conflict-b"`, `attached in the Aggregate on component "web"`,
			},
			locator: ".spec.components.web",
		},
		{
			name:      "one on an outer boundary, the other on a nested boundary holding a component",
			aggregate: webOnly,
			release: "  boundaries:\n    outer:\n      directory: ./dist/outer\n" +
				"      features:\n        a: { type: core/conflict-a }\n" +
				"      boundaries:\n        inner:\n          directory: ./dist/inner\n" +
				"          features:\n            b: { type: core/conflict-b }\n" +
				"          components:\n            web: {}\n",
			halts: 1,
			mentions: []string{
				`"core/conflict-a"`, `attached in the Release on boundary "outer"`,
				`conflicts with "core/conflict-b"`, `attached in the Release on boundary "outer/inner"`,
			},
			locator: ".spec.components.web",
		},
		{
			name: "one on an Aggregate subsystem, the other on a component in it",
			aggregate: "  subsystems:\n    sub:\n" +
				"      features:\n        a: { type: core/conflict-a }\n" +
				"      components:\n        web:\n          type: core/web\n" +
				"          features:\n            b: { type: core/conflict-b }\n",
			release: edgeHoldingWeb,
			halts:   1,
			mentions: []string{
				`"core/conflict-a"`, `attached in the Aggregate on subsystem "sub"`,
				`conflicts with "core/conflict-b"`, `attached in the Aggregate on component "web"`,
			},
			locator: ".spec.subsystems.sub.components.web",
		},
		{
			name:      "one by annotation-driven expansion, the other explicitly on the component",
			aggregate: webCarryingB,
			release: "  boundaries:\n    edge:\n      directory: ./dist/edge\n" +
				"      annotations:\n        feature.aggrega.ai/conflict-a:\n          level: high\n" +
				"      components:\n        web: {}\n",
			halts: 1,
			mentions: []string{
				`"core/conflict-a"`,
				`attached by annotation-driven expansion of "feature.aggrega.ai/conflict-a"`,
				`conflicts with "core/conflict-b"`, `attached in the Aggregate on component "web"`,
			},
			locator: ".spec.components.web",
		},
		{
			name:      "one on a Release component entry, the other on the component in the Aggregate",
			aggregate: webCarryingB,
			release: "  boundaries:\n    edge:\n      directory: ./dist/edge\n" +
				"      components:\n        web:\n          features:\n            a: { type: core/conflict-a }\n",
			halts: 1,
			mentions: []string{
				`"core/conflict-a"`, `attached in the Release on the entry for component "web"`,
				`in boundary "edge"`, `conflicts with "core/conflict-b"`,
			},
			locator: ".spec.components.web",
		},
		{
			// A connection's attachment reaches the component that draws the
			// connection, and is written on the connection's entry: the place names
			// that entry rather than the component's own block, which does not hold
			// it.
			name: "one on a connection's entry in the Aggregate, the other on the component",
			aggregate: "  components:\n    web:\n      type: core/web\n" +
				"      features:\n        b: { type: core/conflict-b }\n" +
				"      uses:\n        peers:\n          db-link:\n            target: db\n" +
				"            features:\n              token: { type: core/conflict-a }\n" +
				"    db:\n      type: core/web\n",
			release: "  boundaries:\n    edge:\n      directory: ./dist/edge\n" +
				"      components:\n        web: {}\n        db: {}\n",
			halts: 1,
			mentions: []string{
				`"core/conflict-a"`, `attached in the Aggregate on the connection "db-link"`,
				`through socket "peers"`, `of component "web"`,
				`conflicts with "core/conflict-b"`, `attached in the Aggregate on component "web"`,
			},
			locator: ".spec.components.web",
		},
		{
			name: "one on a connection's entry in the Release, the other on the component",
			aggregate: "  components:\n    web:\n      type: core/web\n" +
				"      features:\n        b: { type: core/conflict-b }\n" +
				"      uses:\n        peers:\n          db-link:\n            target: db\n" +
				"    db:\n      type: core/web\n",
			release: "  boundaries:\n    edge:\n      directory: ./dist/edge\n" +
				"      components:\n        web:\n          uses:\n            peers:\n              db-link:\n" +
				"                features:\n                  token: { type: core/conflict-a }\n" +
				"        db: {}\n",
			halts: 1,
			mentions: []string{
				`"core/conflict-a"`, `attached in the Release on the connection "db-link"`,
				`through socket "peers"`, `of the entry for component "web"`, `in boundary "edge"`,
				`conflicts with "core/conflict-b"`, `attached in the Aggregate on component "web"`,
			},
			locator: ".spec.components.web",
		},
		{
			name:      "both on one boundary holding two components halts once per component",
			aggregate: webAndDB,
			release: "  boundaries:\n    edge:\n      directory: ./dist/edge\n" +
				"      features:\n        a: { type: core/conflict-a }\n        b: { type: core/conflict-b }\n" +
				"      components:\n        web: {}\n        db: {}\n",
			halts: 2,
			mentions: []string{
				`"core/conflict-a"`, `attached in the Release on boundary "edge"`,
				`conflicts with "core/conflict-b"`,
			},
			locator: ".spec.components.web",
		},
		{
			name: "a versioned attachment type is matched by the Feature's name",
			aggregate: "  components:\n    web:\n      type: core/web\n" +
				"      features:\n        a: { type: core/conflict-a }\n        b: { type: \"core/conflict-b:1.0.0\" }\n",
			release:  edgeHoldingWeb,
			halts:    1,
			mentions: []string{`"core/conflict-a"`, `conflicts with "core/conflict-b"`},
			locator:  ".spec.components.web",
		},
		{
			name: "a versioned conflictsWith entry names every version of the Feature",
			aggregate: "  components:\n    web:\n      type: core/web\n" +
				"      features:\n        b: { type: core/conflict-b }\n        c: { type: core/conflict-c }\n",
			release: edgeHoldingWeb,
			halts:   1,
			// The Feature whose list names the other leads, whatever order the
			// merged set holds the two in.
			mentions: []string{`"core/conflict-c"`, `conflicts with "core/conflict-b"`},
			locator:  ".spec.components.web",
		},
		{
			name:      "two sibling boundaries sharing no component do not halt",
			aggregate: webAndDB,
			release: "  boundaries:\n" +
				"    left:\n      directory: ./dist/left\n" +
				"      features:\n        a: { type: core/conflict-a }\n" +
				"      components:\n        web: {}\n" +
				"    right:\n      directory: ./dist/right\n" +
				"      features:\n        b: { type: core/conflict-b }\n" +
				"      components:\n        db: {}\n",
			halts: 0,
		},
		{
			name:      "an empty boundary carrying both does not halt",
			aggregate: webOnly,
			release: "  boundaries:\n" +
				"    holder:\n      directory: ./dist/holder\n" +
				"      features:\n        a: { type: core/conflict-a }\n        b: { type: core/conflict-b }\n" +
				"      boundaries:\n        nested:\n          directory: ./dist/nested\n" +
				"    edge:\n      directory: ./dist/edge\n" +
				"      components:\n        web: {}\n",
			halts: 0,
		},
		{
			name: "two Features neither of which names the other do not halt",
			aggregate: "  components:\n    web:\n      type: core/web\n" +
				"      features:\n        b: { type: core/conflict-b }\n        u: { type: core/unrelated }\n",
			release: edgeHoldingWeb,
			halts:   0,
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			result, err := buildConflicting(t, conflictingWorkspace(t, row.aggregate, row.release))

			if row.halts == 0 {
				if err != nil {
					t.Fatalf("the build halted on a workspace where no component carries both Features: %v", err)
				}
				// The build rendered, which is what says the rule was asked and
				// passed rather than the compilation stopping somewhere else first.
				if len(result.Artifacts) == 0 {
					t.Fatal("the build produced no artifact, so it proves nothing about the rule passing")
				}
				return
			}

			if err == nil {
				t.Fatalf("the build succeeded.\n" +
					"INVARIANT: two Features one of which declares a conflict with the other halt the build " +
					"when one component's merged attachment set holds both.\n" +
					"CONSEQUENCE: the declared incompatibility renders, and the build reports success.")
			}
			// The identity, not the text: a message comparison keeps compiling after
			// the condition is re-registered and silently stops matching.
			findings := conflictFindings(err)
			if len(findings) != row.halts {
				t.Fatalf("the halt carries %d finding(s) of this rule; want %d, one per component both reach.\ngot: %v",
					len(findings), row.halts, err)
			}
			for i, finding := range findings {
				message := finding.Error()
				cursor := 0
				for _, want := range row.mentions {
					at := strings.Index(message[cursor:], want)
					if at < 0 {
						t.Errorf("finding %d does not name %s after what precedes it.\n"+
							"INVARIANT: the halt names both Features, where each was attached, and the component "+
							"where they meet.\ngot: %s", i, want, message)
						break
					}
					cursor += at + len(want)
				}
			}
			if !strings.Contains(findings[0].Error(), row.locator) {
				t.Errorf("the first finding is not positioned at %s; got: %s", row.locator, findings[0].Error())
			}
			// A pair meeting on several components reports each, in the order the
			// graph holds its nodes.
			if row.halts == 2 {
				if !strings.Contains(findings[0].Error(), `on component "web"`) ||
					!strings.Contains(findings[1].Error(), `on component "db"`) {
					t.Errorf("the findings are not one per component in node order (web, then db):\n%v", err)
				}
			}
		})
	}
}

// TestConflictingFeaturesHaltTheValidationPathAlike proves the verdict does not
// depend on which entry point compiles the workspace: the one the validation
// surfaces use reports the same condition, with the same text, as a build halts
// on. That entry carries a rule's findings as structured diagnostics rather than
// as its returned error, so the finding is read from there.
func TestConflictingFeaturesHaltTheValidationPathAlike(t *testing.T) {
	t.Parallel()
	in := conflictingWorkspace(t,
		"  components:\n    web:\n      type: core/web\n"+
			"      features:\n        a: { type: core/conflict-a }\n        b: { type: core/conflict-b }\n",
		edgeHoldingWeb)

	_, buildErr := buildConflicting(t, in)
	built := conflictFindings(buildErr)
	if len(built) != 1 {
		t.Fatalf("build path: want one conflicting-Features finding; got %v", buildErr)
	}

	t4, err := pipelinetest.Tier4E(t, in)
	if err != nil {
		t.Fatalf("validation path: the run failed before its findings could be read: %v", err)
	}
	if t4 == nil || t4.Analysis == nil {
		t.Fatal("validation path: the run returned no analysis to read the finding from")
	}
	var validated []string
	for i := range t4.Analysis.Errors {
		finding := &t4.Analysis.Errors[i]
		if errors.Is(finding.Sentinel, analyzer.ErrConflictingFeaturesReachOneComponent()) {
			validated = append(validated, finding.ToCompilerError().Error())
		}
	}
	if len(validated) != 1 {
		t.Fatalf("validation path: want one conflicting-Features finding; got %d: %+v",
			len(validated), t4.Analysis.Errors)
	}
	if built[0].Error() != validated[0] {
		t.Errorf("the two paths must present one finding identically:\n  build:      %q\n  validation: %q",
			built[0].Error(), validated[0])
	}
}

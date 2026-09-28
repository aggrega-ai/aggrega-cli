// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// Release-scope single-source parity, CLI leg. validateRelease /
// renderReleaseValidation render the SAME shared core the MCP release scope and
// the serve daemon render — compilerops.ResolveClosure + compilerops.Validate.
// These tests prove the CLI faithfully surfaces that core's verdict and its
// underlying diagnostic texts, and that the detailed/concise verbosity governs
// output VOLUME only, never the verdict. The oracle is a direct compilerops call
// over the same resolved inputs validateRelease builds internally; since the CLI,
// the MCP payload, and serve each equal the core over the same fixture, they equal
// each other.
//
// The fixture YAML mirrors the MCP leg's cases deliberately (independent
// construction per leg catches a drift in any one leg rather than masking it).

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

func cliFullstackCatalogDir(t *testing.T) string {
	t.Helper()
	cwd, err := filepath.Abs(".")
	if err != nil {
		t.Fatalf("abs cwd: %v", err)
	}
	moduleRoot := filepath.Dir(filepath.Dir(cwd))
	return filepath.Join(moduleRoot, "internal", "compiler", "testdata", "fullstack-app", "catalog")
}

type cliReleaseParityCase struct {
	name      string
	aggregate string
	manifest  string
	release   string
	overlay   map[string]string
}

const cliBackendAggregate = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: parity-app
  version: "1.0.0"
spec:
  components:
    backend:
      type: core/compute/api-server
`

const cliCleanManifest = `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: parity-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/compute/api-server: helm-api-server-synthesized-with-artifacts:1.0.0
`

const cliEmptyManifest = `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: parity-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type: {}
`

const cliDefaultRelease = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: parity-release
  environment: test
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    app:
      directory: ./dist/app
      components:
        backend:
          infra:
            port: 8080
`

const cliEmptyBoundaryRelease = `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: parity-release
  environment: test
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    app:
      directory: ./dist/app
      components: {}
`

const cliExtendsOverlayComponent = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/compute/api-server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  extends: local/non-existent-base:1.0.0
  inject: ["env"]
  schema: inherited
`

const cliPillarOverlayComponent = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/compute/api-server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  inject: ["env"]
  schema:
    infra:
      storage:
        $ref: "Nonexistent#/schemas/Storage"
    provides:
      host: { type: string, required: true }
      service_port: { type: string, required: true }
`

const cliResolveFailingAggregate = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: parity-app
  version: "1.0.0"
spec:
  parameters:
    database_url:
      type: string
      required: true
  components: {}
`

const cliInputKindManifest = `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: parity-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/compute/api-server: api-sourceonly:1.0.0
`

const cliInputKindRecipe = `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: api-sourceonly
  version: "1.0.0"
spec:
  capability: sourced-only-cap
  implements: core/compute/api-server
  provides:
    host: "{{ .context.instance_id }}.svc"
    service_port: "8080"
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: 1
    containers:
      main:
        image: x:latest
`

const cliInputKindPlugin = `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: sourced-only-renderer
  pluginType: provider
  version: "1.0.0"
spec:
  abiVersion: v1alpha1
  exports:
    - name: render_node
    - name: assemble_boundary
  provider:
    capabilities:
      - sourced-only-cap
    contract:
      inputKinds:
        sourced: true
      boundaryAssembly: nest
`

func cliReleaseParityCases() []cliReleaseParityCase {
	return []cliReleaseParityCase{
		{name: "clean", aggregate: cliBackendAggregate, manifest: cliCleanManifest},
		{name: "extends-broken", aggregate: cliBackendAggregate, manifest: cliCleanManifest,
			overlay: map[string]string{"components/api-server.yaml": cliExtendsOverlayComponent}},
		{name: "pillar-invalid", aggregate: cliBackendAggregate, manifest: cliCleanManifest,
			overlay: map[string]string{"components/api-server.yaml": cliPillarOverlayComponent}},
		{name: "resolve-failing", aggregate: cliResolveFailingAggregate, manifest: cliEmptyManifest,
			release: cliEmptyBoundaryRelease},
		{name: "resolution-incomplete", aggregate: cliBackendAggregate, manifest: cliEmptyManifest},
		{name: "input-kind-capability-failing", aggregate: cliBackendAggregate, manifest: cliInputKindManifest,
			overlay: map[string]string{
				"recipes/api-sourceonly.yaml":        cliInputKindRecipe,
				"plugins/sourced-only-renderer.yaml": cliInputKindPlugin,
			}},
	}
}

func writeCLIReleaseParityWorkspace(t *testing.T, c cliReleaseParityCase) (root string, dirs []string) {
	t.Helper()
	root = t.TempDir()
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), c.aggregate)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), c.manifest)
	rel := c.release
	if rel == "" {
		rel = cliDefaultRelease
	}
	writeFixture(t, filepath.Join(root, "release.yaml"), rel)
	dirs = []string{cliFullstackCatalogDir(t)}
	if len(c.overlay) > 0 {
		overlay := filepath.Join(root, "overlay")
		for rel, body := range c.overlay {
			writeFixture(t, filepath.Join(overlay, rel), body)
		}
		dirs = append([]string{overlay}, dirs...)
	}
	return root, dirs
}

// cliCoreUnderlyingDiagTexts is the independent oracle: the raw .Error() text of
// every blocking finding in a ValidateResult, in gate order. The prefixes the CLI
// renderer adds (`  extends: `, `  input-kind: `) are deliberately NOT applied
// here — the invariant these tests guard is that each finding's own message
// reaches the operator verbatim, whichever stream the CLI routes it to.
func cliCoreUnderlyingDiagTexts(vr *compilerops.ValidateResult) []string {
	var out []string
	for _, e := range vr.ClosureExtendsErrors {
		out = append(out, e.Error())
	}
	if vr.PillarError != nil {
		out = append(out, vr.PillarError.Error())
	}
	// The verdict-plugin trust gate rides the verdict error rather than diagOut,
	// exactly as the pillar gate does, so it belongs in the underlying set and not
	// in the diagOut-routed set below.
	if vr.VerdictTrustError != nil {
		out = append(out, vr.VerdictTrustError.Error())
	}
	if vr.Tier4 != nil {
		for _, ve := range vr.Tier4.ResolveDiagnostics {
			out = append(out, ve.Error())
		}
		if vr.Tier4.Analysis != nil {
			for _, ae := range vr.Tier4.Analysis.Errors {
				out = append(out, ae.Error())
			}
		}
	}
	if vr.RunErr != nil && !isCancellation(vr.RunErr) &&
		!runErrPublishedPerViolation(vr.RunErr, tier4ResolveDiagnostics(vr)) {
		out = append(out, vr.RunErr.Error())
	}
	for _, f := range vr.ResolutionFindings {
		out = append(out, f.Error())
	}
	for _, e := range vr.InputKindErrors {
		out = append(out, e.Error())
	}
	return out
}

// tier4ResolveDiagnostics reads the resolver-class per-violation slice off a
// result that may have stopped before the pipeline ran.
//
// Both oracles in this file call the renderer's own phase-error predicate rather
// than writing a fourth copy of it. What they exist to check is which findings
// reach which stream and whether each one's own text survives verbatim; a second
// spelling of the predicate here would report a disagreement about the predicate
// as a routing fault, which it is not. The predicate's own two directions are
// pinned by the fall-back test in this package, which drives a workspace built
// for each of them.
func tier4ResolveDiagnostics(vr *compilerops.ValidateResult) []*resolver.ResolverError {
	if vr.Tier4 == nil {
		return nil
	}
	return vr.Tier4.ResolveDiagnostics
}

// cliDiagOutRoutedTexts is the subset of underlying texts the detailed renderer
// writes to diagOut (extends, pipeline diagnostics, input-kind). The pillar error
// is deliberately excluded — the renderer returns it as the verdict error and
// writes nothing to diagOut for it.
func cliDiagOutRoutedTexts(vr *compilerops.ValidateResult) []string {
	var out []string
	for _, e := range vr.ClosureExtendsErrors {
		out = append(out, e.Error())
	}
	if vr.Tier4 != nil {
		for _, ve := range vr.Tier4.ResolveDiagnostics {
			out = append(out, ve.Error())
		}
		if vr.Tier4.Analysis != nil {
			for _, ae := range vr.Tier4.Analysis.Errors {
				out = append(out, ae.Error())
			}
		}
	}
	if vr.RunErr != nil && !isCancellation(vr.RunErr) &&
		!runErrPublishedPerViolation(vr.RunErr, tier4ResolveDiagnostics(vr)) {
		out = append(out, vr.RunErr.Error())
	}
	for _, f := range vr.ResolutionFindings {
		out = append(out, f.Error())
	}
	for _, e := range vr.InputKindErrors {
		out = append(out, e.Error())
	}
	return out
}

// cliOracleValidate mirrors validateRelease's own input construction and returns
// the shared core's result — the single-source oracle the CLI output is measured
// against.
func cliOracleValidate(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, releasePath string, catalogFlagDirs []string) *compilerops.ValidateResult {
	t.Helper()
	wsRoot := rw.Root()
	wsCfg := rw.Config()
	cacheDir := wsCfg.ResolvedArtifactCacheDir()
	dirs := rw.ResolveCatalogDirs(releasePath, catalogFlagDirs)
	lazyView := catalog.NewLazyLookup(wsRoot, cacheDir, dirs, nil)
	resolved, cerr := compilerops.ResolveClosure(context.Background(), compilerops.ClosureInputs{
		View:            lazyView,
		ReleasePath:     releasePath,
		WorkspaceRoot:   wsRoot,
		WorkspaceConfig: wsCfg,
	})
	if cerr != nil {
		t.Fatalf("oracle ResolveClosure failed: %v", cerr)
	}
	return compilerops.Validate(context.Background(), compilerops.ValidateInputs{
		View:            lazyView,
		Closure:         resolved.Closure,
		Release:         resolved.Release,
		ReleasePath:     releasePath,
		Manifest:        resolved.Manifest,
		AggBytes:        resolved.AggBytes,
		AggregatePath:   catalog.RelativeToWorkspace(wsRoot, resolved.AggregatePath),
		WorkspaceRoot:   wsRoot,
		WorkspaceConfig: wsCfg,
		CompilerVersion: provenanceCompilerVersion(),
	})
}

func sameErr(a, b error) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a == nil {
		return true
	}
	return a.Error() == b.Error()
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// TestValidateReleaseCoreParity proves the CLI release verb surfaces the shared
// core faithfully across all six fixture cases, and that verbosity governs volume
// only: the verdict error is byte-identical between detailed and concise, every
// core finding's own text reaches the operator, and the concise form reduces
// diagOut volume without emitting advisory lines.
func TestValidateReleaseCoreParity(t *testing.T) {
	for _, c := range cliReleaseParityCases() {
		c := c
		t.Run(c.name, func(t *testing.T) {
			root, dirs := writeCLIReleaseParityWorkspace(t, c)
			rw, err := workspaceconfig.Resolve(root, root)
			if err != nil {
				t.Fatalf("resolve workspace: %v", err)
			}
			releasePath := filepath.Join(root, "release.yaml")

			var diagDet, outDet bytes.Buffer
			errDet := validateRelease(context.Background(), rw, releasePath, dirs, compilerops.VerbosityDetailed, false, &diagDet, &outDet)
			var diagCon, outCon bytes.Buffer
			errCon := validateRelease(context.Background(), rw, releasePath, dirs, compilerops.VerbosityConcise, false, &diagCon, &outCon)

			vr := cliOracleValidate(t, rw, releasePath, dirs)
			underlying := cliCoreUnderlyingDiagTexts(vr)
			coreClean := len(underlying) == 0

			// Fixture sanity: each case exercises exactly its class.
			if c.name == "clean" {
				if !coreClean {
					t.Fatalf("clean fixture is not clean: core findings %#v", underlying)
				}
			} else if coreClean {
				t.Fatalf("case %q produced no core findings; the fixture no longer exercises its class", c.name)
			}

			// Verdict correctness against the oracle.
			if (errDet == nil) != coreClean {
				t.Fatalf("detailed verdict diverges from core: err=%v, coreClean=%v, findings=%#v", errDet, coreClean, underlying)
			}
			// Verbosity is verdict-invariant: the two forms return the identical error.
			if !sameErr(errDet, errCon) {
				t.Fatalf("verbosity leaked into the verdict:\n detailed: %q\n concise:  %q", errText(errDet), errText(errCon))
			}

			// Content faithfulness: every core finding's own text reaches the operator
			// (diagOut for the streamed classes, the verdict error for the pillar gate).
			combinedDet := diagDet.String() + errText(errDet)
			for _, txt := range underlying {
				if !strings.Contains(combinedDet, txt) {
					t.Fatalf("detailed output dropped core finding %q\n diagOut:\n%s\n err: %s", txt, diagDet.String(), errText(errDet))
				}
			}

			// The concise form never emits advisory notice lines.
			if strings.Contains(diagCon.String(), "notice:") {
				t.Errorf("concise diagOut carried an advisory line:\n%s", diagCon.String())
			}

			if coreClean {
				return
			}

			// Volume reduction: for classes the renderer streams to diagOut, the
			// concise form differs from the detailed form and collapses to a count +
			// first summary. The pillar gate writes nothing to diagOut in either form
			// (its text rides the verdict error), so it is exempt from the diagOut
			// volume check.
			if len(cliDiagOutRoutedTexts(vr)) > 0 {
				if diagDet.String() == diagCon.String() {
					t.Errorf("verbosity did not change diagOut volume:\n%s", diagDet.String())
				}
				if !strings.Contains(diagCon.String(), "diagnostic(s); first:") {
					t.Errorf("concise diagOut is not the count+first summary:\n%s", diagCon.String())
				}
				routed := cliDiagOutRoutedTexts(vr)
				if len(routed) >= 2 && strings.Contains(diagCon.String(), routed[1]) {
					t.Errorf("concise diagOut leaked a non-first finding (volume not reduced):\n%s", diagCon.String())
				}
			}

			// Detailed diagOut carries every streamed finding verbatim.
			for _, txt := range cliDiagOutRoutedTexts(vr) {
				if !strings.Contains(diagDet.String(), txt) {
					t.Errorf("detailed diagOut dropped streamed finding %q:\n%s", txt, diagDet.String())
				}
			}
		})
	}
}

// cliFailingAssertionOverlayComponent declares a Component whose build-time
// assertion never holds, so every instance of it fails the assertion phase. It is
// otherwise the shared catalog's own contract for this type, so the fault this
// fixture carries is the assertion and nothing else.
const cliFailingAssertionOverlayComponent = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/compute/api-server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  inject: ["env"]
  assertions:
    replicas-are-explicit: '{{ fail "this contract requires replicas to be declared explicitly" }}'
  schema:
    infra:
      port: { type: integer, default: 8080 }
      replicas: { type: integer, default: 1 }
    provides:
      host: { type: string, required: true }
      service_port: { type: string, required: true }
`

// TestRenderReleaseValidationPublishesPhaseErrorBesidePerViolationFindings pins
// both directions of the closure-level fall-back for a pre-render phase error
// that carries no per-violation structure of its own. They pull in opposite
// directions, and a rule satisfying only one is wrong in the other:
//
//   - a phase error must still reach the operator when per-violation findings were
//     published alongside it but do not describe it — and this verb flattens each
//     diagnostic with its cause chain precisely because that frame carries the
//     offending entity, the assertion, and the assertion's own author-written
//     message, so dropping the line drops all of it;
//   - a run error that WAS published finding by finding must not also be published
//     as a closure-level line, which would report one failure twice.
//
// The first row needs a workspace carrying two independent faults at once: an
// instance the Manifest maps no recipe for, whose finding travels out of the
// pipeline on the per-violation channel without failing the run, and a Component
// contract whose build-time assertion fails, which is a phase error with nothing
// per-violation about it. No other case in this package carries two faults
// together.
func TestRenderReleaseValidationPublishesPhaseErrorBesidePerViolationFindings(t *testing.T) {
	cases := []struct {
		name string
		// fixture is the workspace to validate.
		fixture cliReleaseParityCase
		// wantFallback is whether the run error is expected to reach diagOut as a
		// closure-level line of its own.
		wantFallback bool
		// wantInstanceNamed is the instance a per-violation finding must name, or
		// empty where the fixture declares no component at all.
		wantInstanceNamed string
	}{
		{
			name: "a phase error beside per-violation findings that do not describe it",
			fixture: cliReleaseParityCase{
				aggregate: cliBackendAggregate,
				manifest:  cliEmptyManifest,
				overlay: map[string]string{
					"components/api-server.yaml": cliFailingAssertionOverlayComponent,
				},
			},
			wantFallback:      true,
			wantInstanceNamed: `"backend"`,
		},
		{
			name: "a resolver-class failure already published finding by finding",
			fixture: cliReleaseParityCase{
				aggregate: cliResolveFailingAggregate,
				manifest:  cliEmptyManifest,
				release:   cliEmptyBoundaryRelease,
			},
			wantFallback: false,
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			root, dirs := writeCLIReleaseParityWorkspace(t, c.fixture)
			rw, err := workspaceconfig.Resolve(root, root)
			if err != nil {
				t.Fatalf("resolve workspace: %v", err)
			}
			releasePath := filepath.Join(root, "release.yaml")

			// The oracle is the same core over the same resolved inputs
			// validateRelease builds internally, so the expected fall-back text is
			// derived from the run error rather than transcribed from it.
			vr := cliOracleValidate(t, rw, releasePath, dirs)

			// Fixture sanity, stated per axis so a fixture that stopped carrying one
			// of its two faults fails here rather than passing the assertions below
			// for the wrong reason.
			if vr.RunErr == nil {
				t.Fatalf("the fixture produced no run error, so it no longer exercises this case; ValidateResult=%+v", vr)
			}
			if len(tier4ResolveDiagnostics(vr)) == 0 {
				t.Fatalf("the fixture produced no per-violation finding, so it no longer exercises this case; ValidateResult=%+v", vr)
			}

			var diag, out bytes.Buffer
			if verdict := renderReleaseValidation(vr, nil, nil, releasePath, compilerops.VerbosityDetailed, &diag, &out); verdict == nil {
				t.Fatalf("the fixture validated clean, so neither direction is exercised\n diagOut:\n%s", diag.String())
			}

			fallback := cerrors.CauseChain(vr.RunErr)
			wantLine := fmt.Sprintf("  %s\n", fallback)

			// Occurrences the other streams already contribute, so the count below
			// measures the fall-back alone even where a stream happens to flatten to
			// the same text — which is what a single-violation run does.
			base := 0
			for _, ve := range tier4ResolveDiagnostics(vr) {
				if cerrors.CauseChain(ve) == fallback {
					base++
				}
			}
			if vr.Tier4 != nil && vr.Tier4.Analysis != nil {
				for _, ae := range vr.Tier4.Analysis.Errors {
					if cerrors.CauseChain(ae) == fallback {
						base++
					}
				}
			}
			for _, f := range vr.ResolutionFindings {
				if cerrors.CauseChain(f) == fallback {
					base++
				}
			}

			want := base
			if c.wantFallback {
				want++
			}
			if got := strings.Count(diag.String(), wantLine); got != want {
				t.Fatalf("the run error was written %d time(s) as a closure-level line; want %d (%d of them from the other streams)\n line: %q\n diagOut:\n%s",
					got, want, base, wantLine, diag.String())
			}

			if c.wantInstanceNamed != "" && !strings.Contains(diag.String(), c.wantInstanceNamed) {
				t.Fatalf("no diagnostic names %s, so the per-violation half of this fixture is not being published\n diagOut:\n%s",
					c.wantInstanceNamed, diag.String())
			}
		})
	}
}

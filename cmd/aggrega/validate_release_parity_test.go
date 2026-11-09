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
	"errors"
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
	// subject is the class of finding the case exists to exercise, which the core
	// result must carry for the case to be exercising it. The empty class is the
	// clean case's, which must carry no finding at all.
	subject cliFindingClass
}

// cliFindingClass names a class of blocking finding by the field of the core
// result that carries it.
type cliFindingClass string

const (
	cliClassNone      cliFindingClass = ""
	cliClassExtends   cliFindingClass = "extends"
	cliClassPillar    cliFindingClass = "pillar"
	cliClassResolver  cliFindingClass = "resolver"
	cliClassInputKind cliFindingClass = "input-kind"
)

// cliCoreCarriesClass reports whether the core result carries at least one
// finding of class. A class this does not know is never carried, so a case
// naming one fails rather than passing unchecked.
//
// The resolver class is the per-violation channel, which carries a required
// parameter left unfilled and an instance the Manifest maps no recipe for alike.
func cliCoreCarriesClass(vr *compilerops.ValidateResult, class cliFindingClass) bool {
	switch class {
	case cliClassExtends:
		return len(vr.ClosureExtendsErrors) > 0
	case cliClassPillar:
		return vr.PillarError != nil
	case cliClassResolver:
		return len(tier4ResolveDiagnostics(vr)) > 0
	case cliClassInputKind:
		return len(vr.InputKindErrors) > 0
	}
	return false
}

const cliBackendAggregate = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: parity-app
  version: "1.0.0"
spec:
  components:
    backend:
      type: core/compute/api-server
`

const cliCleanManifest = `apiVersion: aggrega.ai/v1beta1
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

const cliEmptyManifest = `apiVersion: aggrega.ai/v1beta1
kind: Manifest
metadata:
  name: parity-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type: {}
`

const cliDefaultRelease = `apiVersion: aggrega.ai/v1beta1
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

const cliEmptyBoundaryRelease = `apiVersion: aggrega.ai/v1beta1
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

const cliExtendsOverlayComponent = `apiVersion: aggrega.ai/v1beta1
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

const cliPillarOverlayComponent = `apiVersion: aggrega.ai/v1beta1
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

const cliResolveFailingAggregate = `apiVersion: aggrega.ai/v1beta1
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

const cliInputKindManifest = `apiVersion: aggrega.ai/v1beta1
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

const cliInputKindRecipe = `apiVersion: aggrega.ai/v1beta1
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
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    replicas: 1
    containers:
      main:
        image: x:latest
`

const cliInputKindPlugin = `apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata:
  name: sourced-only-renderer
  pluginType: provider
  version: "1.0.0"
spec:
  abiVersion: v1beta1
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
		{name: "clean", aggregate: cliBackendAggregate, manifest: cliCleanManifest, subject: cliClassNone},
		{name: "extends-broken", aggregate: cliBackendAggregate, manifest: cliCleanManifest,
			overlay: map[string]string{"components/api-server.yaml": cliExtendsOverlayComponent},
			subject: cliClassExtends},
		{name: "pillar-invalid", aggregate: cliBackendAggregate, manifest: cliCleanManifest,
			overlay: map[string]string{"components/api-server.yaml": cliPillarOverlayComponent},
			subject: cliClassPillar},
		{name: "resolve-failing", aggregate: cliResolveFailingAggregate, manifest: cliEmptyManifest,
			release: cliEmptyBoundaryRelease, subject: cliClassResolver},
		{name: "resolution-incomplete", aggregate: cliBackendAggregate, manifest: cliEmptyManifest,
			subject: cliClassResolver},
		// This case carries two findings, and its subject is the input-kind one. The
		// Plugin it declares has no module beside it, so asking the provider for the
		// name it gives the node's resources fails as well, ahead of the Recipe
		// assertions; that finding rides the run error. The subject check below is
		// what keeps the case exercising its class should the input-kind gate stop
		// reporting while the other finding stays.
		{name: "input-kind-capability-failing", aggregate: cliBackendAggregate, manifest: cliInputKindManifest,
			overlay: map[string]string{
				"recipes/api-sourceonly.yaml":        cliInputKindRecipe,
				"plugins/sourced-only-renderer.yaml": cliInputKindPlugin,
			},
			subject: cliClassInputKind},
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

// cliFinding is one blocking finding of a ValidateResult as the oracle reads it:
// its one-line text, which names it in a failure message, and the facts the
// operator must find in the output for its own text to have reached them whole.
type cliFinding struct {
	text  string
	facts []string
}

// verbatimFinding is a finding whose own one-line text is what reaches the
// operator, so that text is its one fact.
func verbatimFinding(err error) cliFinding {
	return cliFinding{text: err.Error(), facts: []string{err.Error()}}
}

// streamedFinding is a finding the detailed renderer writes to diagOut, whose
// facts are read off it by cliFindingFacts.
func streamedFinding(err error) cliFinding {
	return cliFinding{text: err.Error(), facts: cliFindingFacts(err)}
}

// cliFindingFacts is what a finding the detailed renderer writes to diagOut must
// carry to the operator.
//
// A finding that is not itself a diagnostic envelope is its own text, verbatim.
// An envelope is different, because its Error is the one-line form a log takes —
// its category, its location and its message, without the condition's short code
// and without the cause beneath it — while diagOut prints it as a build prints it:
// the head carrying the category and the short code, the message beneath, and
// the cause beneath that. Its one-line text is therefore not something the
// detailed form prints, and the facts it carries are what the operator must find
// instead: for each envelope along its chain, the category, the location and the
// message; where the chain forks into joined errors, the facts of each of them;
// and the text of the cause the chain ends in.
//
// The reading is written from the envelope's own accessors rather than by calling
// the renderer, and that is deliberate: the comparison is between what a finding
// carries and what was printed, and a reading that asked the renderer would
// compare the renderer with itself. A wrapping frame between two envelopes adds no
// fact of its own here, because the frame below it is what the output reports.
func cliFindingFacts(err error) []string {
	if _, ok := err.(*cerrors.CompilerError); !ok {
		return []string{err.Error()}
	}
	var facts []string
	for cur := err; cur != nil; {
		if ce, ok := cur.(*cerrors.CompilerError); ok {
			if c := ce.Category(); c != "" {
				facts = append(facts, "["+string(c))
			}
			if src := ce.Source().String(); src != "" {
				facts = append(facts, src)
			}
			if msg := ce.Message(); msg != "" {
				facts = append(facts, msg)
			}
			cur = ce.Unwrap()
			continue
		}
		if joined, ok := cur.(interface{ Unwrap() []error }); ok {
			for _, sub := range joined.Unwrap() {
				facts = append(facts, cliFindingFacts(sub)...)
			}
			break
		}
		next := errors.Unwrap(cur)
		if !cliChainReachesEnvelope(next) {
			if txt := cur.Error(); txt != "" {
				facts = append(facts, txt)
			}
			break
		}
		cur = next
	}
	return facts
}

// cliChainReachesEnvelope reports whether the chain beneath err, followed one
// frame at a time, still reaches an envelope or a fork into joined errors — that
// is, whether a frame further down reports the cause, so the frame at hand is not
// the one whose text ends the chain.
func cliChainReachesEnvelope(err error) bool {
	for cur := err; cur != nil; cur = errors.Unwrap(cur) {
		if _, ok := cur.(*cerrors.CompilerError); ok {
			return true
		}
		if _, ok := cur.(interface{ Unwrap() []error }); ok {
			return true
		}
	}
	return false
}

// cliMissingFacts returns the facts of f that output does not contain, in the
// order f carries them.
func cliMissingFacts(output string, f cliFinding) []string {
	var missing []string
	for _, fact := range f.facts {
		if !strings.Contains(output, fact) {
			missing = append(missing, fact)
		}
	}
	return missing
}

// cliFindingTexts is the one-line text of each finding, for a failure message.
func cliFindingTexts(findings []cliFinding) []string {
	out := make([]string, 0, len(findings))
	for _, f := range findings {
		out = append(out, f.text)
	}
	return out
}

// cliCoreUnderlyingFindings is the independent oracle: every blocking finding in
// a ValidateResult, in gate order. The prefixes the CLI renderer adds
// (`  extends: `, `  input-kind: `) are deliberately NOT applied here — the
// invariant these tests guard is that each finding's own text reaches the
// operator whole, whichever stream the CLI routes it to.
//
// The pillar and verdict-plugin trust gates ride the verdict error, whose text
// carries theirs verbatim, so each of those is read verbatim. Every other finding
// is written to diagOut and is read by cliFindingFacts.
func cliCoreUnderlyingFindings(vr *compilerops.ValidateResult) []cliFinding {
	var out []cliFinding
	for _, e := range vr.ClosureExtendsErrors {
		out = append(out, streamedFinding(e))
	}
	if vr.PillarError != nil {
		out = append(out, verbatimFinding(vr.PillarError))
	}
	// The verdict-plugin trust gate rides the verdict error rather than diagOut,
	// exactly as the pillar gate does, so it belongs in the underlying set and not
	// in the diagOut-routed set below.
	if vr.VerdictTrustError != nil {
		out = append(out, verbatimFinding(vr.VerdictTrustError))
	}
	if vr.Tier4 != nil {
		for _, ve := range vr.Tier4.ResolveDiagnostics {
			out = append(out, streamedFinding(ve))
		}
		if vr.Tier4.Analysis != nil {
			for _, ae := range vr.Tier4.Analysis.Errors {
				out = append(out, streamedFinding(ae))
			}
		}
	}
	if vr.RunErr != nil && !isCancellation(vr.RunErr) &&
		!runErrPublishedPerViolation(vr.RunErr, tier4ResolveDiagnostics(vr)) {
		out = append(out, streamedFinding(vr.RunErr))
	}
	for _, f := range vr.ResolutionFindings {
		out = append(out, streamedFinding(f))
	}
	for _, e := range vr.InputKindErrors {
		out = append(out, streamedFinding(e))
	}
	return out
}

// tier4ResolveDiagnostics reads the resolver-class per-violation slice off a
// result that may have stopped before the pipeline ran.
//
// Both oracles in this file call the renderer's own phase-error predicate rather
// than writing a fourth copy of it. What they exist to check is which findings
// reach which stream and whether each one's own text survives whole; a second
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

// cliDiagOutRoutedFindings is the subset of underlying findings the detailed
// renderer writes to diagOut (extends, pipeline diagnostics, input-kind). The
// pillar error is deliberately excluded — the renderer returns it as the verdict
// error and writes nothing to diagOut for it.
func cliDiagOutRoutedFindings(vr *compilerops.ValidateResult) []cliFinding {
	var out []cliFinding
	for _, e := range vr.ClosureExtendsErrors {
		out = append(out, streamedFinding(e))
	}
	if vr.Tier4 != nil {
		for _, ve := range vr.Tier4.ResolveDiagnostics {
			out = append(out, streamedFinding(ve))
		}
		if vr.Tier4.Analysis != nil {
			for _, ae := range vr.Tier4.Analysis.Errors {
				out = append(out, streamedFinding(ae))
			}
		}
	}
	if vr.RunErr != nil && !isCancellation(vr.RunErr) &&
		!runErrPublishedPerViolation(vr.RunErr, tier4ResolveDiagnostics(vr)) {
		out = append(out, streamedFinding(vr.RunErr))
	}
	for _, f := range vr.ResolutionFindings {
		out = append(out, streamedFinding(f))
	}
	for _, e := range vr.InputKindErrors {
		out = append(out, streamedFinding(e))
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
	dirs := stackFor(t, rw, releasePath, catalogFlagDirs)
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
			errDet := validateRelease(context.Background(), rw, releasePath, dirs, compilerops.VerbosityDetailed, false, false, &diagDet, &outDet)
			var diagCon, outCon bytes.Buffer
			errCon := validateRelease(context.Background(), rw, releasePath, dirs, compilerops.VerbosityConcise, false, false, &diagCon, &outCon)

			vr := cliOracleValidate(t, rw, releasePath, dirs)
			underlying := cliCoreUnderlyingFindings(vr)
			coreClean := len(underlying) == 0

			// Fixture sanity: each case exercises its own class. A finding of some
			// other class does not stand in for it, since the oracle below is read off
			// the same core result and would simply hold one finding fewer.
			if c.subject == cliClassNone {
				if !coreClean {
					t.Fatalf("clean fixture is not clean: core findings %#v", cliFindingTexts(underlying))
				}
			} else if !cliCoreCarriesClass(vr, c.subject) {
				t.Fatalf("case %q carries no %s finding, which is its subject; the fixture no longer exercises its class (core findings %#v)",
					c.name, c.subject, cliFindingTexts(underlying))
			}

			// Verdict correctness against the oracle.
			if (errDet == nil) != coreClean {
				t.Fatalf("detailed verdict diverges from core: err=%v, coreClean=%v, findings=%#v", errDet, coreClean, cliFindingTexts(underlying))
			}
			// Verbosity is verdict-invariant: the two forms return the identical error.
			if !sameErr(errDet, errCon) {
				t.Fatalf("verbosity leaked into the verdict:\n detailed: %q\n concise:  %q", errText(errDet), errText(errCon))
			}

			// Content faithfulness: every core finding's own text reaches the operator
			// (diagOut for the streamed classes, the verdict error for the pillar gate).
			combinedDet := diagDet.String() + errText(errDet)
			for _, f := range underlying {
				if missing := cliMissingFacts(combinedDet, f); len(missing) > 0 {
					t.Fatalf("detailed output dropped core finding %q: missing %q\n diagOut:\n%s\n err: %s", f.text, missing, diagDet.String(), errText(errDet))
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
			routed := cliDiagOutRoutedFindings(vr)
			if len(routed) > 0 {
				if diagDet.String() == diagCon.String() {
					t.Errorf("verbosity did not change diagOut volume:\n%s", diagDet.String())
				}
				if !strings.Contains(diagCon.String(), "diagnostic(s); first:") {
					t.Errorf("concise diagOut is not the count+first summary:\n%s", diagCon.String())
				}
				// A non-first finding leaked when every fact it carries is in the
				// concise form.
				if len(routed) >= 2 && len(cliMissingFacts(diagCon.String(), routed[1])) == 0 {
					t.Errorf("concise diagOut leaked a non-first finding (volume not reduced):\n%s", diagCon.String())
				}
			}

			// Detailed diagOut carries every streamed finding whole.
			for _, f := range routed {
				if missing := cliMissingFacts(diagDet.String(), f); len(missing) > 0 {
					t.Errorf("detailed diagOut dropped streamed finding %q: missing %q\n%s", f.text, missing, diagDet.String())
				}
			}
		})
	}
}

// TestReleaseFindingFactsNameEveryDroppedFact pins the reading the parity test
// above compares the detailed form against, in both directions: every fact a
// finding carries is found in the detailed rendering of it, and a rendering with
// any one of those facts removed is reported as missing exactly that fact. A
// reading that returned fewer facts, or none, would pass the parity test while
// checking nothing, so each row also states the facts it expects.
func TestReleaseFindingFactsNameEveryDroppedFact(t *testing.T) {
	nodeAt := cerrors.SourceLocation{Instance: "backend"}
	recipeAt := cerrors.SourceLocation{File: "recipes/api-sourceonly.yaml"}
	notOnMachine := errors.New("the module serving this provider is not on this machine")
	inner := cerrors.Wrap(notOnMachine, cerrors.CatalogError, recipeAt, "reading the provider's plugin")

	cases := []struct {
		name      string
		finding   error
		wantFacts []string
	}{
		{
			name:    "an envelope over the cause it ends in",
			finding: cerrors.Wrap(notOnMachine, cerrors.TranslationError, nodeAt, `node "backend" rendered name`),
			wantFacts: []string{
				"[" + string(cerrors.TranslationError), nodeAt.String(), `node "backend" rendered name`,
				notOnMachine.Error(),
			},
		},
		{
			name: "an envelope over a wrapping frame over a second envelope",
			finding: cerrors.Wrap(fmt.Errorf("asking the provider: %w", inner), cerrors.TranslationError, nodeAt,
				`node "backend" rendered name`),
			wantFacts: []string{
				"[" + string(cerrors.TranslationError), nodeAt.String(), `node "backend" rendered name`,
				"[" + string(cerrors.CatalogError), recipeAt.String(), "reading the provider's plugin",
				notOnMachine.Error(),
			},
		},
		{
			name:      "a finding that is not an envelope",
			finding:   errors.New(`Recipe "api-sourceonly:1.0.0": its provider does not support synthesized recipes`),
			wantFacts: []string{`Recipe "api-sourceonly:1.0.0": its provider does not support synthesized recipes`},
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			f := streamedFinding(c.finding)
			if strings.Join(f.facts, "\n") != strings.Join(c.wantFacts, "\n") {
				t.Fatalf("the reading returned facts %q; want %q", f.facts, c.wantFacts)
			}

			rendered := releaseFinding("", c.finding)
			if missing := cliMissingFacts(rendered, f); len(missing) > 0 {
				t.Fatalf("the detailed rendering lacks facts %q the finding carries:\n%s", missing, rendered)
			}

			for _, fact := range f.facts {
				dropped := strings.ReplaceAll(rendered, fact, "")
				found := false
				for _, m := range cliMissingFacts(dropped, f) {
					if m == fact {
						found = true
					}
				}
				if !found {
					t.Errorf("a rendering with %q removed was not reported as missing it:\n%s", fact, dropped)
				}
			}
		})
	}
}

// cliFailingAssertionOverlayComponent declares a Component whose build-time
// assertion never holds, so every instance of it fails the assertion phase. It is
// otherwise the shared catalog's own contract for this type, so the fault this
// fixture carries is the assertion and nothing else.
const cliFailingAssertionOverlayComponent = `apiVersion: aggrega.ai/v1beta1
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
//     published alongside it but do not describe it — and this verb prints each
//     diagnostic with every frame beneath its head precisely because those frames
//     carry the offending entity, the assertion, and the assertion's own
//     author-written message, so dropping the diagnostic drops all of it;
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

			// The fall-back is the run error rendered as every finding is rendered,
			// so the block is derived from the run error rather than transcribed.
			wantLine := releaseFinding("", vr.RunErr)

			// Occurrences the other streams already contribute, so the count below
			// measures the fall-back alone even where a stream happens to render to
			// the same text — which is what a single-violation run does.
			base := 0
			for _, ve := range tier4ResolveDiagnostics(vr) {
				if releaseFinding("", ve) == wantLine {
					base++
				}
			}
			if vr.Tier4 != nil && vr.Tier4.Analysis != nil {
				for _, ae := range vr.Tier4.Analysis.Errors {
					if releaseFinding("", ae.ToCompilerError()) == wantLine {
						base++
					}
				}
			}
			for _, f := range vr.ResolutionFindings {
				if releaseFinding("", f) == wantLine {
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

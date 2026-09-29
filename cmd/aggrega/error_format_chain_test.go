// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for nested-envelope frame rendering: every *CompilerError frame
// in a wrapped chain reaches the terminal — the outermost as the
// documented head shape, nested frames as indented "caused by:"
// continuations, the terminal sentinel's text preserved when it carries
// information the frames don't.

package main

import (
	"context"
	stderrors "errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// TestFormatCompilerError_NestedFrames_CatalogDuplicate reconstructs the
// exact wrap shape the catalog loader produces for a same-provider
// duplicate: a load-phase context envelope wrapping the
// ErrDuplicateRecipeIdentity frame whose SourceLocation.File is the
// offending recipe path. Every information-bearing piece must render:
// the load-phase head, the qualified identity message, the file path,
// and the sentinel's explanation.
func TestFormatCompilerError_NestedFrames_CatalogDuplicate(t *testing.T) {
	t.Parallel()
	inner := cerrors.Wrap(
		catalog.ErrDuplicateRecipeIdentity(),
		cerrors.CatalogError,
		cerrors.SourceLocation{File: "aggrega-catalog/recipe/terraform/mariadb-rds-copy/recipe.yaml"},
		`recipe identity "terraform/mariadb-rds:1.0.0" already declared`,
	)
	outer := cerrors.Wrap(inner, cerrors.CatalogError,
		cerrors.SourceLocation{File: "aggrega-catalog"}, "catalog load")

	got := formatCompilerError(outer)
	for _, want := range []string{
		"catalog load", // the head frame's phase context
		`recipe identity "terraform/mariadb-rds:1.0.0" already declared`,
		"aggrega-catalog/recipe/terraform/mariadb-rds-copy/recipe.yaml",
		"duplicate entity identity within a single catalog directory",
		"caused by:",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("nested-frame output missing %q\n--- got ---\n%s", want, got)
		}
	}
	// errors.Is stays intact: rendering is read-only on the chain.
	if !stderrors.Is(outer, catalog.ErrDuplicateRecipeIdentity()) {
		t.Error("errors.Is must keep traversing the chain")
	}
}

// TestFormatCompilerError_ThreeFrameChain pins that every envelope in a
// deeper chain renders, in outer-to-inner order, each as a caused-by
// continuation with its own category and source.
func TestFormatCompilerError_ThreeFrameChain(t *testing.T) {
	t.Parallel()
	innermost := cerrors.Wrap(nil, cerrors.DeveloperError,
		cerrors.SourceLocation{File: "aggregate.yaml", Line: 7},
		"socket selector does not match any component")
	middle := cerrors.Wrap(innermost, cerrors.CatalogError,
		cerrors.SourceLocation{File: "catalog/component/core/compute/server.yaml"},
		"resolving uses socket")
	head := cerrors.Wrap(middle, cerrors.OpsError,
		cerrors.SourceLocation{}, "release wiring")

	got := formatCompilerError(head)
	for _, want := range []string{
		"[OPS_ERROR]", "release wiring",
		"[CATALOG_ERROR]", "resolving uses socket",
		"[DEVELOPER_ERROR]", "aggregate.yaml:7",
		"socket selector does not match any component",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("three-frame output missing %q\n--- got ---\n%s", want, got)
		}
	}
	if i, j := strings.Index(got, "release wiring"), strings.Index(got, "resolving uses socket"); i > j {
		t.Errorf("frames out of order (outer must precede inner):\n%s", got)
	}
}

// TestFormatCompilerError_SentinelTail pins the novelty rule for the
// terminal non-envelope error: render when it adds information the
// frames don't carry; dedupe when its text is already shown.
func TestFormatCompilerError_SentinelTail(t *testing.T) {
	t.Parallel()
	novel := stderrors.New("the engine registry rejects unknown dispatch keys")
	withNovel := cerrors.Wrap(novel, cerrors.CatalogError,
		cerrors.SourceLocation{File: "recipe.yaml"}, "provider dispatch")
	got := formatCompilerError(withNovel)
	if !strings.Contains(got, "the engine registry rejects unknown dispatch keys") {
		t.Errorf("novel sentinel text must render:\n%s", got)
	}

	contained := stderrors.New("provider dispatch")
	withContained := cerrors.Wrap(contained, cerrors.CatalogError,
		cerrors.SourceLocation{File: "recipe.yaml"}, "provider dispatch failed for recipe")
	got = formatCompilerError(withContained)
	if strings.Count(got, "provider dispatch") != 1 {
		t.Errorf("contained sentinel text must dedupe:\n%s", got)
	}
}

// TestFormatCompilerError_DegenerateFrameDedupe pins the frame dedupe
// rule: a nested envelope adding nothing beyond its parent (same
// category, same source, message contained) renders once, not twice.
func TestFormatCompilerError_DegenerateFrameDedupe(t *testing.T) {
	t.Parallel()
	inner := cerrors.Wrap(nil, cerrors.CatalogError,
		cerrors.SourceLocation{File: "x.yaml"}, "schema invalid")
	outer := cerrors.Wrap(inner, cerrors.CatalogError,
		cerrors.SourceLocation{File: "x.yaml"}, "loading entity: schema invalid")

	got := formatCompilerError(outer)
	if strings.Count(got, "schema invalid") != 1 {
		t.Errorf("degenerate frame should dedupe:\n%s", got)
	}
	if strings.Contains(got, "caused by:") {
		t.Errorf("nothing novel to continue with:\n%s", got)
	}
}

// TestFormatCompilerError_JoinOfNestedChains pins that the multi-error
// walker and the frame walker compose: each joined chain renders its
// full frame stack, separated by the documented rule.
func TestFormatCompilerError_JoinOfNestedChains(t *testing.T) {
	t.Parallel()
	chainA := cerrors.Wrap(
		cerrors.Wrap(nil, cerrors.CatalogError, cerrors.SourceLocation{File: "a.yaml"}, "inner A"),
		cerrors.CatalogError, cerrors.SourceLocation{}, "outer A")
	chainB := cerrors.Wrap(
		cerrors.Wrap(nil, cerrors.DeveloperError, cerrors.SourceLocation{File: "b.yaml"}, "inner B"),
		cerrors.OpsError, cerrors.SourceLocation{}, "outer B")

	got := formatCompilerError(stderrors.Join(chainA, chainB))
	for _, want := range []string{"outer A", "inner A", "outer B", "inner B", "\n---\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("joined nested chains missing %q\n--- got ---\n%s", want, got)
		}
	}
}

// TestFormatCompilerError_MultiErrorLink_ResolutionShape mirrors the
// production shape exactly: the recipe-resolution envelope around the
// resolution-completeness verifier's errors.Join, whose sub-errors are
// fmt-wrapped sentinels. Every joined sub-error must render beneath the
// enclosing frame with a count header; errors.Is keeps traversing.
func TestFormatCompilerError_MultiErrorLink_ResolutionShape(t *testing.T) {
	t.Parallel()
	unmapped := fmt.Errorf(
		"%w: component %q (type %q); recipes implementing it: [cloudformation/mariadb-rds:1.0.0 terraform/mariadb-rds:1.0.0]; pin one in the Manifest",
		catalog.ErrNoRecipeResolved(), "db", "core/datastore/mariadb:1.0.0")
	noRecipe := fmt.Errorf(
		"%w: feature %q (type %q): no Manifest mapping and no feature recipe implements it",
		catalog.ErrNoRecipeResolved(), "metrics", "core/metrics:1.0.0")
	join := stderrors.Join(unmapped, noRecipe)
	head := cerrors.Wrap(join, cerrors.CatalogError,
		cerrors.SourceLocation{}, "recipe resolution")

	got := formatCompilerError(head)
	for _, want := range []string{
		"recipe resolution",
		"caused by (2):",
		"cloudformation/mariadb-rds:1.0.0 terraform/mariadb-rds:1.0.0",
		"pin one in the Manifest",
		"no Manifest mapping and no feature recipe implements it",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("recipe-resolution shape missing %q\n--- got ---\n%s", want, got)
		}
	}
	if !stderrors.Is(head, catalog.ErrNoRecipeResolved()) {
		t.Error("errors.Is must keep traversing through the multi-error link")
	}
}

// TestFormatCompilerError_DepthComposition pins that the Join walker and
// the frame walker compose at both levels: a top-level Join of [an
// envelope whose chain contains a Join, a plain envelope] renders every
// piece.
func TestFormatCompilerError_DepthComposition(t *testing.T) {
	t.Parallel()
	innerJoin := stderrors.Join(
		stderrors.New("sub one failed"),
		cerrors.Wrap(nil, cerrors.DeveloperError,
			cerrors.SourceLocation{File: "deep.yaml"}, "sub two failed"),
	)
	withNestedJoin := cerrors.Wrap(innerJoin, cerrors.CatalogError,
		cerrors.SourceLocation{}, "phase with forked failures")
	plain := cerrors.Wrap(nil, cerrors.OpsError,
		cerrors.SourceLocation{File: "release.yaml"}, "independent failure")

	got := formatCompilerError(stderrors.Join(withNestedJoin, plain))
	for _, want := range []string{
		"phase with forked failures",
		"caused by (2):",
		"sub one failed",
		"sub two failed",
		"deep.yaml",
		"independent failure",
		"\n---\n", // the top-level Join separator
	} {
		if !strings.Contains(got, want) {
			t.Errorf("depth composition missing %q\n--- got ---\n%s", want, got)
		}
	}
}

// TestFormatCompilerError_AssertionFailureReachesTerminal drives a real
// build whose Component carries a failing assertion and pins that what
// the formatter hands the terminal names the assertion and carries the
// fail-call message.
//
// The chain a failing assertion produces carries its identity on
// fmt-wrapped links rather than on envelope frames: the assertion name on
// one, the fail-call message on the next, and a bare sentinel at the end.
// A walk that rendered only the chain's terminal would print the
// sentinel's generic text and discard both, leaving the author no way to
// tell which assertion fired or why. The formatter's return value is what
// the command's error path writes to stderr, so this asserts over the
// bytes a user actually reads.
func TestFormatCompilerError_AssertionFailureReachesTerminal(t *testing.T) {
	root := t.TempDir()
	writeAssertionFailureFixture(t, root)

	releasePath := filepath.Join(root, "release.yaml")
	catalogDirs := []string{filepath.Join(root, "catalog")}
	traceEnabled := false

	ws := resolveTempWorkspace(t, tempWorkspaceOptions{})
	if err := pullRun(context.Background(), pullConfig{ReleasePath: releasePath, CatalogDirs: catalogDirs, ws: ws}); err != nil {
		t.Fatalf("prime lockfile via pull: %v", err)
	}
	err := dispatchBuild(context.Background(), ws, releasePath, catalogDirs, traceEnabled, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("build returned nil; expected an assertion failure")
	}

	got := formatCompilerError(err)
	t.Logf("formatter output for a failing assertion:\n%s", got)
	for _, want := range []string{
		"port-must-be-privileged",              // which assertion fired
		"PROBEMSG-port-8080-is-not-privileged", // the fail-call message
	} {
		if !strings.Contains(got, want) {
			t.Errorf("assertion diagnostic missing %q\n--- got ---\n%s", want, got)
		}
	}
	// The tail is flattened into the frame that carries it, so the sentinel's
	// own text appears once rather than on a line of its own.
	if n := strings.Count(got, "assertion failed"); n != 1 {
		t.Errorf("sentinel text should appear exactly once, got %d\n--- got ---\n%s", n, got)
	}
}

// writeAssertionFailureFixture writes a complete build workspace whose
// core/server Component carries an always-firing failing assertion, so a
// build over it halts before render with the assertion's chain intact.
func writeAssertionFailureFixture(t *testing.T, root string) {
	t.Helper()
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: assert-diag
  version: "1.0.0"
spec:
  components:
    only:
      type: core/server
      values:
        port: 8080
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: assert-diag-release
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        only:
          infra:
            replicas: 1
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: assert-diag-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        core/server: server-recipe:1.0.0
`)
	catalogRoot := filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(catalogRoot, "base", "components", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
  assertions:
    port-must-be-privileged: '{{ fail "PROBEMSG-port-8080-is-not-privileged" }}'
`)
	writeHelmRendererPlugin(t, filepath.Join(catalogRoot, "base", "providers"))
	writeFixture(t, filepath.Join(catalogRoot, "recipes", "helm", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server-recipe
  version: "1.0.0"
spec:
  capability: helm
  implements: core/server
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: {{ .infra.replicas | default 1 }}
    containers:
      server:
        image: test-server:latest
        ports:
          web: {{ .values.port }}
    services:
      svc:
        type: ClusterIP
        ports:
          web: {{ .values.port }}
`)
}

// TestFormatCompilerError_FmtLinkBetweenFrames pins that a fmt-wrapped
// link between two envelopes does not break the walk: both envelope
// frames render.
func TestFormatCompilerError_FmtLinkBetweenFrames(t *testing.T) {
	t.Parallel()
	inner := cerrors.Wrap(nil, cerrors.DeveloperError,
		cerrors.SourceLocation{File: "inner.yaml"}, "the actual failure")
	mid := fmt.Errorf("step context: %w", inner)
	head := cerrors.Wrap(mid, cerrors.CatalogError, cerrors.SourceLocation{}, "phase head")

	got := formatCompilerError(head)
	for _, want := range []string{"phase head", "the actual failure", "inner.yaml"} {
		if !strings.Contains(got, want) {
			t.Errorf("fmt-linked chain missing %q\n--- got ---\n%s", want, got)
		}
	}
}

// TestFormatCompilerError_ShortCode_HeadOnlyNotOnContinuation pins the one rule
// that decides where a condition's short code appears: the head of a diagnostic
// carries it, and no frame beneath a "caused by:" marker does.
//
// The assertion that earns its keep is the count. Asserting only that the head
// carries the code passes just as happily when every continuation carries it
// too, which is the shape that adds a line's worth of noise per frame and names
// nothing new — so the test counts the code across the whole rendering and
// requires exactly one. It would fail if the code leaked onto the continuation.
//
// The chain is the catalog loader's real duplicate shape: the condition sits at
// the bottom, two envelope frames above it, so this also pins that the head
// prints the code the CHAIN names rather than one the head frame holds itself.
func TestFormatCompilerError_ShortCode_HeadOnlyNotOnContinuation(t *testing.T) {
	t.Parallel()
	condition := catalog.ErrDuplicateRecipeIdentity()
	code, ok := cerrors.ShortCodeOf(condition)
	if !ok {
		t.Fatalf("condition %v reports no short code; nothing to pin", condition)
	}

	inner := cerrors.Wrap(condition, cerrors.CatalogError,
		cerrors.SourceLocation{File: "aggrega-catalog/recipe/helm/api-copy/recipe.yaml"},
		`recipe identity "helm/api:1.0.0" already declared`)
	head := cerrors.Wrap(inner, cerrors.CatalogError,
		cerrors.SourceLocation{File: "aggrega-catalog"}, "catalog load")

	got := formatCompilerError(head)
	t.Logf("head-and-continuation rendering:\n%s", got)

	if want := headTag(t, cerrors.CatalogError, condition); !strings.Contains(got, want) {
		t.Errorf("head does not name the condition: missing %q\n--- got ---\n%s", want, got)
	}
	if want := "caused by: [" + string(cerrors.CatalogError) + "]"; !strings.Contains(got, want) {
		t.Errorf("continuation should carry the category alone: missing %q\n--- got ---\n%s", want, got)
	}
	if n := strings.Count(got, code); n != 1 {
		t.Errorf("short code %s said %d times, want exactly 1 — a continuation is repeating it\n--- got ---\n%s",
			code, n, got)
	}
}

// TestFormatCompilerError_ShortCode_NotOnJoinedForkSubErrors covers the second
// way a code can leak into a continuation, and the one a straightforward
// implementation misses: the sub-errors beneath a "caused by (N):" header are
// rendered through the whole formatting recursion, so each of them is a head
// frame as far as that recursion can tell.
//
// They are not heads to a reader — they sit under a marker, indented, as the
// detail of the frame above — so none of them carries a code, and the count
// assertion is again what pins it.
func TestFormatCompilerError_ShortCode_NotOnJoinedForkSubErrors(t *testing.T) {
	t.Parallel()
	condition := catalog.ErrNoRecipeResolved()
	code, ok := cerrors.ShortCodeOf(condition)
	if !ok {
		t.Fatalf("condition %v reports no short code; nothing to pin", condition)
	}

	subA := cerrors.Wrap(condition, cerrors.CatalogError,
		cerrors.SourceLocation{File: "manifest.yaml"}, `component "db" has no mapping`)
	subB := cerrors.Wrap(condition, cerrors.CatalogError,
		cerrors.SourceLocation{File: "manifest.yaml"}, `feature "metrics" has no mapping`)
	head := cerrors.Wrap(stderrors.Join(subA, subB), cerrors.CatalogError,
		cerrors.SourceLocation{}, "recipe resolution")

	got := formatCompilerError(head)
	t.Logf("forked-continuation rendering:\n%s", got)

	if !strings.Contains(got, "caused by (2):") {
		t.Errorf("expected the two-way fork header\n--- got ---\n%s", got)
	}
	if want := headTag(t, cerrors.CatalogError, condition); !strings.Contains(got, want) {
		t.Errorf("head does not name the condition: missing %q\n--- got ---\n%s", want, got)
	}
	if n := strings.Count(got, code); n != 1 {
		t.Errorf("short code %s said %d times, want exactly 1 — a forked sub-error is repeating it\n--- got ---\n%s",
			code, n, got)
	}
	// Both sub-errors still name their category, and name it alone.
	if n := strings.Count(got, "["+string(cerrors.CatalogError)+"]"); n != 2 {
		t.Errorf("expected both forked sub-errors to carry the bare category tag, found %d\n--- got ---\n%s", n, got)
	}
}

// TestFormatCompilerError_ShortCode_EachTopLevelJoinElementCarriesItsOwn is the
// converse of the two above, and it is why the rule is stated in terms of
// position rather than of nesting depth.
//
// A top-level errors.Join is a list of independent failures — separated by the
// "---" rule, each about a different thing — so every element leads a
// diagnostic of its own and names its own condition. Suppressing the code on
// everything but the first element would leave a reader unable to name any
// failure but one.
func TestFormatCompilerError_ShortCode_EachTopLevelJoinElementCarriesItsOwn(t *testing.T) {
	t.Parallel()
	first := catalog.ErrNoRecipeResolved()
	second := catalog.ErrDuplicateRecipeIdentity()

	got := formatCompilerError(stderrors.Join(
		cerrors.Wrap(first, cerrors.CatalogError,
			cerrors.SourceLocation{File: "manifest.yaml"}, "recipe resolution"),
		cerrors.Wrap(second, cerrors.CatalogError,
			cerrors.SourceLocation{File: "recipe.yaml"}, "catalog load"),
	))
	t.Logf("two independent failures:\n%s", got)

	blocks := strings.Split(got, "\n---\n")
	if len(blocks) != 2 {
		t.Fatalf("want two diagnostics separated by the join rule; got %d\n--- got ---\n%s", len(blocks), got)
	}
	for i, condition := range []error{first, second} {
		want := headTag(t, cerrors.CatalogError, condition)
		if !strings.HasPrefix(blocks[i], want) {
			t.Errorf("diagnostic %d does not open with %q\n--- got ---\n%s", i, want, blocks[i])
		}
	}
}

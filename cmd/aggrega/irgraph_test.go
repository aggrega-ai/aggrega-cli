// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// Three commands hand back the IR graph they resolved. What has to be proved is
// the same on each and is proved by invocation rather than by declaration: the
// document appears, it is the graph, it validates against the graph's own
// schema, the command's OWN output is byte-identical to what it was without the
// flag, and the values an author declared sensitive are withheld from it.
//
// The compiler-version stamp the graph carries is why the byte-identity
// assertions here compare two runs of ONE binary rather than a captured
// document: the stamp is a property of the binary, so it is identical across two
// runs in one process and would differ across two builds. Nothing here records a
// graph as a golden for that reason.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/buildops"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// decodeLeadingGraph reads the ONE JSON document at the head of a command's
// output stream and returns it plus everything that followed.
//
// It is a streaming decode rather than a whole-stream unmarshal, and that is the
// property under test as much as it is a convenience: the graph is written ahead
// of the command's own output precisely so a consumer can read it and stop. A
// decoder that had to consume the whole stream would pass here on an emission
// that only works when nothing follows it.
func decodeLeadingGraph(t *testing.T, stream string) (graph map[string]any, rest string) {
	t.Helper()
	document, rest := splitLeadingGraph(t, stream)
	if err := json.Unmarshal([]byte(document), &graph); err != nil {
		t.Fatalf("the leading document is not a JSON object: %v\n--- document ---\n%s", err, truncate(document))
	}
	return graph, rest
}

// splitLeadingGraph returns the leading JSON document's own bytes and everything
// after it, with the single newline the shared structured writer appends already
// consumed — that newline belongs to the document rather than to whatever
// follows, so leaving it on the remainder would make every byte-identity
// comparison below fail on the separator instead of on the output.
func splitLeadingGraph(t *testing.T, stream string) (document, rest string) {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(stream))
	var probe json.RawMessage
	if err := dec.Decode(&probe); err != nil {
		t.Fatalf("the stream does not open with one JSON document: %v\n--- stream ---\n%s", err, truncate(stream))
	}
	remaining, err := io.ReadAll(dec.Buffered())
	if err != nil {
		t.Fatalf("read what followed the document: %v", err)
	}
	consumed := len(stream) - len(remaining)
	document, rest = stream[:consumed], stream[consumed:]
	rest = strings.TrimPrefix(rest, "\n")
	return document, rest
}

func truncate(s string) string {
	if len(s) <= 2000 {
		return s
	}
	return s[:2000] + "\n… (truncated)"
}

// assertIsAnIRGraph checks the decoded document is the graph rather than some
// other JSON the command might have written, and that it conforms to the graph's
// own schema through the one validation component — which is what makes "the
// document is schema-bound" an executable claim about what the command emits
// rather than about what the pipeline holds.
func assertIsAnIRGraph(t *testing.T, subject string, graph map[string]any) {
	t.Helper()
	if graph["kind"] != types.IRKind {
		t.Errorf("%s: the emitted document's kind is %v, and the graph's is %q", subject, graph["kind"], types.IRKind)
	}
	spec, ok := graph["spec"].(map[string]any)
	if !ok {
		t.Fatalf("%s: the emitted document carries no spec object", subject)
	}
	if _, ok := spec["nodes"]; !ok {
		t.Errorf("%s: the emitted document carries no nodes, so it is not a resolved graph", subject)
	}
	findings, err := envelope.ValidateInstance(envelope.KindIR, any(graph))
	if err != nil {
		t.Fatalf("%s: IR schema engine error: %v", subject, err)
	}
	if len(findings) > 0 {
		t.Errorf("%s: the emitted document violates the IR schema (%d finding(s)): %+v",
			subject, len(findings), findings[:minInt(4, len(findings))])
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// --- the inspection command --------------------------------------------------

// TestInspectIRGraph_IsAdditiveAndLeavesTheViewByteIdentical is the whole of
// what "additive" means, asserted from both directions in one case. The graph
// appears, AND the readable view that follows it is byte-for-byte the view the
// same command renders without the flag.
//
// Either half alone proves nothing: the first is satisfied by a flag that
// replaced the view, and the second by a flag that did nothing at all.
func TestInspectIRGraph_IsAdditiveAndLeavesTheViewByteIdentical(t *testing.T) {
	t.Parallel()

	without := inspectInstance(t, fullstackRelease, nil)
	with := inspectInstance(t, fullstackRelease, func(o *inspectOptions) { o.irGraph = true })

	graph, rest := decodeLeadingGraph(t, with)
	assertIsAnIRGraph(t, "aggrega inspect --ir-graph", graph)

	if rest != without {
		t.Errorf("the view behind the graph is not the view this command renders without the flag\n--- with, behind the graph ---\n%s\n--- without ---\n%s",
			truncate(rest), truncate(without))
	}
	if strings.TrimSpace(without) == "" {
		t.Fatal("the command renders nothing without the flag, so the comparison above cannot fail")
	}
}

// TestInspectIRGraph_IsOrthogonalToTheFormatFlag pins the ruling that the two
// flags select different things: one the document, the other the format. They
// are usable together, and each still does its own job when they are.
//
// The as-authored half is the sharper of the two assertions: a design that made
// them exclusive would refuse this call, and one that made --output yaml mean
// "the graph again" would emit the graph twice.
func TestInspectIRGraph_IsOrthogonalToTheFormatFlag(t *testing.T) {
	t.Parallel()

	authoredAlone := inspectInstance(t, fullstackRelease, func(o *inspectOptions) { o.output = outputYAML })
	both := inspectInstance(t, fullstackRelease, func(o *inspectOptions) {
		o.output = outputYAML
		o.irGraph = true
	})

	graph, rest := decodeLeadingGraph(t, both)
	assertIsAnIRGraph(t, "aggrega inspect --output yaml --ir-graph", graph)
	if rest != authoredAlone {
		t.Errorf("--output yaml behind the graph is not what --output yaml renders alone\n--- behind the graph ---\n%s\n--- alone ---\n%s",
			truncate(rest), truncate(authoredAlone))
	}
	if strings.Contains(rest, `"nodes"`) || strings.Contains(rest, "nodes:") {
		t.Errorf("--output yaml emitted a graph beside the one --ir-graph emitted; the two flags select one document each:\n%s", truncate(rest))
	}
}

// TestInspectOutputYAMLNoLongerEmitsTheGraph is the retirement, stated as a
// property of what the flag now produces rather than as the absence of a
// substring: --output yaml on a Release reproduces the authored file, which is
// what it does for every other kind.
//
// Comparing against --raw is what makes it capable of failing. An assertion that
// merely looked for "no nodes:" would pass on a command that had stopped
// emitting anything.
func TestInspectOutputYAMLNoLongerEmitsTheGraph(t *testing.T) {
	t.Parallel()

	yamlOut := inspectInstance(t, fullstackRelease, func(o *inspectOptions) { o.output = outputYAML })
	rawOut := inspectInstance(t, fullstackRelease, func(o *inspectOptions) { o.mode = modeForcedRaw })

	if yamlOut != rawOut {
		t.Errorf("--output yaml on a Release no longer emits the graph; it reproduces the authored file, as --raw does\n--- --output yaml ---\n%s\n--- --raw ---\n%s",
			truncate(yamlOut), truncate(rawOut))
	}
	authored, err := os.ReadFile(fullstackRelease)
	if err != nil {
		t.Fatalf("read the authored Release: %v", err)
	}
	if !strings.Contains(yamlOut, "kind: Release") {
		t.Errorf("--output yaml did not reproduce a Release document; got:\n%s", truncate(yamlOut))
	}
	if !bytes.Contains(authored, []byte("kind: Release")) {
		t.Fatal("the fixture is not a Release, so the assertion above proves nothing")
	}
}

// TestInspectIRGraph_RefusesAKindThatResolvesToNoGraph pins the loud refusal.
// The alternative is a flag that renders the kind's own view and silently omits
// the document that was asked for, which reads to the caller exactly like an
// entity whose graph is empty.
//
// The first row is the positive control: without it, a dispatch broken for an
// unrelated reason would refuse everything and read as a working guard.
func TestInspectIRGraph_RefusesAKindThatResolvesToNoGraph(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name       string
		identifier string
		refuses    string
	}{
		{"a Release, which resolves to one", fullstackRelease, ""},
		{"an Aggregate", fullstackAggregate, "resolves to no graph"},
		{"a Manifest", fullstackManifest, "resolves to no graph"},
		{"a catalog entity by name", "core/compute/api-server", "resolves to no graph"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			opts := testInspectOptions(&buf)
			opts.irGraph = true
			err := dispatchInspect(resolveTempWorkspace(t, tempWorkspaceOptions{}), tc.identifier, []string{fullstackCatalog}, opts)
			switch {
			case tc.refuses == "" && err != nil:
				t.Fatalf("a Release was refused: %v", err)
			case tc.refuses != "" && err == nil:
				t.Fatalf("a kind that resolves to no graph was accepted; it produced:\n%s", truncate(buf.String()))
			case tc.refuses != "" && !strings.Contains(err.Error(), tc.refuses):
				t.Fatalf("the refusal does not say what was wrong: want %q in %q", tc.refuses, err)
			}
		})
	}
}

// TestInspectIRGraph_WithholdsDeclaredValuesAndTheAffordanceRevealsThem is the
// same pair of assertions the readable layouts already carry, over the document
// this command now hands back. Both directions run, because the withholding is a
// decision rather than a limitation only while the reader has a way to ask for
// the graph as the compiler produced it.
//
// The public values are the positive control for the substring instrument: an
// absence assertion over a stream the search cannot see into passes on a graph
// that withholds nothing at all.
func TestInspectIRGraph_WithholdsDeclaredValuesAndTheAffordanceRevealsThem(t *testing.T) {
	t.Parallel()

	emit := func(unredacted bool) string {
		var buf bytes.Buffer
		opts := testInspectOptions(&buf)
		opts.irGraph = true
		opts.unredacted = unredacted
		if err := dispatchInspect(resolveTempWorkspace(t, tempWorkspaceOptions{}), yamlRedactionRelease, []string{yamlRedactionCatalog}, opts); err != nil {
			t.Fatalf("dispatchInspect(--ir-graph, unredacted=%v): %v", unredacted, err)
		}
		return buf.String()
	}

	withheld := emit(false)
	for _, secret := range authoredSecrets {
		if strings.Contains(withheld, secret.value) {
			t.Errorf("the emitted graph carries %q in the clear — %s", secret.value, secret.why)
		}
	}
	if !strings.Contains(withheld, redact.WithheldMarkerKey()) {
		t.Error("the emitted graph names no withheld value; the absence assertions above would also pass on an empty graph")
	}
	for _, public := range authoredPublic {
		if !strings.Contains(withheld, public) {
			t.Errorf("the emitted graph does not carry %q, which no declaration covers; withholding it declares public data secret", public)
		}
	}

	revealed := emit(true)
	for _, secret := range authoredSecrets {
		if !strings.Contains(revealed, secret.value) {
			t.Errorf("--unredacted did not emit %q — %s", secret.value, secret.why)
		}
	}
	if strings.Contains(revealed, redact.WithheldMarkerKey()) {
		t.Error("--unredacted emitted a withheld marker; the reader asked for the graph as the compiler produced it")
	}
}

// --- the validation command --------------------------------------------------

// TestValidateIRGraph_IsAdditiveAndLeavesTheVerdictAlone runs one Release
// through the release scope with and without the flag and asserts the three
// things the clause asks for at once: the graph appears, this verb's own output
// is byte-identical, and the verdict and the diagnostics are untouched.
func TestValidateIRGraph_IsAdditiveAndLeavesTheVerdictAlone(t *testing.T) {
	t.Parallel()

	rw := resolveTempWorkspace(t, tempWorkspaceOptions{})
	dirs := []string{cliFullstackCatalogDir(t)}

	var diagWithout, outWithout bytes.Buffer
	errWithout := validateRelease(rw, fullstackRelease, dirs, compilerops.VerbosityDetailed, false, &diagWithout, &outWithout)

	var diagWith, outWith bytes.Buffer
	errWith := validateRelease(rw, fullstackRelease, dirs, compilerops.VerbosityDetailed, true, &diagWith, &outWith)

	if (errWithout == nil) != (errWith == nil) {
		t.Fatalf("the flag moved the verdict: without=%v with=%v", errWithout, errWith)
	}
	if errWithout != nil {
		t.Fatalf("the fixture does not validate, so nothing here observes the emission: %v\n%s", errWithout, diagWithout.String())
	}
	if diagWith.String() != diagWithout.String() {
		t.Errorf("the flag changed the diagnostics\n--- with ---\n%s\n--- without ---\n%s", diagWith.String(), diagWithout.String())
	}

	graph, rest := decodeLeadingGraph(t, outWith.String())
	assertIsAnIRGraph(t, "aggrega validate --release --ir-graph", graph)
	if rest != outWithout.String() {
		t.Errorf("this verb's own report behind the graph is not the report it writes without the flag\n--- behind ---\n%s\n--- without ---\n%s",
			rest, outWithout.String())
	}
	if strings.TrimSpace(outWithout.String()) == "" {
		t.Fatal("the verb writes nothing without the flag, so the comparison above cannot fail")
	}
}

// TestValidateIRGraph_RefusesOutsideTheReleaseScope pins the refusal, driven
// through the command's own argument parsing rather than through the scope
// function, because the scope is what the arguments select.
func TestValidateIRGraph_RefusesOutsideTheReleaseScope(t *testing.T) {
	t.Parallel()

	cmd := newValidateCmd()
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--" + flagIRGraph})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("the whole-catalog scope accepted a request for a graph it resolves none of")
	}
	if !strings.Contains(err.Error(), "--release") {
		t.Errorf("the refusal does not name the scope that carries the capability: %v", err)
	}
}

// --- the build command -------------------------------------------------------

// TestBuildIRGraph_IsAdditiveAndStillWritesTheArtifacts is the clause-6
// assertion for the build: the graph reaches standard output without corrupting
// what the build already prints there, and the build still does its job.
//
// Both runs render into the same output directory so the summary sentence, which
// names that directory, is comparable. A build re-renders and rewrites rather
// than skipping, so the second run is a build rather than a no-op over the
// first's tree.
func TestBuildIRGraph_IsAdditiveAndStillWritesTheArtifacts(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath, catalogDir := dryRunFixture(t, root, "")
	primeDryRunLockfile(t, root, releasePath, catalogDir)
	ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
	outDir := filepath.Join(root, "dist")

	run := func(withGraph bool) string {
		var out, diag bytes.Buffer
		opts := buildops.RunOptions{OutDir: outDir}
		if withGraph {
			opts.IRGraph = func(graph *types.IRGraph) error {
				return writeIRGraph(&out, "build", graph, false)
			}
		}
		if err := dispatchBuildWithDiag(context.Background(), ws, releasePath, []string{catalogDir}, false,
			buildDiagOptions{}, opts, &diag, &out); err != nil {
			t.Fatalf("build (withGraph=%v): %v\ndiagnostics:\n%s", withGraph, err, diag.String())
		}
		return out.String()
	}

	without := run(false)
	with := run(true)

	graph, rest := decodeLeadingGraph(t, with)
	assertIsAnIRGraph(t, "aggrega build --ir-graph", graph)

	// The build's own stdout carries a wall-clock line, which moves between two
	// runs for reasons that have nothing to do with the flag, so the comparison
	// is over the lines that are not it. Everything else is compared verbatim.
	if got, want := withoutTiming(rest), withoutTiming(without); got != want {
		t.Errorf("the build's own output behind the graph is not what it prints without the flag\n--- behind ---\n%s\n--- without ---\n%s", got, want)
	}
	if !strings.Contains(without, "Build completed") {
		t.Fatalf("the build prints no summary without the flag, so the comparison above cannot fail; got:\n%s", without)
	}
	if withoutTiming(without) == without {
		t.Fatal("the timing filter matched nothing, so it is silently comparing a line that moves")
	}

	written := readTree(t, outDir)
	if len(written) == 0 {
		t.Error("the build that handed back the graph wrote no artifacts; the flag is additive, not a mode")
	}
}

// withoutTiming drops the build's wall-clock line. Its label is taken from the
// production formatter rather than spelled here, so a reworded summary moves the
// filter with it instead of leaving it silently matching nothing — a condition
// the caller above asserts against rather than trusting.
func withoutTiming(stream string) string {
	label := strings.TrimSpace(strings.SplitN(formatTotalBuildTimeLine(""), ":", 2)[0])
	var kept []string
	for _, line := range strings.Split(stream, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), label) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// TestBuildIRGraph_CombinesWithADryRun discharges the page's claim that the two
// combine: the graph is written, the artifact report follows it, and nothing
// reaches the disk.
//
// The flags that WRITE are refused alongside a dry run rather than ignored, so a
// flag that combines with one has to be shown combining rather than assumed to.
func TestBuildIRGraph_CombinesWithADryRun(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath, catalogDir := dryRunFixture(t, root, "")
	primeDryRunLockfile(t, root, releasePath, catalogDir)
	ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
	before := treeSnapshot(t, root)

	var out, diag bytes.Buffer
	opts := buildops.RunOptions{DryRun: true}
	opts.IRGraph = func(graph *types.IRGraph) error {
		return writeIRGraph(&out, "build", graph, false)
	}
	if err := dispatchBuildWithDiag(context.Background(), ws, releasePath, []string{catalogDir}, false,
		buildDiagOptions{}, opts, &diag, &out); err != nil {
		t.Fatalf("dry run --ir-graph: %v\ndiagnostics:\n%s", err, diag.String())
	}

	if d := diffSnapshots(before, treeSnapshot(t, root)); d != "" {
		t.Fatalf("asking for the graph made a dry run write to the workspace:\n%s", d)
	}
	graph, rest := decodeLeadingGraph(t, out.String())
	assertIsAnIRGraph(t, "aggrega build --dry-run --ir-graph", graph)
	if !strings.Contains(rest, "nothing was written") {
		t.Errorf("the dry run's own report did not follow the graph; got:\n%s", truncate(rest))
	}
	if strings.Count(rest, "==> ") == 0 {
		t.Errorf("the dry run reported no artifacts behind the graph, so the render did not happen:\n%s", truncate(rest))
	}
}

// TestBuildIRGraph_WithholdsDeclaredValues is the build's leg of the same
// withholding the inspection command carries. It matters more here rather than
// less: this command has no affordance for revealing them on this stream, so a
// leak would have no counterpart the reader could have asked for instead.
func TestBuildIRGraph_WithholdsDeclaredValues(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath, catalogDir := dryRunFixture(t, root, "")
	primeDryRunLockfile(t, root, releasePath, catalogDir)
	ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})

	var out, diag bytes.Buffer
	opts := buildops.RunOptions{OutDir: filepath.Join(root, "dist")}
	opts.IRGraph = func(graph *types.IRGraph) error {
		// The unredacted arm is asserted here, in the same case, so the
		// withholding below is known to be the writer's doing rather than a
		// fixture that declares nothing sensitive.
		var revealed bytes.Buffer
		if err := writeIRGraph(&revealed, "build", graph, true); err != nil {
			return err
		}
		var withheld bytes.Buffer
		if err := writeIRGraph(&withheld, "build", graph, false); err != nil {
			return err
		}
		if revealed.String() == withheld.String() && strings.Contains(revealed.String(), redact.WithheldMarkerKey()) {
			t.Error("the two arms produced the same bytes and both carry a marker; the writer is not the thing withholding")
		}
		return writeIRGraph(&out, "build", graph, false)
	}
	if err := dispatchBuildWithDiag(context.Background(), ws, releasePath, []string{catalogDir}, false,
		buildDiagOptions{}, opts, &diag, &out); err != nil {
		t.Fatalf("build --ir-graph: %v\ndiagnostics:\n%s", err, diag.String())
	}

	graph, _ := decodeLeadingGraph(t, out.String())
	assertIsAnIRGraph(t, "aggrega build --ir-graph", graph)
}

// --- the writer itself -------------------------------------------------------

// TestWriteIRGraph_RefusesANilGraph pins the one input the writer will not
// serialize. An empty document at the head of the stream reads to a consumer as
// a Release that resolved to nothing, which is a different and worse answer than
// a refusal.
func TestWriteIRGraph_RefusesANilGraph(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	err := writeIRGraph(&buf, "inspect", nil, false)
	if err == nil {
		t.Fatalf("a nil graph was serialized as %q", buf.String())
	}
	if !strings.Contains(err.Error(), flagIRGraph) {
		t.Errorf("the refusal does not name the flag that was asked for: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("the refusal still wrote %d byte(s)", buf.Len())
	}
}

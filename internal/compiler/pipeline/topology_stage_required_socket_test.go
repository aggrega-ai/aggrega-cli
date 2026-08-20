// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The proofs in this file cover the required uses-socket rule as the build
// actually asks it: over the composed application, from the pass that holds that
// composition, with a real catalog and a real deployment document.
//
// The rule's own reading — which surfaces populate a socket, which do not, and
// where the finding is positioned — is proved beside its declaration. What can
// only be proved here is that the build invokes it at all, that the facts it
// reads carry the composition's real values rather than zero ones, and that a
// document offending against it stops.
//
// The last of those is why the fixtures below are compiled through the whole
// pre-render pipeline rather than through a helper that assumes a compilation
// reached its outputs: the halt is the thing under proof.

// catalogSplice is one edit to one catalog document, spelled as the text it
// replaces and the text that replaces it, so a reader sees the YAML being
// written.
type catalogSplice struct {
	file        string
	anchor      string
	replacement string
}

// The splices these proofs need. The fixture catalog declares a dependency
// socket on each application component type and no requirement anywhere, so a
// requirement has to be written in for the rule to have anything to say.
var (
	// webUIRequiresItsSocket marks the socket the application's own document
	// writes a connection on, so removing that connection is the whole edit a
	// negative fixture needs.
	webUIRequiresItsSocket = catalogSplice{
		file:        "base/components/web-ui.yaml",
		anchor:      "  usesSchema:\n    apis:\n",
		replacement: "  usesSchema:\n    apis:\n      required: true\n",
	}
	// webUIRequiresASecondSocket adds a second requirement the application
	// cannot have satisfied, so one component offends twice.
	webUIRequiresASecondSocket = catalogSplice{
		file:   "base/components/web-ui.yaml",
		anchor: "  usesSchema:\n    apis:\n",
		replacement: "  usesSchema:\n    extras:\n      required: true\n" +
			"      selector: \"component.aggrega.ai/type=server\"\n    apis:\n      required: true\n",
	}
	// postgresRequiresASocket puts a requirement on the type the module's own
	// component names, which is the case no reading of the two authoring
	// documents can answer: that component is declared in the module's
	// document.
	postgresRequiresASocket = catalogSplice{
		file:   "base/components/postgres.yaml",
		anchor: "spec:\n  schema:\n",
		replacement: "spec:\n  usesSchema:\n    peers:\n      required: true\n" +
			"      selector: \"component.aggrega.ai/type=stateful-server\"\n  schema:\n",
	}
)

// The application's whole dependency block on the component whose type the
// splices above put a requirement on. It is quoted to the end of the document
// because removing a prefix of it would orphan the keys nested under it.
const frontendDependencyBlock = "      uses:\n        apis:\n          my-api:\n            target: backend\n" +
	"            inject:\n              env:\n                - in: API_HOST\n                  from: host\n" +
	"                - in: API_PORT\n                  from: service_port\n"

// splicedCatalog copies the fixture catalog into a temp directory and applies
// each edit, refusing one that changed nothing.
//
// The copy is what makes these fixtures real: the compilation loads a catalog
// off disk, resolves its inheritance and reads the requirement through the same
// path a user's catalog goes through. A hand-built lookup would answer for the
// types the fixture names and prove nothing about the loader.
func splicedCatalog(t *testing.T, splices ...catalogSplice) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "catalog")
	if err := os.CopyFS(root, os.DirFS(invocationCatalogPath)); err != nil {
		t.Fatalf("copy the fixture catalog: %v", err)
	}
	for _, sp := range splices {
		path := filepath.Join(root, sp.file)
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", sp.file, err)
		}
		if !bytes.Contains(source, []byte(sp.anchor)) {
			t.Fatalf("the catalog entity %s no longer carries %q; the edit would be silently inert", sp.file, sp.anchor)
		}
		out := bytes.Replace(source, []byte(sp.anchor), []byte(sp.replacement), 1)
		if bytes.Equal(out, source) {
			t.Fatalf("the edit to %s changed nothing", sp.file)
		}
		if err := os.WriteFile(path, out, 0o600); err != nil {
			t.Fatalf("write %s: %v", sp.file, err)
		}
	}
	return root
}

// compileAgainst drives the whole pre-render pipeline over the fixture
// application, optionally with its dependency block removed, and hands back the
// error instead of failing on it.
func compileAgainst(t *testing.T, catalogPath string, dropTheDependencyBlock bool) error {
	t.Helper()
	rel, err := resolver.LoadRelease(invocationReleasePath)
	if err != nil {
		t.Fatalf("load the deployment document: %v", err)
	}
	manifest, err := resolver.LoadManifest(invocationManifestPath)
	if err != nil {
		t.Fatalf("load the manifest: %v", err)
	}
	cat, err := catalog.LoadAll("", catalogPath)
	if err != nil {
		t.Fatalf("load the catalog: %v", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		t.Fatalf("resolve catalog inheritance: %d error(s); first: %v", len(extErrs), extErrs[0])
	}
	aggregate, err := os.ReadFile(invocationAggregatePath)
	if err != nil {
		t.Fatalf("read the application document: %v", err)
	}
	if dropTheDependencyBlock {
		if !bytes.Contains(aggregate, []byte(frontendDependencyBlock)) {
			t.Fatal("the application fixture no longer carries the dependency block these proofs remove; the edit would be silently inert")
		}
		aggregate = bytes.Replace(aggregate, []byte(frontendDependencyBlock), nil, 1)
	}
	return runStepsOneToFour(
		context.Background(),
		Inputs{
			Release:           rel,
			Manifest:          manifest,
			AggregateRawBytes: aggregate,
			AggregatePath:     invocationAggregatePath,
			ReleasePath:       invocationReleasePath,
			WorkspaceConfig:   workspaceconfig.Default(),
			CompilerVersion:   invocationVersion,
		},
		cat.CatalogLookup(),
		cat.RecipeLookupWithManifest(manifest).Recipe,
		cat.SubAggregateLoader(),
		func(*stepsOneToFourOutput) error { return nil },
	)
}

// requiredSocketViolations pulls the per-violation slice out of the halt,
// failing when the halt is not this rule's.
//
// It reads the structured slice rather than the summary text, because the field
// a front end positions on is carried per violation and the summary only counts
// them.
func requiredSocketViolations(t *testing.T, err error) []*resolver.ResolverError {
	t.Helper()
	if err == nil {
		t.Fatal("a required dependency socket left unpopulated did not halt the build")
	}
	if !errors.Is(err, resolver.ErrMissingRequiredSocket()) {
		t.Fatalf("the halt does not carry the required-socket rule's own identity: %v", err)
	}
	var multi *resolver.MultiResolverError
	if !errors.As(err, &multi) {
		t.Fatalf("the halt must carry its violations; got %T: %v", err, err)
	}
	return multi.Violations
}

// TestTheRequiredSocketRuleIsInvokedByTheBuildAndHalts is the invocation proof:
// the build asks the rule, and a document leaving a required socket unpopulated
// stops.
//
// The message assertions are the part that cannot be replaced by a shorter
// check. A rule reading a fact that answered with a zero value would return
// nothing and the build would pass, so a halt already proves the facts are live
// — but a halt alone would not distinguish live facts from a rule that raised
// unconditionally. Requiring the emitted text to carry the component type the
// catalog declared and the socket that type named pins the VALUES the rule read,
// against a real compilation of a real catalog.
func TestTheRequiredSocketRuleIsInvokedByTheBuildAndHalts(t *testing.T) {
	t.Parallel()

	violations := requiredSocketViolations(t,
		compileAgainst(t, splicedCatalog(t, webUIRequiresItsSocket), true))

	if len(violations) != 1 {
		t.Fatalf("one unpopulated required socket must raise exactly one violation; got %d: %+v", len(violations), violations)
	}
	const wantField = ".spec.components.frontend.uses.apis"
	if got := violations[0].Field; got != wantField {
		t.Errorf("the finding must position at the socket the author has to write, %q; got %q", wantField, got)
	}
	for _, want := range []string{"apis", "core/compute/web-ui"} {
		if !bytes.Contains([]byte(violations[0].Message), []byte(want)) {
			t.Errorf("the finding must name %q, which it can only have read from the composition; got %q",
				want, violations[0].Message)
		}
	}
}

// TestTheRequiredSocketRuleAcceptsThePopulatedApplication is the control the
// proof above is read against.
//
// Without it, a rule that refused every document would look like a success: the
// only difference between the two is the one connection the application writes,
// and the requirement is declared identically in both.
func TestTheRequiredSocketRuleAcceptsThePopulatedApplication(t *testing.T) {
	t.Parallel()
	if err := compileAgainst(t, splicedCatalog(t, webUIRequiresItsSocket), false); err != nil {
		t.Fatalf("an application that populates the required socket must build; got %v", err)
	}
}

// TestTheRequiredSocketRuleReachesAComponentAModuleContributed is the coverage
// proof for the half of the composition no reading of the two authoring
// documents can see.
//
// The offending component is declared inside the module the application imports,
// so its dependency block is not in either document the resolve holds. The
// connection set the composition produced is what answers for it, and that set
// exists only once the composition is flattened — which is why the rule sits
// where it does.
func TestTheRequiredSocketRuleReachesAComponentAModuleContributed(t *testing.T) {
	t.Parallel()

	violations := requiredSocketViolations(t,
		compileAgainst(t, splicedCatalog(t, postgresRequiresASocket), false))

	if len(violations) != 1 {
		t.Fatalf("the module's unpopulated socket must raise exactly one violation; got %d: %+v", len(violations), violations)
	}
	// The locator names the module's own document, which is where the author
	// edits: the component is spelled with the one name it has inside that
	// document rather than with the prefix the composition gave it.
	const wantField = ".spec.components.main-db.uses.peers"
	if got := violations[0].Field; got != wantField {
		t.Errorf("the finding must position in the document that declares the component, %q; got %q", wantField, got)
	}
}

// TestTheRequiredSocketRuleReportsAcrossTheWholeComposition proves the rule
// reports every offender in one pass, whichever document declared the component.
//
// It is the case a rule asked in two places over two halves of the composition
// structurally cannot produce: one half would report, the build would stop, and
// the other half's offender would be reached on a later run only after the first
// was fixed. Asking one rule once over every node is what makes both faults
// reachable together.
func TestTheRequiredSocketRuleReportsAcrossTheWholeComposition(t *testing.T) {
	t.Parallel()

	violations := requiredSocketViolations(t, compileAgainst(t,
		splicedCatalog(t, webUIRequiresItsSocket, postgresRequiresASocket), true))

	want := []string{
		".spec.components.frontend.uses.apis",
		".spec.components.main-db.uses.peers",
	}
	got := make([]string, 0, len(violations))
	for _, v := range violations {
		got = append(got, v.Field)
	}
	if len(got) != len(want) {
		t.Fatalf("both offenders must be reported in one halt; got %d: %v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("the findings must come back in the order the composition produced its nodes; got %v, want %v", got, want)
		}
	}
}

// TestTheRequiredSocketRuleReportsEverySocketOnOneComponent proves the reading
// is per socket rather than per component: a component leaving two requirements
// unpopulated is told about both.
//
// The two are declared in an order a sort would reverse, so the assertion also
// pins that the sequence is the catalog entity's own author order.
func TestTheRequiredSocketRuleReportsEverySocketOnOneComponent(t *testing.T) {
	t.Parallel()

	violations := requiredSocketViolations(t,
		compileAgainst(t, splicedCatalog(t, webUIRequiresASecondSocket), true))

	want := []string{
		".spec.components.frontend.uses.extras",
		".spec.components.frontend.uses.apis",
	}
	got := make([]string, 0, len(violations))
	for _, v := range violations {
		got = append(got, v.Field)
	}
	if len(got) != len(want) {
		t.Fatalf("both unpopulated sockets on one component must be reported; got %d: %v", len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("the findings must follow the order the component type declares its sockets; got %v, want %v", got, want)
		}
	}
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/usesedge"
)

// The proofs in this file cover the rule about who owns a connection's target,
// evaluated over the finalised graph.
//
// They sit in this package rather than beside the declaration because the
// presentation and the enumeration the rule needs are both carried on an
// unexported output, and reaching them is the point: a proof over an enumeration
// a test assembles proves the test's account of the graph rather than the
// compiler's. They read the violations the pre-render pipeline accumulated,
// which is the slice a build gates on — so what they assert is that the
// production path invokes the rule, not merely that the rule would object if
// something asked it.
//
// The Release document each proof needs is written into a temporary directory
// rather than committed, because every one of them wants a different edge
// surface over the same Aggregate and the fixture on disk is shared with other
// proofs.

// retargetSentinelsIn returns the locators of every finding the no-retarget rule
// raised, in the order the pipeline accumulated them.
func retargetSentinelsIn(t *testing.T, out *stepsOneToFourOutput) []string {
	t.Helper()
	var found []string
	for _, o := range out.Violations {
		if errors.Is(o.Sentinel(), topology.ErrReleaseEdgeRetarget()) {
			found = append(found, o.Locator())
		}
	}
	return found
}

// releaseWith writes a Release document carrying the given boundary tree
// and returns its path. The pointers to the Aggregate and the manifest are
// absolute so the document resolves from anywhere.
func releaseWith(t *testing.T, boundaries string) string {
	t.Helper()
	aggregate, err := filepath.Abs(invocationAggregatePath)
	if err != nil {
		t.Fatalf("resolve the Aggregate's path: %v", err)
	}
	manifest, err := filepath.Abs(invocationManifestPath)
	if err != nil {
		t.Fatalf("resolve the manifest's path: %v", err)
	}
	body := fmt.Sprintf(`apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: prod-release
  environment: production
  version: "1.0.0"
spec:
  aggregate: %s
  manifest: %s
  boundaries:
%s`, aggregate, manifest, boundaries)

	path := filepath.Join(t.TempDir(), "release.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write the Release document: %v", err)
	}
	return path
}

// compileAgainstRelease runs the real pre-render pipeline over the fullstack
// Aggregate and a Release document written for one proof.
func compileAgainstRelease(t *testing.T, aggregate []byte, releasePath string) *stepsOneToFourOutput {
	t.Helper()
	out, _ := compileWorkspace(t, workspaceFixture{
		aggregatePath: invocationAggregatePath,
		releasePath:   releasePath,
		manifestPath:  invocationManifestPath,
		catalogPath:   invocationCatalogPath,
	}, aggregate)
	return out
}

// withThreeDeclaredConnections adds a second connection to the front end's
// dependency block and a second consumer declaring one of its own, so a
// Release document can conflict with several connections at once and the
// reported order can be read against the order the Aggregate declares them in.
//
// One conflicting connection cannot prove the reporting shape: a reading that
// returned after the first would pass such a document, and returning after the
// first is exactly what the reading this rule replaced did.
func withThreeDeclaredConnections(t *testing.T, aggregate []byte) []byte {
	t.Helper()
	const anchor = "                - in: API_PORT\n                  from: service_port\n"
	const extended = anchor + `          spare-api:
            target: backend
    console:
      type: core/compute/web-ui
      uses:
        apis:
          main-api:
            target: backend
`
	if !bytes.Contains(aggregate, []byte(anchor)) {
		t.Fatalf("fixture no longer carries the connection this splice extends; the splice would be silently inert")
	}
	return bytes.Replace(aggregate, []byte(anchor), []byte(extended), 1)
}

// threeConsumersPlaced is the boundary tree the extended Aggregate needs, with
// no Release entry on any connection.
const threeConsumersPlaced = `    frontend-ns:
      directory: ./dist/frontend
      annotations:
        aggrega.ai/namespace: "frontend-zone"
      components:
        frontend:
          infra:
            replicas: 2
            port: 3003
%s
          features:
            cross-namespace-routing:
              type: core/k8s-dns-resolver
              infra:
                target_var: "API_HOST"
        console:
          infra:
            replicas: 1
%s
    backend-ns:
      directory: ./dist/backend
      annotations:
        aggrega.ai/namespace: "backend-zone"
      components:
        backend:
          infra:
            replicas: 3
            port: 8082
    data-ns:
      directory: ./dist/data
      annotations:
        aggrega.ai/namespace: "database-zone"
      components:
        data.main-db:
          infra:
            replicas: 1
`

// TestEveryRedirectedConnectionIsReportedInOneRun proves the rule is invoked
// from the production path over the whole connection set, and that it
// accumulates across consumers rather than stopping at the first offence.
//
// The locators are asserted by value because they are the other half of the win:
// the reading this rule replaced carried no authored position at all, only a
// sentence naming the connection's coordinates.
func TestEveryRedirectedConnectionIsReportedInOneRun(t *testing.T) {
	release := releaseWith(t, fmt.Sprintf(threeConsumersPlaced,
		`          uses:
            apis:
              my-api:
                target: frontend
              spare-api:
                target: frontend`,
		`          uses:
            apis:
              main-api:
                target: frontend`,
	))
	out := compileAgainstRelease(t, withThreeDeclaredConnections(t, readAggregate(t)), release)

	got := retargetSentinelsIn(t, out)
	want := []string{
		".spec.components.frontend.uses.apis.my-api",
		".spec.components.frontend.uses.apis.spare-api",
		".spec.components.console.uses.apis.main-api",
	}
	if len(got) != len(want) {
		t.Fatalf("the rule reported %d redirected connection(s), want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("finding %d is positioned at %q, want %q", i, got[i], want[i])
		}
	}

	for _, o := range out.Violations {
		if !errors.Is(o.Sentinel(), topology.ErrReleaseEdgeRetarget()) {
			continue
		}
		if o.SourceFile() == "" {
			t.Errorf("finding at %q names no file; it could not be positioned in an editor", o.Locator())
		}
		message := constraint.ObjectionMessage(o)
		if message == "" {
			t.Errorf("finding at %q renders to nothing", o.Locator())
		}
		// Both targets belong in the sentence: the one the Aggregate declared
		// says what is being displaced, and the one the Release names says
		// what displaced it. Neither alone tells the author what to change.
		if !strings.Contains(message, "backend") || !strings.Contains(message, "frontend") {
			t.Errorf("finding at %q names only one side of the disagreement: %s", o.Locator(), message)
		}
	}
}

// TestARedirectedConnectionKeepsTheDeclaredTarget proves the rule reports the
// redirection without the composition ever applying it, which is what makes
// carrying on past one conflict safe.
func TestARedirectedConnectionKeepsTheDeclaredTarget(t *testing.T) {
	release := releaseWith(t, fmt.Sprintf(threeConsumersPlaced,
		`          uses:
            apis:
              my-api:
                target: frontend`,
		"",
	))
	out := compileAgainstRelease(t, withThreeDeclaredConnections(t, readAggregate(t)), release)

	if got := retargetSentinelsIn(t, out); len(got) != 1 {
		t.Fatalf("the rule reported %d redirected connection(s), want 1: %v", len(got), got)
	}
	if target := out.GraphView.PerEdgeResolvedTarget("frontend", "apis", "my-api"); target != "backend" {
		t.Errorf("the connection resolved to %q, want backend: a refused redirection must not be applied", target)
	}
}

// TestAReleaseNamingTheDeclaredTargetRedirectsNothing is the control the
// accumulation proof needs, and it is written over a MODULE'S PUBLISHED ALIAS
// deliberately.
//
// A connection naming an alias resolves to the component the module publishes it
// for, so the resolved target and the name the author typed differ on both
// sides. A rule comparing the Release's entry against the resolved target
// would report this document — which is legitimate authoring the build accepts —
// as a redirection. Comparing what each document WROTE is what keeps it silent,
// and only a fixture whose two forms differ can tell the two comparisons apart.
func TestAReleaseNamingTheDeclaredTargetRedirectsNothing(t *testing.T) {
	aggregate := readAggregate(t)
	const anchor = "        databases:\n          - data.main-db\n"
	// The connection is rewritten into the form that carries an alias of its
	// own, because a Release entry is keyed by the alias and the short form
	// takes the target itself as one — which the Release schema will not
	// accept as a key.
	const viaAlias = "        databases:\n          primary:\n            target: data.public_db\n"
	if !bytes.Contains(aggregate, []byte(anchor)) {
		t.Fatalf("fixture no longer carries the connection this splice rewrites; the splice would be silently inert")
	}
	aggregate = bytes.Replace(aggregate, []byte(anchor), []byte(viaAlias), 1)

	release := releaseWith(t, `    frontend-ns:
      directory: ./dist/frontend
      annotations:
        aggrega.ai/namespace: "frontend-zone"
      components:
        frontend:
          infra:
            replicas: 2
            port: 3003
          features:
            cross-namespace-routing:
              type: core/k8s-dns-resolver
              infra:
                target_var: "API_HOST"
    backend-ns:
      directory: ./dist/backend
      annotations:
        aggrega.ai/namespace: "backend-zone"
      components:
        backend:
          infra:
            replicas: 3
            port: 8082
          uses:
            databases:
              primary:
                target: data.public_db
    data-ns:
      directory: ./dist/data
      annotations:
        aggrega.ai/namespace: "database-zone"
      components:
        data.main-db:
          infra:
            replicas: 1
`)
	out := compileAgainstRelease(t, aggregate, release)

	if got := retargetSentinelsIn(t, out); len(got) != 0 {
		t.Fatalf("a Release naming exactly what the Aggregate declared redirects nothing; got %v", got)
	}
	// The control is only worth having if the two forms really do differ here.
	declared := out.GraphView.PerEdgeAuthoredTarget("backend", "databases", "primary")
	resolved := out.GraphView.PerEdgeResolvedTarget("backend", "databases", "primary")
	if declared != "data.public_db" {
		t.Fatalf("the declared target reads %q, want data.public_db; the control is not exercising the alias", declared)
	}
	if resolved != "data.main-db" {
		t.Fatalf("the resolved target reads %q, want data.main-db; the control is not exercising the alias", resolved)
	}
}

// TestACleanCompositionRedirectsNothing is the control every assertion above
// needs: on the fixture as committed, the rule objects to nothing.
//
// Without it the proofs above are satisfied by a rule that objects to every
// connection, which would refuse every workspace and pass each of them.
func TestACleanCompositionRedirectsNothing(t *testing.T) {
	out := compileFixture(t, readAggregate(t))

	if got := retargetSentinelsIn(t, out); len(got) != 0 {
		t.Fatalf("the committed fixture must satisfy the rule; got %v", got)
	}
	if len(out.IRGraph.Spec.Edges) < 2 {
		t.Fatalf("the fixture graph holds %d connection(s); a control cannot vouch for a rule that was barely asked",
			len(out.IRGraph.Spec.Edges))
	}
}

// TestARedirectionMatchesThePublishedAccessor proves the finding an integration
// author matches programmatically is the finding the build raises.
//
// The published accessor forwards to the condition the compiler registers, and
// the forwarding is what this asserts against a finding a real compilation
// produced rather than against the two values side by side.
func TestARedirectionMatchesThePublishedAccessor(t *testing.T) {
	release := releaseWith(t, fmt.Sprintf(threeConsumersPlaced,
		`          uses:
            apis:
              my-api:
                target: frontend`,
		"",
	))
	out := compileAgainstRelease(t, withThreeDeclaredConnections(t, readAggregate(t)), release)

	var matched int
	for _, o := range out.Violations {
		if errors.Is(o.Sentinel(), usesedge.ErrReleaseEdgeRetarget()) {
			matched++
		}
	}
	if matched != 1 {
		t.Fatalf("the published accessor matched %d finding(s), want 1", matched)
	}
}

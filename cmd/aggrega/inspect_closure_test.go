// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// fullstackFixture is the worked example the closure cases below resolve
// against: a Release with its Aggregate and Manifest beside it, over a catalog
// carrying the entities they reach.
const fullstackFixture = "../../internal/compiler/testdata/fullstack-app"

// runInspectClosure drives the command's closure selection over the fixture and
// returns what it wrote plus the error it reported.
func runInspectClosure(t *testing.T, release string, level inspectLevel) (string, error) {
	t.Helper()
	var buf bytes.Buffer
	opts := testInspectOptions(&buf)
	opts.closure = true
	opts.level = level
	err := dispatchInspect(context.Background(),
		resolveTempWorkspace(t, tempWorkspaceOptions{}),
		fullstackFixture+"/"+release,
		[]string{fullstackFixture + "/catalog"},
		opts,
	)
	return buf.String(), err
}

// TestInspectClosure_Resolved proves the command answers the operation's Release
// half: which entities the Release reaches, and at which resolved versions.
//
// It asserts the resolved versions rather than only the references, because the
// version is the half the resolved-node view structurally cannot carry — a node
// records its type, not the version that type resolved to.
func TestInspectClosure_Resolved(t *testing.T) {
	t.Parallel()

	out, err := runInspectClosure(t, "release.yaml", levelDefault)
	if err != nil {
		t.Fatalf("closure of the worked Release should resolve; got: %v", err)
	}
	if out == "" {
		t.Fatal("the closure listing wrote nothing, so the assertions below would compare two absences")
	}
	for _, want := range []string{
		"Release: prod-release:1.0.0",
		"Aggregate: fullstack-app",
		"Manifest: prod-manifest",
		"Environment: production",
		"Entities (",
		"core/compute/api-server (1.0.0)",
		"core/datastore/postgres (1.0.0)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("closure listing missing %q; got:\n%s", want, out)
		}
	}
}

// TestInspectClosure_LevelsDiffer pins that the three verbosity levels mean here
// what they mean for every other kind, so a caller reading one level is not
// silently reading another.
func TestInspectClosure_LevelsDiffer(t *testing.T) {
	t.Parallel()

	list, err := runInspectClosure(t, "release.yaml", levelList)
	if err != nil {
		t.Fatalf("list level: %v", err)
	}
	deflt, err := runInspectClosure(t, "release.yaml", levelDefault)
	if err != nil {
		t.Fatalf("default level: %v", err)
	}
	detail, err := runInspectClosure(t, "release.yaml", levelDetail)
	if err != nil {
		t.Fatalf("detail level: %v", err)
	}
	if list == "" || deflt == "" || detail == "" {
		t.Fatal("one of the three levels wrote nothing, so comparing them proves nothing")
	}
	if strings.Contains(list, "Release: ") {
		t.Errorf("the identifiers-only level carried the structured header; got:\n%s", list)
	}
	if !strings.Contains(list, "core/compute/api-server") {
		t.Errorf("the identifiers-only level dropped the references it exists to list; got:\n%s", list)
	}
	if list == deflt {
		t.Error("the identifiers-only level and the default level wrote the same bytes")
	}
	// The fullest level is the only one that names the kind a reference was
	// collected under, and it names it as an author writes it.
	if strings.Contains(deflt, "[Recipe]") {
		t.Errorf("the default level carried the per-entry kind reserved for the fullest level; got:\n%s", deflt)
	}
	if !strings.Contains(detail, "[Recipe]") {
		t.Errorf("the fullest level did not name the kind of a recipe reference; got:\n%s", detail)
	}
}

// TestInspectClosure_ThreeOutcomes proves the three answers are told apart, which
// is the reading the by-name capabilities here already carry: a subject that
// resolves, a subject that is not there at all, and a subject that is there and
// whose closure could not be resolved.
//
// An empty listing for any of the three would report a typo, or a broken
// workspace, as a Release that depends on nothing.
func TestInspectClosure_ThreeOutcomes(t *testing.T) {
	t.Parallel()

	resolved, err := runInspectClosure(t, "release.yaml", levelDefault)
	if err != nil {
		t.Fatalf("resolved case should not fail; got: %v", err)
	}
	if !strings.Contains(resolved, "Entities (") {
		t.Errorf("resolved case wrote no listing; got:\n%s", resolved)
	}

	absentOut, absentErr := runInspectClosure(t, "no-such-release.yaml", levelDefault)
	if absentErr == nil {
		t.Fatalf("a Release that is not there should be refused; it wrote:\n%s", absentOut)
	}
	if absentOut != "" {
		t.Errorf("a refused subject still wrote a document:\n%s", absentOut)
	}
	if !strings.Contains(absentErr.Error(), "no such file or directory") {
		t.Errorf("the absent-subject refusal does not say the subject is absent; got: %v", absentErr)
	}

	brokenOut, brokenErr := runInspectClosure(t, "release-dangling-component-mapping.yaml", levelDefault)
	if brokenErr == nil {
		t.Fatalf("a Release whose closure cannot resolve should be refused; it wrote:\n%s", brokenOut)
	}
	if brokenOut != "" {
		t.Errorf("a refused subject still wrote a document:\n%s", brokenOut)
	}
	if !strings.Contains(brokenErr.Error(), "no entity in the catalog matches this URI") {
		t.Errorf("the unresolvable-subject refusal does not name what could not be resolved; got: %v", brokenErr)
	}

	// The two refusals are different answers and have to read as different
	// answers: a subject that is not there is a request to correct, while a
	// subject that is there and did not resolve is a workspace to repair.
	if absentErr.Error() == brokenErr.Error() {
		t.Errorf("the two refusals are indistinguishable: %v", absentErr)
	}
}

// TestInspectClosure_RefusesASubjectThatResolvesNone proves the selection is
// refused rather than silently dropped on a subject with no closure — the same
// reading the graph selection carries, for the same reason: a command that
// rendered the subject's own view and dropped the request would read to the
// caller exactly like a subject whose closure is empty.
func TestInspectClosure_RefusesASubjectThatResolvesNone(t *testing.T) {
	t.Parallel()

	for name, subject := range map[string]string{
		"a component file":  fullstackFixture + "/catalog/base/components/api-server.yaml",
		"an aggregate file": fullstackFixture + "/aggregate.yaml",
		"a catalog name":    "core/compute/api-server",
	} {
		var buf bytes.Buffer
		opts := testInspectOptions(&buf)
		opts.closure = true
		err := dispatchInspect(context.Background(),
			resolveTempWorkspace(t, tempWorkspaceOptions{}),
			subject,
			[]string{fullstackFixture + "/catalog"},
			opts,
		)
		if err == nil {
			t.Errorf("%s: the closure selection was accepted; it wrote:\n%s", name, buf.String())
			continue
		}
		if !strings.Contains(err.Error(), "--"+flagClosure) {
			t.Errorf("%s: the refusal does not name the selection that was refused; got: %v", name, err)
		}
		if !strings.Contains(err.Error(), "no dependency closure") {
			t.Errorf("%s: the refusal does not say what the subject resolves none of; got: %v", name, err)
		}
		if buf.String() != "" {
			t.Errorf("%s: a refused request still wrote a document:\n%s", name, buf.String())
		}
	}
}

// TestInspectClosure_RefusesASecondDocumentInOneInvocation proves the command
// refuses a request for two documents at once rather than picking one of them.
//
// It drives the assembled command rather than the dispatch, because the mutual
// exclusion is declared on the flags and a test below that layer would not reach
// it.
func TestInspectClosure_RefusesASecondDocumentInOneInvocation(t *testing.T) {
	t.Parallel()

	for name, args := range map[string][]string{
		"as authored":    {"--" + flagClosure, "--raw"},
		"fully resolved": {"--" + flagClosure, "--resolved"},
		"the graph":      {"--" + flagClosure, "--" + flagIRGraph},
		"a format":       {"--" + flagClosure, "--output", "yaml"},
	} {
		cmd := newInspectCmd()
		var buf bytes.Buffer
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetArgs(append([]string{fullstackFixture + "/release.yaml"}, args...))
		if err := cmd.Execute(); err == nil {
			t.Errorf("%s: asking for two documents at once was accepted; it wrote:\n%s", name, buf.String())
		}
	}
}

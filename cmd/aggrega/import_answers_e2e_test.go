// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// The values an import's drafts leave empty that no source can hold, at the
// command line: listed and refused where nobody can be asked, asked one by one at
// a terminal and kept in the answers file so a later run asks nothing, and a
// secret's answer that is not a name refused with nothing written.
//
// The workspace's catalog holds the draft-composition catalog, the draft-set
// importer and the owing entities; the source drafts an Aggregate and a Release
// owing four values — the Release's spec.manifest by its kind, the instance's
// model and image by its Component, and the credential's origin, a secret's, by
// its Feature.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/importops"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// layOwingImport lays a workspace whose catalog holds the owing catalog and
// writes the owing draft-set source in a directory of its own outside it,
// returning the workspace root and the source's path.
func layOwingImport(t *testing.T) (root, source string) {
	t.Helper()
	root = t.TempDir()
	importopstest.WriteConfiguration(t, root)
	importopstest.LayOwingCatalog(t, root, filepath.Join(root, workspaceconfig.ConventionalCatalogDir))
	source = filepath.Join(t.TempDir(), "drafts.txt")
	writeFixture(t, source, importopstest.OwingSource())
	return root, source
}

// runImportWithInput is runImportAtTheRoot with input on the command's standard
// input.
func runImportWithInput(t *testing.T, root, input string, args ...string) (code int, stdout, stderr, process string) {
	t.Helper()
	cmd := newRootCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetIn(strings.NewReader(input))
	cmd.SetArgs(append([]string{"--workspace-root", root, "import"}, args...))
	process = captureStderr(t, func() {
		code = executeRoot(context.Background(), cmd)
	})
	return code, out.String(), errOut.String(), process
}

// atATerminal has the command's terminal detectors answer that standard input
// and standard output are a terminal for the rest of the test.
func atATerminal(t *testing.T) {
	t.Helper()
	input, output := isTerminal, fileTerminal
	isTerminal = func() bool { return true }
	fileTerminal = func(io.Writer) bool { return true }
	t.Cleanup(func() { isTerminal, fileTerminal = input, output })
}

// owedLines are the warnings naming the four values the owing drafts owe.
var owedLines = []string{
	"warn: aggregate.yaml: spec.components.api.values.model is still empty: The model the server calls.",
	"warn: release.yaml: spec.manifest is still empty: ",
	"warn: release.yaml: spec.components.api.infra.image is still empty: Container image running the server.",
	"warn: release.yaml: spec.components.api.features.cred.infra.origin is still empty: Where the credential comes from.",
}

// TestAnImportNobodyCanBeAskedInNamesEveryOwedValueAndExitsOne is the shape an
// import takes in continuous integration: standard input is no terminal and no
// answers file exists. Nothing is asked; each owed value is named on standard
// error; the drafts are written with their empty values; the answers file is
// written beside the source with every owed key present and empty, and named
// among what was written; and the command exits 1 with the one sentence naming
// how many values are owed and where.
//
// 🔴 PLANTED DEFECT: return nil from OwedRefusal (internal/importops/import.go):
// the command exits 0 and the case fails on the exit status.
func TestAnImportNobodyCanBeAskedInNamesEveryOwedValueAndExitsOne(t *testing.T) {
	// Not parallel: captureStderr swaps process-global os.Stderr.
	withCLIRegistrations(t, nil)
	// The draft-set importer is built before the machine is isolated, since the
	// toolchain building it reads its caches from there.
	root, source := layOwingImport(t)
	isolateFromThisMachine(t)

	code, stdout, stderr, process := runImportWithInput(t, root, "", source, "--format",
		importopstest.DraftSetFormat, "--offline")
	if code != 1 {
		t.Fatalf("the import exited %d; want 1\nstdout:\n%s\nstderr:\n%s\nprocess:\n%s", code, stdout, stderr, process)
	}
	answers := filepath.Join(filepath.Dir(source), importops.AnswersFileName)
	if !strings.Contains(process, "4 values the drafts leave empty are still owed, listed in the answers file "+answers) {
		t.Errorf("the refusal does not name the count and the answers file:\n%s", process)
	}
	for _, line := range owedLines {
		if !strings.Contains(stderr, line) {
			t.Errorf("standard error does not name the owed value %q:\n%s", line, stderr)
		}
	}
	if strings.Contains(stderr, "Answer each one") {
		t.Errorf("a question was asked with no terminal:\n%s", stderr)
	}
	rel, err := filepath.Rel(root, answers)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout, "wrote release.yaml\n") || !strings.Contains(stdout, "wrote "+rel+"\n") {
		t.Errorf("standard output does not name the drafts and the answers file written:\n%s", stdout)
	}
	written, err := os.ReadFile(answers)
	if err != nil {
		t.Fatalf("the answers file was not written: %v", err)
	}
	for _, key := range []string{`spec.components.api.values.model: ""`, `spec.manifest: ""`,
		`spec.components.api.infra.image: ""`, `spec.components.api.features.cred.infra.origin: ""`} {
		if !strings.Contains(string(written), key) {
			t.Errorf("the answers file does not hold %s:\n%s", key, written)
		}
	}
	if release, err := os.ReadFile(filepath.Join(root, "release.yaml")); err != nil ||
		string(release) != importopstest.OwingRelease {
		t.Errorf("the Release was not drafted with its empty values (%v):\n%s", err, release)
	}
}

// TestAnImportAtATerminalAsksEachOwedValueAndARerunAsksNothing answers the
// questions an import asks at a terminal — a secret's answer that is not a name
// refused and asked again — and then syncs the drafts at a terminal with nothing
// typed: the second run asks nothing, since every answer is in the answers file,
// and finds the drafts in step.
//
// 🔴 PLANTED DEFECT: have importAsker return nil at a terminal too: nothing is
// asked, the command exits 1 owing four values, and the case fails on the exit
// status before it reaches the answers file.
func TestAnImportAtATerminalAsksEachOwedValueAndARerunAsksNothing(t *testing.T) {
	// Not parallel: captureStderr swaps process-global os.Stderr, and the terminal
	// detectors are package state.
	withCLIRegistrations(t, nil)
	root, source := layOwingImport(t)
	isolateFromThisMachine(t)
	atATerminal(t)

	typed := "gpt-4.1\nowing\nexample/server:1.0.0\nsk-PLANTED/value+==\napi-credentials\n"
	code, stdout, stderr, process := runImportWithInput(t, root, typed, source, "--format",
		importopstest.DraftSetFormat, "--offline")
	if code != 0 {
		t.Fatalf("the answered import exited %d\nstdout:\n%s\nstderr:\n%s\nprocess:\n%s", code, stdout, stderr, process)
	}
	for _, want := range []string{"Answer each one", "release.yaml spec.components.api.features.cred.infra.origin",
		"This is a secret", "refused: that is not a name"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the questions on standard error do not carry %q:\n%s", want, stderr)
		}
	}
	answers := filepath.Join(filepath.Dir(source), importops.AnswersFileName)
	written, err := os.ReadFile(answers)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`spec.components.api.values.model: "gpt-4.1"`, `spec.manifest: "owing"`,
		`spec.components.api.infra.image: "example/server:1.0.0"`,
		`spec.components.api.features.cred.infra.origin: "api-credentials"`} {
		if !strings.Contains(string(written), want) {
			t.Errorf("the answers file does not keep %s:\n%s", want, written)
		}
	}
	if strings.Contains(string(written), "PLANTED") {
		t.Errorf("a refused answer reached the answers file:\n%s", written)
	}
	release, err := os.ReadFile(filepath.Join(root, "release.yaml"))
	if err != nil || !strings.Contains(string(release), `origin: "api-credentials"`) ||
		!strings.Contains(string(release), `manifest: "owing"`) {
		t.Errorf("the answers were not written into the Release (%v):\n%s", err, release)
	}

	code, stdout, stderr, process = runImportWithInput(t, root, "", source, "--format",
		importopstest.DraftSetFormat, "--offline", "--sync")
	if code != 0 {
		t.Fatalf("the sync exited %d\nstdout:\n%s\nstderr:\n%s\nprocess:\n%s", code, stdout, stderr, process)
	}
	if strings.Contains(stderr, "Answer each one") {
		t.Errorf("the sync asked again:\n%s", stderr)
	}
}

// TestTheToolAndTheSessionReportTheOwedValuesAndTakeTheAnswersFile drives the
// assistant's import tool and the editor's session over the owing drafts, the
// source inside the served workspace: neither asks; each reports every owed value
// among its diagnostics, the tool failing the call with the one sentence and the
// session closing its diagnostics with it; each writes the answers file beside
// the source. Then each is sent `answers` naming a file inside the workspace that
// answers every value, and owes nothing.
func TestTheToolAndTheSessionReportTheOwedValuesAndTakeTheAnswersFile(t *testing.T) {
	// Not parallel: the environment is set for the whole test.
	withCLIRegistrations(t, nil)
	const answered = "aggregate.yaml:\n  spec.components.api.values.model: gpt-4.1\nrelease.yaml:\n" +
		"  spec.manifest: owing\n  spec.components.api.infra.image: example/server:1.0.0\n" +
		"  spec.components.api.features.cred.infra.origin: api-credentials\n"
	type diagnostic struct {
		Severity string `json:"severity"`
		Message  string `json:"message"`
	}
	lay := func(t *testing.T) string {
		t.Helper()
		root, _ := layOwingImport(t)
		writeFixture(t, filepath.Join(root, "src", "drafts.txt"), importopstest.OwingSource())
		writeFixture(t, filepath.Join(root, "answered", importops.AnswersFileName), answered)
		// Offline, so an answered import pulls nothing.
		importopstest.WriteOfflineConfiguration(t, root)
		return root
	}
	roots := []string{lay(t), lay(t), lay(t), lay(t)}
	isolateFromThisMachine(t)

	t.Run("the assistant's tool", func(t *testing.T) {
		answer := callAssistantTool(t, roots[0], "import", map[string]any{
			"source": "src/drafts.txt", "format": importopstest.DraftSetFormat, "output_dir": "drafts"})
		if !answer.isError || !strings.HasPrefix(answer.text, "4 values the drafts leave empty are still owed") {
			t.Fatalf("the call did not fail with the owed sentence: %s", answer.text)
		}
		var payload struct {
			Diagnostics []diagnostic `json:"diagnostics"`
			Written     []string     `json:"written"`
		}
		if err := json.Unmarshal(answer.structured, &payload); err != nil {
			t.Fatal(err)
		}
		var warned int
		for _, d := range payload.Diagnostics {
			if d.Severity == "warn" && strings.Contains(d.Message, " is still empty: ") {
				warned++
			}
		}
		if warned != 4 || len(payload.Written) == 0 {
			t.Errorf("the answer carries %d owed warning(s) and wrote %v; want 4 and the drafts", warned, payload.Written)
		}
		if _, err := os.Stat(filepath.Join(roots[0], "src", importops.AnswersFileName)); err != nil {
			t.Errorf("the answers file was not written beside the source: %v", err)
		}

		answer = callAssistantTool(t, roots[1], "import", map[string]any{"source": "src/drafts.txt",
			"format": importopstest.DraftSetFormat, "output_dir": "drafts", "answers": "answered/" + importops.AnswersFileName})
		if answer.isError {
			t.Fatalf("an answered import failed: %s", answer.text)
		}
		if release, err := os.ReadFile(filepath.Join(roots[1], "drafts", "release.yaml")); err != nil ||
			!strings.Contains(string(release), `origin: "api-credentials"`) {
			t.Errorf("the answers were not written into the Release (%v):\n%s", err, release)
		}
	})

	t.Run("the session", func(t *testing.T) {
		frame, stderr := callSession(t, roots[2], "aggrega/import", map[string]any{
			"source": "src/drafts.txt", "format": importopstest.DraftSetFormat, "outputDir": "drafts"})
		if raw, refused := frame["error"]; refused {
			t.Fatalf("aggrega/import was refused: %s\nstderr:\n%s", raw, stderr)
		}
		var result struct {
			Diagnostics []diagnostic `json:"diagnostics"`
		}
		if err := json.Unmarshal(frame["result"], &result); err != nil {
			t.Fatal(err)
		}
		n := len(result.Diagnostics)
		if n == 0 || !strings.HasPrefix(result.Diagnostics[n-1].Message, "4 values the drafts leave empty are still owed") {
			t.Fatalf("the diagnostics do not close with the owed sentence: %+v", result.Diagnostics)
		}

		frame, stderr = callSession(t, roots[3], "aggrega/import", map[string]any{"source": "src/drafts.txt",
			"format": importopstest.DraftSetFormat, "outputDir": "drafts", "answers": "answered/" + importops.AnswersFileName})
		if raw, refused := frame["error"]; refused {
			t.Fatalf("an answered aggrega/import was refused: %s\nstderr:\n%s", raw, stderr)
		}
		if err := json.Unmarshal(frame["result"], &result); err != nil {
			t.Fatal(err)
		}
		for _, d := range result.Diagnostics {
			if strings.Contains(d.Message, "still owed") || strings.Contains(d.Message, " is still empty: ") {
				t.Errorf("an answered import still reports an owed value: %+v", d)
			}
		}
	})
}

// TestAPlantedSecretInTheAnswersFileIsRefusedAndNothingIsWritten plants a value
// that is not a name as the answer to the credential's origin: the command exits
// 1 naming the key and never the answer, and writes no draft.
func TestAPlantedSecretInTheAnswersFileIsRefusedAndNothingIsWritten(t *testing.T) {
	// Not parallel: captureStderr swaps process-global os.Stderr.
	withCLIRegistrations(t, nil)
	root, source := layOwingImport(t)
	isolateFromThisMachine(t)
	const planted = "sk-PLANTED/value+=="
	answers := filepath.Join(filepath.Dir(source), importops.AnswersFileName)
	writeFixture(t, answers, "release.yaml:\n  spec.components.api.features.cred.infra.origin: \""+planted+"\"\n")

	code, stdout, stderr, process := runImportWithInput(t, root, "", source, "--format",
		importopstest.DraftSetFormat, "--offline")
	if code != 1 {
		t.Fatalf("the import exited %d; want 1\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if !strings.Contains(process, "spec.components.api.features.cred.infra.origin") ||
		strings.Contains(process+stdout+stderr, planted) {
		t.Errorf("the refusal must name the key and never the answer:\n%s", process)
	}
	if _, err := os.Stat(filepath.Join(root, "release.yaml")); !os.IsNotExist(err) {
		t.Errorf("a refused import drafted the Release (stat: %v)", err)
	}
}

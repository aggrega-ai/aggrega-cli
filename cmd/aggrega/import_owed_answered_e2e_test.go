// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// The values an import's drafts still owe, carried as data by the editor's
// session and the assistant's import tool, and answered by sending each back:
// the import judges a sent answer as it judges one typed at a terminal, keeps it
// in the answers file and writes it into the drafts, and a secret's answer that
// is not a name comes back on its owed entry, in the import's own words, with
// nothing kept.
//
// The workspace's catalog holds the draft-composition catalog, the draft-set
// importer and the owing entities; the source, inside the workspace, drafts an
// Aggregate and a Release owing four values, the credential's origin a secret's.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	schemas "github.com/aggrega-ai/aggrega-schemas"

	"github.com/aggrega-ai/aggrega-cli/internal/importops"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
)

// The two sentences a secret's owed entry carries, written out here rather than
// read from the import, deliberately: the cases pin the words a client shows, so
// a change to either sentence fails here instead of travelling unseen.
const (
	wantSecretAdvice = "This is a secret: answer with where it comes from — a variable name, ${NAME}, or the name " +
		"of a Kubernetes Secret — never the secret itself."
	wantSecretRefusal = "that is not a name: a secret's answer names where the secret comes from — a variable name, " +
		"${NAME}, or the name of a Kubernetes Secret — and never the secret itself"
)

// plantedSecret is an answer to the credential's origin that is not a name.
const plantedSecret = "sk-PLANTED/value+=="

// owedEntry is one entry of an answer's owed list, whichever surface carried it;
// advice is the secret's advice under the key that surface spells it with, nil
// where the entry carries none.
type owedEntry struct {
	File      string
	Key       []string
	Question  string
	Sensitive bool
	Advice    *string
}

// wantOwedEntries are the four values the owing drafts owe, in the order the
// drafts are written, as an owed list names them.
//
// Each question is its schema's own description. The Release's spec.manifest is
// owed by its kind's schema, so its question is read from that schema here
// rather than written out: the case is about the entry carrying the description,
// not about the description's wording, which the schema owns.
var wantOwedEntries = []owedEntry{
	{File: "aggregate.yaml", Key: []string{"spec", "components", "api", "values", "model"},
		Question: "The model the server calls."},
	{File: "release.yaml", Key: []string{"spec", "manifest"}, Question: releaseManifestDescription()},
	{File: "release.yaml", Key: []string{"spec", "components", "api", "infra", "image"},
		Question: "Container image running the server."},
	{File: "release.yaml", Key: []string{"spec", "components", "api", "features", "cred", "infra", "origin"},
		Question: "Where the credential comes from.", Sensitive: true},
}

// releaseManifestDescription is the description the Release schema the binary
// carries gives spec.manifest, its whitespace folded to single spaces as an owed
// value's question folds it; empty where the schema cannot be read, which fails
// every case comparing against it.
func releaseManifestDescription() string {
	var release struct {
		Properties struct {
			Spec struct {
				Properties struct {
					Manifest struct {
						Description string `json:"description"`
					} `json:"manifest"`
				} `json:"properties"`
			} `json:"spec"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(schemas.ReleaseV1beta1), &release); err != nil {
		return ""
	}
	return strings.Join(strings.Fields(release.Properties.Spec.Properties.Manifest.Description), " ")
}

// originEntry is the credential's origin, the one secret among the owed values.
var originEntry = wantOwedEntries[3]

// answeredEntries are answers for the four owed values, the origin's given as
// origin.
func answeredEntries(origin string) []map[string]any {
	answers := []string{"gpt-4.1", "owing", "example/server:1.0.0", origin}
	out := make([]map[string]any, 0, len(wantOwedEntries))
	for i, o := range wantOwedEntries {
		out = append(out, map[string]any{"file": o.File, "key": o.Key, "answer": answers[i]})
	}
	return out
}

// layOwingWorkspaceSource lays an owing workspace, offline so an answered import
// pulls nothing, with the owing source inside it at src/drafts.txt, and returns
// its root.
func layOwingWorkspaceSource(t *testing.T) string {
	t.Helper()
	root, _ := layOwingImport(t)
	writeFixture(t, filepath.Join(root, "src", "drafts.txt"), importopstest.OwingSource())
	importopstest.WriteOfflineConfiguration(t, root)
	return root
}

// checkOwedEntries compares an owed list with want, entry by entry, the advice of
// each secret's entry with advice and every other entry carrying none.
func checkOwedEntries(t *testing.T, got, want []owedEntry, advice string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("owed lists %d value(s): %+v; want %d", len(got), got, len(want))
	}
	for i, w := range want {
		g := got[i]
		if g.File != w.File || !slices.Equal(g.Key, w.Key) || g.Question != w.Question || g.Sensitive != w.Sensitive {
			t.Errorf("owed[%d] is %+v; want %+v", i, g, w)
		}
		switch {
		case w.Sensitive && (g.Advice == nil || *g.Advice != advice):
			t.Errorf("owed[%d], a secret's value, advises %v; want %q", i, g.Advice, advice)
		case !w.Sensitive && g.Advice != nil:
			t.Errorf("owed[%d], not a secret's value, advises %q; want no advice", i, *g.Advice)
		}
	}
}

// checkAnsweredOnDisk reads the answers file beside the source and the drafts
// beneath drafts in root: each accepted answer is kept in the file and written
// into its draft, the origin's only where it was accepted, and the planted answer
// reaches neither.
func checkAnsweredOnDisk(t *testing.T, root string, originAccepted bool) {
	t.Helper()
	answers, err := os.ReadFile(filepath.Join(root, "src", importops.AnswersFileName))
	if err != nil {
		t.Fatalf("read the answers file: %v", err)
	}
	release, err := os.ReadFile(filepath.Join(root, "drafts", "release.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	aggregate, err := os.ReadFile(filepath.Join(root, "drafts", "aggregate.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, kept := range []string{`spec.components.api.values.model: "gpt-4.1"`, `spec.manifest: "owing"`,
		`spec.components.api.infra.image: "example/server:1.0.0"`} {
		if !strings.Contains(string(answers), kept) {
			t.Errorf("the answers file does not keep %s:\n%s", kept, answers)
		}
	}
	if !strings.Contains(string(aggregate), `model: "gpt-4.1"`) {
		t.Errorf("the model was not written into the Aggregate:\n%s", aggregate)
	}
	for _, written := range []string{`manifest: "owing"`, `image: "example/server:1.0.0"`} {
		if !strings.Contains(string(release), written) {
			t.Errorf("the Release does not carry %s:\n%s", written, release)
		}
	}
	origin, keptOrigin := `origin: "api-credentials"`, `spec.components.api.features.cred.infra.origin: "api-credentials"`
	if !originAccepted {
		origin, keptOrigin = `origin: ""`, `spec.components.api.features.cred.infra.origin: ""`
	}
	if !strings.Contains(string(release), origin) || !strings.Contains(string(answers), keptOrigin) {
		t.Errorf("the origin is not %s in the Release and the answers file:\n%s\n%s", origin, release, answers)
	}
	if strings.Contains(string(answers)+string(release)+string(aggregate), plantedSecret) {
		t.Errorf("the refused answer reached the answers file or a draft:\n%s\n%s", answers, release)
	}
}

// sessionOwed is an owed entry as the editor's session carries it.
type sessionOwed struct {
	File         string   `json:"file"`
	Key          []string `json:"key"`
	Question     string   `json:"question"`
	Sensitive    bool     `json:"sensitive"`
	SecretAdvice *string  `json:"secretAdvice"`
}

// sessionImportOwed calls aggrega/import on the session over root and returns its
// owed list, its raw owed member and its diagnostics' messages, failing the test
// where the request is refused.
func sessionImportOwed(t *testing.T, root string, params map[string]any) ([]owedEntry, string, []string) {
	t.Helper()
	frame, stderr := callSession(t, root, "aggrega/import", params)
	if raw, refused := frame["error"]; refused {
		t.Fatalf("aggrega/import was refused: %s\nstderr:\n%s", raw, stderr)
	}
	if strings.Contains(string(frame["result"]), plantedSecret) {
		t.Errorf("the answer repeats the refused answer: %s", frame["result"])
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(frame["result"], &members); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Owed        []sessionOwed `json:"owed"`
		Diagnostics []struct {
			Message string `json:"message"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(frame["result"], &result); err != nil {
		t.Fatal(err)
	}
	owed := make([]owedEntry, 0, len(result.Owed))
	for _, o := range result.Owed {
		owed = append(owed, owedEntry{File: o.File, Key: o.Key, Question: o.Question, Sensitive: o.Sensitive,
			Advice: o.SecretAdvice})
	}
	messages := make([]string, 0, len(result.Diagnostics))
	for _, d := range result.Diagnostics {
		messages = append(messages, d.Message)
	}
	return owed, string(members["owed"]), messages
}

// TestTheSessionReportsOwedValuesAsDataAndTakesTheirAnswers imports the owing
// drafts through the editor's session: the answer lists the four owed values in
// owed, the origin a secret's with the advice a terminal shows. Sending each back
// under answered, with sync, keeps every answer in the answers file beside the
// source, writes each into its draft, and answers an empty owed list.
//
// 🔴 PLANTED DEFECT: leave Ask unset in dispatchImport (internal/serve/import.go):
// the answered sync is handed no answer, still owes four values, and the case
// fails on the owed list before it reaches the disk.
func TestTheSessionReportsOwedValuesAsDataAndTakesTheirAnswers(t *testing.T) {
	// Not parallel: the environment is set for the whole test.
	withCLIRegistrations(t, nil)
	root := layOwingWorkspaceSource(t)
	isolateFromThisMachine(t)

	params := map[string]any{"source": "src/drafts.txt", "format": importopstest.DraftSetFormat, "outputDir": "drafts"}
	owed, _, _ := sessionImportOwed(t, root, params)
	checkOwedEntries(t, owed, wantOwedEntries, wantSecretAdvice)

	params["sync"], params["answered"] = true, answeredEntries("api-credentials")
	owed, raw, messages := sessionImportOwed(t, root, params)
	if raw != "[]" || len(owed) != 0 {
		t.Errorf("an answered sync owes %s; want an empty list", raw)
	}
	for _, m := range messages {
		if strings.Contains(m, "still owed") || strings.Contains(m, " is still empty: ") {
			t.Errorf("an answered sync still reports an owed value: %s", m)
		}
	}
	checkAnsweredOnDisk(t, root, true)
}

// TestTheSessionReturnsARefusedSecretAnswerOnItsOwedEntry sends a value that is
// not a name as the origin's answer beside three good answers: the three are kept
// and written, the origin stays owed with the import's own refusal as its
// advice, the refused answer reaches neither the answers file, a draft nor the
// answer, and the diagnostics close with one value owed.
//
// 🔴 PLANTED DEFECT: have GivenAnswers.Advice return o.SecretAdvice() alone
// (internal/importops/answers.go): the origin advises as before it was answered,
// and the case fails on its entry.
func TestTheSessionReturnsARefusedSecretAnswerOnItsOwedEntry(t *testing.T) {
	// Not parallel: the environment is set for the whole test.
	withCLIRegistrations(t, nil)
	root := layOwingWorkspaceSource(t)
	isolateFromThisMachine(t)

	params := map[string]any{"source": "src/drafts.txt", "format": importopstest.DraftSetFormat, "outputDir": "drafts"}
	sessionImportOwed(t, root, params)

	params["sync"], params["answered"] = true, answeredEntries(plantedSecret)
	owed, _, messages := sessionImportOwed(t, root, params)
	checkOwedEntries(t, owed, []owedEntry{originEntry}, wantSecretRefusal)
	if n := len(messages); n == 0 || !strings.HasPrefix(messages[n-1], "1 value the drafts leave empty is still owed") {
		t.Errorf("the diagnostics do not close with one value owed: %q", messages)
	}
	checkAnsweredOnDisk(t, root, false)
}

// toolOwed is an owed entry as the assistant's import tool carries it.
type toolOwed struct {
	File         string   `json:"file"`
	Key          []string `json:"key"`
	Question     string   `json:"question"`
	Sensitive    bool     `json:"sensitive"`
	SecretAdvice *string  `json:"secret_advice"`
}

// toolImportOwed calls the assistant's import tool over root and returns the
// answer and its owed list and raw owed member.
func toolImportOwed(t *testing.T, root string, arguments map[string]any) (assistantToolAnswer, []owedEntry, string) {
	t.Helper()
	answer := callAssistantTool(t, root, "import", arguments)
	if strings.Contains(answer.text+string(answer.structured), plantedSecret) {
		t.Errorf("the answer repeats the refused answer: %s\n%s", answer.text, answer.structured)
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(answer.structured, &members); err != nil {
		t.Fatalf("decode the payload: %v (%s)", err, answer.text)
	}
	var payload struct {
		Owed []toolOwed `json:"owed"`
	}
	if err := json.Unmarshal(answer.structured, &payload); err != nil {
		t.Fatal(err)
	}
	owed := make([]owedEntry, 0, len(payload.Owed))
	for _, o := range payload.Owed {
		owed = append(owed, owedEntry{File: o.File, Key: o.Key, Question: o.Question, Sensitive: o.Sensitive,
			Advice: o.SecretAdvice})
	}
	return answer, owed, string(members["owed"])
}

// TestTheAssistantToolReportsOwedValuesAsDataAndTakesTheirAnswers drives the
// assistant's import tool as the session cases drive the editor's: the call
// owing values fails with the owed sentence and lists them in owed; one answering
// each with sync succeeds owing nothing, every answer kept and written; and one
// answering the origin with something that is not a name fails owing that value
// alone, the refusal as its secret_advice.
//
// 🔴 PLANTED DEFECT: leave Ask unset in importInvoke (internal/mcp/tool_import.go):
// the answered call still fails owing four values.
func TestTheAssistantToolReportsOwedValuesAsDataAndTakesTheirAnswers(t *testing.T) {
	// Not parallel: the environment is set for the whole test.
	withCLIRegistrations(t, nil)
	roots := []string{layOwingWorkspaceSource(t), layOwingWorkspaceSource(t)}
	isolateFromThisMachine(t)

	for _, row := range []struct {
		name           string
		origin         string
		failsWith      string
		owed           []owedEntry
		advice         string
		originAccepted bool
	}{
		{name: "every answer accepted", origin: "api-credentials", originAccepted: true},
		{name: "the secret's answer refused", origin: plantedSecret,
			failsWith: "1 value the drafts leave empty is still owed", owed: []owedEntry{originEntry},
			advice: wantSecretRefusal},
	} {
		root := roots[0]
		if !row.originAccepted {
			root = roots[1]
		}
		t.Run(row.name, func(t *testing.T) {
			arguments := map[string]any{"source": "src/drafts.txt", "format": importopstest.DraftSetFormat,
				"output_dir": "drafts"}
			answer, owed, _ := toolImportOwed(t, root, arguments)
			if !answer.isError || !strings.HasPrefix(answer.text, "4 values the drafts leave empty are still owed") {
				t.Fatalf("the call did not fail with the owed sentence: %s", answer.text)
			}
			checkOwedEntries(t, owed, wantOwedEntries, wantSecretAdvice)

			arguments["sync"], arguments["answered"] = true, answeredEntries(row.origin)
			answer, owed, raw := toolImportOwed(t, root, arguments)
			switch {
			case row.failsWith == "" && answer.isError:
				t.Fatalf("the answered call failed: %s", answer.text)
			case row.failsWith != "" && (!answer.isError || !strings.HasPrefix(answer.text, row.failsWith)):
				t.Fatalf("the call did not fail with %q: %s", row.failsWith, answer.text)
			}
			if row.owed == nil && raw != "[]" {
				t.Errorf("an answered call owes %s; want an empty list", raw)
			}
			checkOwedEntries(t, owed, row.owed, row.advice)
			checkAnsweredOnDisk(t, root, row.originAccepted)
		})
	}
}

// TestTheTerminalShowsTheSecretSentenceUnchanged answers the questions an import
// asks at a terminal, a secret's answer that is not a name first, and pins the
// bytes the origin's question is put in: the question, the secret's advice under
// it, and the refusal in its place when the question is put again.
//
// 🔴 PLANTED DEFECT: change one character of the advice in Owed.SecretAdvice
// (internal/importops/owed.go): the question's bytes differ and the case fails.
func TestTheTerminalShowsTheSecretSentenceUnchanged(t *testing.T) {
	// Not parallel: captureStderr swaps process-global os.Stderr, and the terminal
	// detectors are package state.
	withCLIRegistrations(t, nil)
	root, source := layOwingImport(t)
	isolateFromThisMachine(t)
	atATerminal(t)

	typed := "gpt-4.1\nowing\nexample/server:1.0.0\n" + plantedSecret + "\napi-credentials\n"
	code, stdout, stderr, process := runImportWithInput(t, root, typed, source, "--format",
		importopstest.DraftSetFormat, "--offline")
	if code != 0 {
		t.Fatalf("the answered import exited %d\nstdout:\n%s\nstderr:\n%s\nprocess:\n%s", code, stdout, stderr, process)
	}
	want := "\nrelease.yaml spec.components.api.features.cred.infra.origin\n  Where the credential comes from.\n  " +
		wantSecretAdvice + "\n> " + "  refused: " + wantSecretRefusal + "\n> "
	if !strings.Contains(stderr, want) {
		t.Errorf("the origin's question is not put in the bytes it was\nwant:\n%s\nstandard error:\n%s", want, stderr)
	}
}

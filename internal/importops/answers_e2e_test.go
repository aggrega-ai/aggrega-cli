// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi/go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The values a draft leaves empty that no source can hold, end to end through
// Import: listed from the drafted documents' kind schemas and from the catalog
// schemas of what they declare, answered from the answers file or by a person,
// written into the drafts, and refused where a secret's answer is not a name —
// then the drafted Release's dependencies pulled through the surface's seam.

// wantOwedKeys are the values the owing drafts owe, in the order the drafts are
// written and, within one, the order the keys are.
var wantOwedKeys = []string{
	"aggregate.yaml spec.components.api.values.model",
	"release.yaml spec.manifest",
	"release.yaml spec.components.api.infra.image",
	"release.yaml spec.components.api.features.cred.infra.origin",
}

// owingWorkspace resolves a scratch workspace whose catalog holds the
// draft-composition catalog and the owing entities, offline where asked, and
// returns it with the path of a source drafting the owing drafts, in a directory
// of its own.
func owingWorkspace(t *testing.T, offline bool) (*workspaceconfig.ResolvedWorkspace, string) {
	t.Helper()
	wsDir := aWorkspaceDir(t)
	if offline {
		importopstest.WriteOfflineConfiguration(t, wsDir)
	} else {
		importopstest.WriteConfiguration(t, wsDir)
	}
	catalogDir := filepath.Join(wsDir, workspaceconfig.ConventionalCatalogDir)
	importopstest.LayOwingCatalog(t, wsDir, catalogDir)
	rw := resolveWorkspaceAt(t, wsDir)
	source := importopstest.WriteSource(t, filepath.Join(t.TempDir(), "drafts.txt"), importopstest.OwingSource())
	return rw, source
}

// owingImport imports the owing drafts over rw, with opts' answering fields,
// into outputDir, failing the test where the import is refused.
func owingImport(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, source, outputDir string, opts Options) Result {
	t.Helper()
	res, err := owingImportErr(rw, source, outputDir, opts)
	if err != nil {
		t.Fatalf("the import was refused: %v", err)
	}
	return res
}

func owingImportErr(rw *workspaceconfig.ResolvedWorkspace, source, outputDir string, opts Options) (Result, error) {
	opts.SourcePath, opts.SourceName = source, source
	opts.Format = importopstest.DraftSetFormat
	opts.OutputDir = outputDir
	opts.ResolvePlugin = (&recordingCascade{}).resolve
	return Import(context.Background(), rw, opts)
}

// owedKeys renders a result's owed values as "<file> <key>".
func owedKeys(owed []Owed) []string {
	out := make([]string, 0, len(owed))
	for _, o := range owed {
		out = append(out, o.File+" "+o.Key())
	}
	return out
}

// diagnosticsBesideTheOwed is a result's diagnostics without the warnings that
// name the values its drafts owe, for a case about what else an import reports.
func diagnosticsBesideTheOwed(res Result) []abi.Diagnostic {
	owed := make([]abi.Diagnostic, 0, len(res.Owed))
	for _, o := range res.Owed {
		owed = append(owed, owedDiagnostic(o))
	}
	return slices.DeleteFunc(slices.Clone(res.Diagnostics), func(d abi.Diagnostic) bool {
		return slices.Contains(owed, d)
	})
}

// recordingPull is a pull seam that records each Release it is asked to pull and
// answers with the account and the failure it is given.
type recordingPull struct {
	releases []string
	notices  []string
	err      error
}

func (p *recordingPull) pull(_ context.Context, release string) ([]string, error) {
	p.releases = append(p.releases, release)
	return p.notices, p.err
}

// TestAnImportNobodyIsAskedInListsEveryValueItsDraftsOwe is the shape an import
// takes in continuous integration: nothing is asked and no answers file exists.
// Every value a schema says is supplied and the drafts leave empty is owed — the
// Release's own spec.manifest by its kind's schema, the instance's model and image
// by its Component's schema, the credential's origin by its Feature's — each
// reported as a warning, the answers file is written beside the source with every
// owed key present and empty, the drafts keep their empty values, the record
// carries no pointer to the answers file beside the source, the refusal names how
// many values are owed and where, and nothing is pulled.
//
// 🔴 PLANTED DEFECTS:
//   - read only the kind schemas in readOwed (return before the catalog reading):
//     the model, image and origin rows vanish and the key list reddens.
//   - have the declared-keys reading follow the required lists (pass false to
//     readPlaceholdersUnder in PlaceholdersUnder): none of the catalog values is
//     required, so the same three rows vanish.
func TestAnImportNobodyIsAskedInListsEveryValueItsDraftsOwe(t *testing.T) {
	t.Parallel()
	rw, source := owingWorkspace(t, false)
	outputDir := filepath.Join(rw.Root(), "drafts")
	pull := &recordingPull{}
	res := owingImport(t, rw, source, outputDir, Options{Pull: pull.pull})

	if got := owedKeys(res.Owed); !slices.Equal(got, wantOwedKeys) {
		t.Fatalf("the drafts owe\n  %v\nwant\n  %v", got, wantOwedKeys)
	}
	if !res.Owed[3].Sensitive || res.Owed[0].Sensitive {
		t.Errorf("only the credential's origin is a secret's value; owed %+v", res.Owed)
	}
	if got := res.Owed[2].Question(); got != "Container image running the server." {
		t.Errorf("the image is described as %q; want its schema's own description", got)
	}
	for _, o := range res.Owed {
		if !slices.Contains(res.Diagnostics, owedDiagnostic(o)) {
			t.Errorf("the diagnostics do not name the owed %s %s: %+v", o.File, o.Key(), res.Diagnostics)
		}
	}
	answers := filepath.Join(filepath.Dir(source), AnswersFileName)
	if res.Answers != answers || !res.AnswersWritten {
		t.Fatalf("the answers file is %q (written %v); want %q written", res.Answers, res.AnswersWritten, answers)
	}
	written, err := os.ReadFile(answers)
	if err != nil {
		t.Fatalf("read the answers file: %v", err)
	}
	read, _, err := readAnswers(answers)
	if err != nil {
		t.Fatalf("the answers file written does not read back: %v\n%s", err, written)
	}
	for _, o := range res.Owed {
		if v, ok := read[o.File][o.Key()]; !ok || v != "" {
			t.Errorf("the answers file holds %s %s as %q (present %v); want it present and empty", o.File, o.Key(), v, ok)
		}
	}
	if !strings.Contains(string(written), "# Container image running the server.") {
		t.Errorf("the answers file does not describe the image above its key:\n%s", written)
	}
	release, err := os.ReadFile(filepath.Join(outputDir, "release.yaml"))
	if err != nil || string(release) != importopstest.OwingRelease {
		t.Errorf("the drafted Release changed with nothing answered (%v):\n%s", err, release)
	}
	record, err := os.ReadFile(filepath.Join(outputDir, RecordFileName))
	if err != nil || strings.Contains(string(record), `"answers"`) {
		t.Errorf("the record points at the answers file beside the source (%v):\n%s", err, record)
	}
	var owed *OwedValuesError
	if refusal := res.OwedRefusal(); !errors.As(refusal, &owed) ||
		!strings.HasPrefix(refusal.Error(), "4 values the drafts leave empty are still owed") ||
		!strings.Contains(refusal.Error(), answers) {
		t.Errorf("the refusal reads %v; want the count and the answers file", refusal)
	}
	if len(pull.releases) != 0 {
		t.Errorf("drafts still owing values were pulled: %v", pull.releases)
	}
}

// TestAnAnsweredImportWritesEveryAnswerIntoItsDraftsAndPulls answers every owed
// value in an answers file named apart from the source and syncs the drafts the
// unanswered import wrote: each answer replaces its empty value and nothing else
// in the drafts moves, nothing is owed, the record points at the named answers
// file, the answers file keeps every answer, and the drafted Release is pulled
// once, at its place in the output directory.
//
// 🔴 PLANTED DEFECT: compose the record before the answers are written into the
// drafts (pass `draft` from before answerDrafts to draftedFiles): the sync then
// finds the parts it wrote changed on disk and reports drift on every run.
func TestAnAnsweredImportWritesEveryAnswerIntoItsDraftsAndPulls(t *testing.T) {
	t.Parallel()
	rw, source := owingWorkspace(t, false)
	outputDir := filepath.Join(rw.Root(), "drafts")
	owingImport(t, rw, source, outputDir, Options{})

	named := filepath.Join(t.TempDir(), "answers", AnswersFileName)
	body := "aggregate.yaml:\n  spec.components.api.values.model: gpt-4.1\nrelease.yaml:\n" +
		"  spec.manifest: owing\n  spec.components.api.infra.image: \"example/server:1.0.0\"\n" +
		"  spec.components.api.features.cred.infra.origin: api-credentials\n"
	if err := os.MkdirAll(filepath.Dir(named), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(named, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	pull := &recordingPull{}
	res := owingImport(t, rw, source, outputDir, Options{Sync: true, Answers: named, Pull: pull.pull})

	if len(res.Owed) != 0 || res.OwedRefusal() != nil {
		t.Fatalf("an answered import still owes %v", owedKeys(res.Owed))
	}
	release, err := os.ReadFile(filepath.Join(outputDir, "release.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	want := strings.NewReplacer(`manifest: ""`, `manifest: "owing"`, `image: ""`, `image: "example/server:1.0.0"`,
		`origin: ""`, `origin: "api-credentials"`).Replace(importopstest.OwingRelease)
	if string(release) != want {
		t.Errorf("the answered Release is\n%s\nwant every answer in place of its empty value and nothing else moved\n%s",
			release, want)
	}
	aggregate, err := os.ReadFile(filepath.Join(outputDir, "aggregate.yaml"))
	if err != nil || !strings.Contains(string(aggregate), `model: "gpt-4.1"`) || !strings.Contains(string(aggregate), `text: ""`) {
		t.Errorf("the answered Aggregate does not carry the model, or the block scalar moved (%v):\n%s", err, aggregate)
	}
	record, err := os.ReadFile(filepath.Join(outputDir, RecordFileName))
	if err != nil {
		t.Fatal(err)
	}
	if pointer := answersPointer(outputDir, named); !strings.Contains(string(record), `"answers": "`+pointer+`"`) {
		t.Errorf("the record does not point at the named answers file %q:\n%s", pointer, record)
	}
	kept, _, err := readAnswers(named)
	if err != nil || kept["release.yaml"]["spec.components.api.features.cred.infra.origin"] != "api-credentials" {
		t.Errorf("the answers file lost an answer (%v): %+v", err, kept)
	}
	if wantPull := []string{filepath.Join(outputDir, "release.yaml")}; !slices.Equal(pull.releases, wantPull) {
		t.Errorf("the pull was asked for %v; want %v", pull.releases, wantPull)
	}
	if !res.pulled {
		t.Error("the result does not say the Release's dependencies were pulled")
	}

	// A second sync, naming no answers file, reads the one the record points at and
	// finds the drafts in step.
	again := owingImport(t, rw, source, outputDir, Options{Sync: true, Delivery: authoredcontent.DeliveryReport})
	if len(again.Drift) != 0 || len(again.Owed) != 0 {
		t.Errorf("a sync reading the recorded answers file drifted %+v and owes %v", again.Drift, owedKeys(again.Owed))
	}
}

// TestASecretAnsweredWithSomethingThatIsNotANameIsRefused plants a value that is
// not a name as the answer to the credential's origin: the import is refused
// naming the key and never the answer, and nothing is written — no draft, no
// record, and the answers file is left as the reader wrote it.
func TestASecretAnsweredWithSomethingThatIsNotANameIsRefused(t *testing.T) {
	t.Parallel()
	const planted = "sk-PLANTED/value+=="
	rw, source := owingWorkspace(t, false)
	answers := filepath.Join(filepath.Dir(source), AnswersFileName)
	body := "release.yaml:\n  spec.components.api.features.cred.infra.origin: \"" + planted + "\"\n"
	if err := os.WriteFile(answers, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	outputDir := filepath.Join(rw.Root(), "drafts")
	_, err := owingImportErr(rw, source, outputDir, Options{})
	var refused *SecretAnswerError
	if !errors.As(err, &refused) {
		t.Fatalf("the import was not refused for the secret's answer: %v", err)
	}
	if strings.Contains(err.Error(), planted) || !strings.Contains(err.Error(), refused.Key) {
		t.Errorf("the refusal must name the key and never the answer: %v", err)
	}
	if entries, _ := os.ReadDir(outputDir); len(entries) != 0 {
		t.Errorf("a refused import wrote %v", entries)
	}
	if after, _ := os.ReadFile(answers); string(after) != body {
		t.Errorf("the answers file was rewritten:\n%s", after)
	}
}

// besideReferenceOrigin is the credential's origin declared through a reference
// to a definition the infra pillar declares itself — a pointer read from the root
// of the pillar's own schema, as a catalog contract's pointers are — its
// sensitivity and its description written beside the reference while the
// definition declares neither the one nor the other.
const besideReferenceOrigin = `      $defs:
        source: { type: string, description: "The value the origin is declared through." }
      origin:
        $ref: "#/$defs/source"
        sensitive: true
        description: "Where the credential comes from, written beside its reference."
`

// pillarDefinitionImage is example/owing's image declared through a reference to
// a definition the infra pillar declares itself, the definition carrying the
// description and nothing declared beside the reference.
const pillarDefinitionImage = `      $defs:
        image: { type: string, description: "Container image running the server, as its definition describes it." }
      image: { $ref: "#/$defs/image" }
`

// catalogEdit replaces, in one owing entity, the declaration written as written
// with replacement: rel is the entity's path in the catalog and body the entity
// as LayOwingCatalog lays it.
type catalogEdit struct {
	rel, body, written, replacement string
}

// editedOwingWorkspace resolves a scratch workspace like owingWorkspace's, with
// each edit made to the owing entity it names.
func editedOwingWorkspace(t *testing.T, edits ...catalogEdit) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	wsDir := aWorkspaceDir(t)
	importopstest.WriteConfiguration(t, wsDir)
	catalogDir := filepath.Join(wsDir, workspaceconfig.ConventionalCatalogDir)
	importopstest.LayOwingCatalog(t, wsDir, catalogDir)
	for _, e := range edits {
		edited := strings.Replace(e.body, e.written, e.replacement, 1)
		if edited == e.body {
			t.Fatalf("%s no longer declares\n%s", e.rel, e.written)
		}
		if err := os.WriteFile(filepath.Join(catalogDir, filepath.FromSlash(e.rel)), []byte(edited), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return resolveWorkspaceAt(t, wsDir)
}

// TestASecretDeclaredBesideAReferenceIsAnsweredOnlyWithAName imports the owing
// drafts against a Feature declaring the credential's origin sensitive beside a
// reference: the origin is owed as a secret's value, described as written beside
// the reference; and an answers file answering it with a planted value that is
// not a name refuses the import naming the key and never the answer, writing no
// draft and no record and leaving the answers file as the reader wrote it.
//
// 🔴 PLANTED DEFECTS:
//   - read the sensitivity off the schema the reference names alone
//     (declaredAlong in validateops reading only the resolved schema): the origin
//     is owed as an ordinary value, the planted answer is written into the drafted
//     Release, and the refusal never comes.
//   - read an attachment against its Feature's whole contract rather than pillar
//     by pillar (PlaceholdersUnder(schema, att, …) in place of pillarPlaceholders):
//     the reference resolves to nothing and the origin is not owed at all.
func TestASecretDeclaredBesideAReferenceIsAnsweredOnlyWithAName(t *testing.T) {
	t.Parallel()
	const key = "spec.components.api.features.cred.infra.origin"
	rw := editedOwingWorkspace(t, catalogEdit{rel: "features/credential.yaml", body: importopstest.OwingFeature,
		written:     "      origin: { type: string, sensitive: true, description: \"Where the credential comes from.\" }\n",
		replacement: besideReferenceOrigin})

	unanswered := importopstest.WriteSource(t, filepath.Join(t.TempDir(), "drafts.txt"), importopstest.OwingSource())
	res := owingImport(t, rw, unanswered, filepath.Join(rw.Root(), "listed"), Options{})
	var origin *Owed
	for i := range res.Owed {
		if res.Owed[i].File == "release.yaml" && res.Owed[i].Key() == key {
			origin = &res.Owed[i]
		}
	}
	if origin == nil {
		t.Fatalf("the origin is not owed; owed %v", owedKeys(res.Owed))
	}
	if !origin.Sensitive || origin.Description != "Where the credential comes from, written beside its reference." {
		t.Errorf("the origin is owed as %+v; want a secret's value described as written beside its reference", *origin)
	}

	const planted = "sk-PLANTED/value+=="
	source := importopstest.WriteSource(t, filepath.Join(t.TempDir(), "drafts.txt"), importopstest.OwingSource())
	answers := filepath.Join(filepath.Dir(source), AnswersFileName)
	body := "release.yaml:\n  " + key + ": \"" + planted + "\"\n"
	if err := os.WriteFile(answers, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	outputDir := filepath.Join(rw.Root(), "drafts")
	_, err := owingImportErr(rw, source, outputDir, Options{})
	var refused *SecretAnswerError
	if !errors.As(err, &refused) {
		t.Fatalf("the import was not refused for the secret's answer: %v", err)
	}
	if strings.Contains(err.Error(), planted) || refused.Key != key {
		t.Errorf("the refusal must name %s and never the answer: %v", key, err)
	}
	if entries, _ := os.ReadDir(outputDir); len(entries) != 0 {
		t.Errorf("a refused import wrote %v", entries)
	}
	if after, _ := os.ReadFile(answers); string(after) != body {
		t.Errorf("the answers file was rewritten:\n%s", after)
	}
}

// TestAValueDeclaredThroughAPillarDefinitionIsOwedAndAsked imports the owing
// drafts against a Component declaring the image through a reference to a
// definition its infra pillar declares: the image is owed — not a secret's value,
// described as the definition describes it — a person is asked for it, and the
// answer given is written into the drafted Release in place of its empty value.
//
// 🔴 PLANTED DEFECT: read an instance against its Component's whole contract rather
// than pillar by pillar (PlaceholdersUnder(schema, n, …) in place of
// pillarPlaceholders in owedReader.instance): the reference resolves to nothing
// from the contract's root, the image is not owed, and nobody is asked for it.
func TestAValueDeclaredThroughAPillarDefinitionIsOwedAndAsked(t *testing.T) {
	t.Parallel()
	const key = "spec.components.api.infra.image"
	rw := editedOwingWorkspace(t, catalogEdit{rel: "components/owing.yaml", body: importopstest.OwingComponent,
		written:     "      image: { type: string, description: \"Container image running the server.\" }\n",
		replacement: pillarDefinitionImage})
	source := importopstest.WriteSource(t, filepath.Join(t.TempDir(), "drafts.txt"), importopstest.OwingSource())

	var asked []Owed
	ask := func(o Owed, _ string) (string, error) {
		if o.File == "release.yaml" && o.Key() == key {
			asked = append(asked, o)
			return "example/server:1.0.0", nil
		}
		return "", nil
	}
	outputDir := filepath.Join(rw.Root(), "drafts")
	res := owingImport(t, rw, source, outputDir, Options{Ask: ask})

	if len(asked) != 1 {
		t.Fatalf("the image was asked for %d time(s); want once. Still owed: %v", len(asked), owedKeys(res.Owed))
	}
	if asked[0].Sensitive || asked[0].Description != "Container image running the server, as its definition describes it." {
		t.Errorf("the image was asked for as %+v; want a value that is not a secret's, described by its definition", asked[0])
	}
	if slices.Contains(owedKeys(res.Owed), "release.yaml "+key) {
		t.Errorf("the answered image is still owed: %v", owedKeys(res.Owed))
	}
	release, err := os.ReadFile(filepath.Join(outputDir, "release.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(release), `image: "example/server:1.0.0"`) || !strings.Contains(string(release), `note: "kept"`) {
		t.Errorf("the drafted Release does not carry the answered image beside the kept note:\n%s", release)
	}
}

// TestAPersonIsAskedForWhatTheAnswersFileLeaves asks through the seam: every value
// the file leaves unanswered is put once, in order; a secret's answer that is not a
// name is refused and asked again; an empty answer leaves its value owed; and the
// answers given are written into the answers file. Under a reporting delivery
// nothing is asked.
func TestAPersonIsAskedForWhatTheAnswersFileLeaves(t *testing.T) {
	t.Parallel()
	rw, source := owingWorkspace(t, false)
	answers := filepath.Join(filepath.Dir(source), AnswersFileName)
	if err := os.WriteFile(answers, []byte("release.yaml:\n  spec.manifest: owing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var asked []string
	replies := map[string][]string{
		"spec.components.api.values.model":               {""},
		"spec.components.api.infra.image":                {"example/server:1.0.0"},
		"spec.components.api.features.cred.infra.origin": {"not/a name", "api-credentials"},
	}
	ask := func(o Owed, refused string) (string, error) {
		asked = append(asked, o.Key()+"|"+refused)
		next := replies[o.Key()]
		reply := next[0]
		replies[o.Key()] = next[1:]
		return reply, nil
	}

	reported := owingImport(t, rw, source, filepath.Join(rw.Root(), "preview"), Options{Ask: ask,
		Delivery: authoredcontent.DeliveryReport})
	if len(asked) != 0 || len(reported.Owed) != 3 {
		t.Fatalf("a reporting delivery asked %v and owes %v; want nothing asked and three owed", asked,
			owedKeys(reported.Owed))
	}

	res := owingImport(t, rw, source, filepath.Join(rw.Root(), "drafts"), Options{Ask: ask})
	want := []string{
		"spec.components.api.values.model|",
		"spec.components.api.infra.image|",
		"spec.components.api.features.cred.infra.origin|",
		"spec.components.api.features.cred.infra.origin|" + secretAnswerRefusal,
	}
	if !slices.Equal(asked, want) {
		t.Errorf("the questions were\n  %v\nwant\n  %v", asked, want)
	}
	if got := owedKeys(res.Owed); !slices.Equal(got, []string{"aggregate.yaml spec.components.api.values.model"}) {
		t.Errorf("after the answers the drafts owe %v; want only the model left empty", got)
	}
	kept, _, err := readAnswers(answers)
	if err != nil {
		t.Fatal(err)
	}
	if kept["release.yaml"]["spec.components.api.infra.image"] != "example/server:1.0.0" ||
		kept["release.yaml"]["spec.components.api.features.cred.infra.origin"] != "api-credentials" ||
		kept["release.yaml"]["spec.manifest"] != "owing" {
		t.Errorf("the answers file does not keep every answer given: %+v", kept)
	}
	if written, _ := os.ReadFile(answers); strings.Contains(string(written), "not/a name") {
		t.Errorf("a refused answer reached the answers file:\n%s", written)
	}
}

// TestAnOfflineImportPullsNothingAndNamesThePull answers every owed value and
// imports into an offline workspace: nothing is pulled, and an information
// diagnostic names the pull to run next.
func TestAnOfflineImportPullsNothingAndNamesThePull(t *testing.T) {
	t.Parallel()
	rw, source := owingWorkspace(t, true)
	named := filepath.Join(t.TempDir(), AnswersFileName)
	body := "aggregate.yaml:\n  spec.components.api.values.model: gpt-4.1\nrelease.yaml:\n  spec.manifest: owing\n" +
		"  spec.components.api.infra.image: example/server:1.0.0\n" +
		"  spec.components.api.features.cred.infra.origin: api-credentials\n"
	if err := os.WriteFile(named, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	outputDir := filepath.Join(rw.Root(), "drafts")
	pull := &recordingPull{}
	res := owingImport(t, rw, source, outputDir, Options{Answers: named, Pull: pull.pull})
	if len(pull.releases) != 0 || res.pulled {
		t.Errorf("an offline import pulled %v", pull.releases)
	}
	want := abi.Diagnostic{Severity: abi.SeverityInfo, Message: "the workspace is offline, so the dependencies of the " +
		"drafted Release were not pulled; run aggrega pull --release " + filepath.Join(outputDir, "release.yaml") + " next"}
	if !slices.Contains(res.Diagnostics, want) {
		t.Errorf("the diagnostics do not name the pull to run: %+v", res.Diagnostics)
	}
}

// TestOneAnswersFileServesTheDraftsOfEveryTarget answers the owing drafts from a
// file that also answers a key these drafts do not owe — the Secret name only
// another target's drafts ask for — under a comment of the reader's: the import
// owes nothing, is not refused, writes the answers file not at all, so it stays
// byte for byte as the reader wrote it, and one information line names the answer
// it kept without applying.
func TestOneAnswersFileServesTheDraftsOfEveryTarget(t *testing.T) {
	t.Parallel()
	rw, source := owingWorkspace(t, true)
	answers := filepath.Join(filepath.Dir(source), AnswersFileName)
	body := "# answers for every target\naggregate.yaml:\n  spec.components.api.values.model: gpt-4.1\n" +
		"release.yaml:\n  spec.manifest: owing\n  spec.components.api.infra.image: example/server:1.0.0\n" +
		"  spec.components.api.features.cred.infra.origin: api-credentials\n" +
		"  # only the other target's drafts ask for this one\n" +
		"  spec.components.api.features.admin.infra.origin: admin-credentials\n"
	if err := os.WriteFile(answers, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	res := owingImport(t, rw, source, filepath.Join(rw.Root(), "drafts"), Options{})
	if len(res.Owed) != 0 || res.OwedRefusal() != nil || res.AnswersWritten {
		t.Fatalf("the import owes %v (answers file written %v); want nothing owed and nothing written",
			owedKeys(res.Owed), res.AnswersWritten)
	}
	if after, _ := os.ReadFile(answers); string(after) != body {
		t.Errorf("the answers file changed:\n%s\nwant it as the reader wrote it\n%s", after, body)
	}
	var kept int
	for _, d := range res.Diagnostics {
		if d.Severity == abi.SeverityInfo && strings.Contains(d.Message, "spec.components.api.features.admin.infra.origin") {
			kept++
		}
		if d.Severity == abi.SeverityWarn && strings.Contains(d.Message, "answers file") {
			t.Errorf("an answer these drafts do not owe drew a warning: %s", d.Message)
		}
	}
	if kept != 1 {
		t.Errorf("one information line names the answer kept and not applied; got %d in %+v", kept, res.Diagnostics)
	}
}

// TestAPullThatFailsLeavesTheDraftsWritten has the pull seam fail: the import
// returns no error, its result names every file it wrote, carries the pull's own
// account among its diagnostics, and reports the failure as its PullRefusal — a
// PullError carrying the Release and the cause.
func TestAPullThatFailsLeavesTheDraftsWritten(t *testing.T) {
	t.Parallel()
	rw, source := owingWorkspace(t, false)
	named := filepath.Join(t.TempDir(), AnswersFileName)
	body := "aggregate.yaml:\n  spec.components.api.values.model: gpt-4.1\nrelease.yaml:\n  spec.manifest: owing\n" +
		"  spec.components.api.infra.image: example/server:1.0.0\n" +
		"  spec.components.api.features.cred.infra.origin: api-credentials\n"
	if err := os.WriteFile(named, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("the registry could not be reached")
	pull := &recordingPull{err: cause, notices: []string{"reused the cached example/owing"}}
	outputDir := filepath.Join(rw.Root(), "drafts")
	res := owingImport(t, rw, source, outputDir, Options{Answers: named, Pull: pull.pull})
	refusal := res.PullRefusal()
	var failed *PullError
	if !errors.As(refusal, &failed) || !errors.Is(refusal, cause) || res.pulled {
		t.Fatalf("the failed pull is reported as %v (pulled %v); want a PullError wrapping its cause", refusal,
			res.pulled)
	}
	if !slices.Contains(res.Diagnostics, infoDiagnostic("reused the cached example/owing")) {
		t.Errorf("the pull's own account is not among the diagnostics: %+v", res.Diagnostics)
	}
	if failed.Release != filepath.Join(outputDir, "release.yaml") {
		t.Errorf("the failure names the Release %q", failed.Release)
	}
	if !slices.Contains(res.Written, "release.yaml") || !slices.Contains(res.Written, "aggregate.yaml") {
		t.Errorf("the result beside the failure does not name the drafts written: %v", res.Written)
	}
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAnAnswerToASecretIsAName holds the names a secret's answer may be — a
// variable name, a reference to one, a Kubernetes object's name — against values
// that are none of them, the shape a secret itself usually takes among them.
func TestAnAnswerToASecretIsAName(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		answer string
		name   bool
	}{
		{"OPENAI_API_KEY", true},
		{"_private", true},
		{"${OPENAI_API_KEY}", true},
		{"langflow-admin", true},
		{"dify.credentials", true},
		{strings.Repeat("a", 253), true},
		{"", false},
		{"sk-PLANTED/value+==", false},
		{"${OPENAI-API-KEY}", false},
		{"$OPENAI_API_KEY", false},
		{"Langflow-Admin", false},
		{"has space", false},
		{"-leading-hyphen", false},
		// Shaped as a Kubernetes name only — a hyphen is no variable's — and one
		// character longer than a Kubernetes object's name may be. A run of letters
		// that long is still a variable name, which has no length of its own.
		{strings.Repeat("a-", 126) + "ab", false},
		{strings.Repeat("a-", 126) + "a", true},
	} {
		if got := AnswerIsAName(tc.answer); got != tc.name {
			t.Errorf("AnswerIsAName(%q) = %v, want %v", tc.answer, got, tc.name)
		}
	}
}

// TestAnAnswerReplacesItsEmptyValueAndNothingElse writes an answer in place of
// the empty quoted value at a key path and holds every other byte — comments,
// the other keys, a multi-byte character earlier on the line, the line endings —
// to the draft as written. A key the draft does not hold, and one whose value is
// not empty, are refused rather than written somewhere else.
func TestAnAnswerReplacesItsEmptyValueAndNothingElse(t *testing.T) {
	t.Parallel()
	draft := "spec:\n  # kept\n  a: {é: \"\"}\n  b:\n    c: ''\n    d: \"set\"\n"
	got, err := writeAnswer(draft, []string{"spec", "b", "c"}, `with "quotes"`)
	if err != nil {
		t.Fatal(err)
	}
	if want := "spec:\n  # kept\n  a: {é: \"\"}\n  b:\n    c: \"with \\\"quotes\\\"\"\n    d: \"set\"\n"; got != want {
		t.Errorf("the answered draft is\n%q\nwant\n%q", got, want)
	}
	got, err = writeAnswer(draft, []string{"spec", "a", "é"}, "x")
	if err != nil {
		t.Fatal(err)
	}
	if want := "spec:\n  # kept\n  a: {é: \"x\"}\n  b:\n    c: ''\n    d: \"set\"\n"; got != want {
		t.Errorf("an answer after a multi-byte character landed at\n%q\nwant\n%q", got, want)
	}
	for _, keys := range [][]string{{"spec", "missing"}, {"spec", "b", "d"}} {
		if _, err := writeAnswer(draft, keys, "x"); err == nil {
			t.Errorf("an answer at %v was written; want it refused", keys)
		}
	}

	// A drafted Aggregate's text is evaluated as a template before it is read, so
	// an answer written into one holding two opening braces is escaped and reads
	// back as typed; a Release is not evaluated, and is written as typed.
	aggregate := "kind: Aggregate\nspec:\n  v: \"\"\n"
	got, err = writeAnswer(aggregate, []string{"spec", "v"}, "{{ .x }}")
	if err != nil {
		t.Fatal(err)
	}
	if want := "kind: Aggregate\nspec:\n  v: \"{{`{{`}} .x }}\"\n"; got != want {
		t.Errorf("an answer into an Aggregate is written\n%q\nwant it escaped\n%q", got, want)
	}
	release := "kind: Release\nspec:\n  v: \"\"\n"
	got, err = writeAnswer(release, []string{"spec", "v"}, "{{ .x }}")
	if err != nil {
		t.Fatal(err)
	}
	if want := "kind: Release\nspec:\n  v: \"{{ .x }}\"\n"; got != want {
		t.Errorf("an answer into a Release is written\n%q\nwant it as typed\n%q", got, want)
	}
}

// TestTheAnswersFileReadsBackWhatItWasWrittenWith writes the answers file for a
// set of owed values and reads it back: each key under its file, the answer given
// or an empty one, a key holding a full stop written so it reads back as one key,
// and each value's description in a comment above its key.
func TestTheAnswersFileReadsBackWhatItWasWrittenWith(t *testing.T) {
	t.Parallel()
	owed := []Owed{
		{File: "aggregate.yaml", Keys: []string{"spec", "components", "model", "values", "model"},
			Description: "The model the server calls."},
		{File: "release.yaml", Keys: []string{"spec", "components", "api", "annotations", "example.com/owner"},
			Takes: "a value that is not empty"},
		{File: "release.yaml", Keys: []string{"spec", "components", "api", "features", "cred", "infra", "origin"},
			Description: "Where the credential comes from.", Sensitive: true},
	}
	given := answers{"aggregate.yaml": {owed[0].Key(): "gpt-4.1"}, "release.yaml": {owed[1].Key(): "", owed[2].Key(): ""}}
	content, changed, err := answersFileContent(nil, owed, given)
	if err != nil || !changed {
		t.Fatalf("composing a new answers file reports changed %v, %v", changed, err)
	}
	path := filepath.Join(t.TempDir(), AnswersFileName)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	read, exists, err := readAnswers(path)
	if err != nil || !exists {
		t.Fatalf("the answers file does not read back (%v, exists %v):\n%s", err, exists, content)
	}
	for _, o := range owed {
		if got, ok := read[o.File][o.Key()]; !ok || got != given[o.File][o.Key()] {
			t.Errorf("%s %s reads back as %q (present %v); want %q", o.File, o.Key(), got, ok, given[o.File][o.Key()])
		}
	}
	for _, want := range []string{"# The model the server calls.", "# a value that is not empty",
		"# Where the credential comes from. A secret: name where it comes from, never the value."} {
		if !strings.Contains(content, want) {
			t.Errorf("the answers file does not carry %q:\n%s", want, content)
		}
	}
	if _, exists, err := readAnswers(filepath.Join(t.TempDir(), AnswersFileName)); exists || err != nil {
		t.Errorf("a missing answers file reads as existing %v, %v; want it answering nothing", exists, err)
	}
}

// TestTheAnswersFileKeepsWhatTheImportDoesNotOwn composes the answers file over
// one a reader already wrote: an answer for a key these drafts do not owe — the
// value another target's drafts owe — and the reader's own comment stay where
// they are, in order; a file already holding every owed key with its answer is
// reported unchanged, byte for byte; and an owed key it lacks is added after its
// file's other keys, everything else kept.
func TestTheAnswersFileKeepsWhatTheImportDoesNotOwn(t *testing.T) {
	t.Parallel()
	image := Owed{File: "release.yaml", Keys: []string{"spec", "components", "api", "infra", "image"},
		Description: "Container image running the server."}
	email := Owed{File: "release.yaml", Keys: []string{"spec", "components", "api", "infra", "owner_email"},
		Description: "Email address of the owner account."}
	written := "# my notes on this Release\nrelease.yaml:\n  # only the helm drafts ask for this\n" +
		"  spec.components.api.features.cred.infra.origin: api-credentials\n" +
		"  spec.components.api.infra.image: example/server:1.0.0\n"

	held := answers{"release.yaml": {image.Key(): "example/server:1.0.0"}}
	got, changed, err := answersFileContent([]byte(written), []Owed{image}, held)
	if err != nil || changed || got != written {
		t.Fatalf("a file holding every owed answer reads as changed %v (%v):\n%s", changed, err, got)
	}

	held["release.yaml"][email.Key()] = ""
	got, changed, err = answersFileContent([]byte(written), []Owed{image, email}, held)
	if err != nil || !changed {
		t.Fatalf("a file lacking an owed key reads as changed %v (%v)", changed, err)
	}
	order := []string{"# my notes on this Release", "# only the helm drafts ask for this",
		"spec.components.api.features.cred.infra.origin: api-credentials", "spec.components.api.infra.image:",
		"# Email address of the owner account.", `spec.components.api.infra.owner_email: ""`}
	at := 0
	for _, want := range order {
		i := strings.Index(got[at:], want)
		if i < 0 {
			t.Fatalf("the answers file does not keep %q in order:\n%s", want, got)
		}
		at += i + len(want)
	}
}

// TestAnAnswersFileThatIsNotOneIsRefused holds each shape an answers file cannot
// take to a refusal naming the file.
func TestAnAnswersFileThatIsNotOneIsRefused(t *testing.T) {
	t.Parallel()
	for _, body := range []string{
		"- a list\n",
		"release.yaml: a value\n",
		"release.yaml:\n  spec.manifest: {nested: value}\n",
		"release.yaml: [\n",
	} {
		path := filepath.Join(t.TempDir(), AnswersFileName)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		_, _, err := readAnswers(path)
		if refused, ok := err.(*AnswersFileError); !ok || refused.Path != path {
			t.Errorf("the answers file %q read as %v; want it refused naming the file", body, err)
		}
	}
}

// TestTheDefaultAnswersFileSitsBesideTheSource places the default answers file
// inside a source directory and beside a source file.
func TestTheDefaultAnswersFileSitsBesideTheSource(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "flow.json")
	if err := os.WriteFile(file, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := DefaultAnswersPath(dir), filepath.Join(dir, AnswersFileName); got != want {
		t.Errorf("for a directory the answers file is %s, want %s", got, want)
	}
	if got, want := DefaultAnswersPath(file), filepath.Join(dir, AnswersFileName); got != want {
		t.Errorf("for a file the answers file is %s, want %s", got, want)
	}
}

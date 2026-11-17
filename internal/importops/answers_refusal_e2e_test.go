// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// A person answering the import's questions never loses what they typed to a
// refusal: end to end through Import, with the questions driven through the Ask
// seam, every refusal that does not turn on the answers comes before the first
// question, and a refusal judged on the answered drafts comes after each answer
// is already in the answers file.

// scriptedAsk answers each question from replies, keyed by the owed key path, one
// reply per asking, and records every question put as "<key>|<refusal>".
type scriptedAsk struct {
	replies map[string][]string
	asked   []string
}

func (s *scriptedAsk) ask(o Owed, refused string) (string, error) {
	s.asked = append(s.asked, o.Key()+"|"+refused)
	next := s.replies[o.Key()]
	if len(next) == 0 {
		return "", nil
	}
	s.replies[o.Key()] = next[1:]
	return next[0], nil
}

// TestARefusalThatDoesNotTurnOnTheAnswersAsksNothing plants, one per row, a
// refusal judged without the answers, into an import a person would otherwise be
// asked in: each refuses with its own condition, no question is put, and the
// answers file is left byte for byte as it was.
//
// 🔴 PLANTED DEFECTS:
//   - judge the draft places after the answers (move checkDraftPlaces below
//     answerDrafts in Import): the existing-Aggregate row is asked every value
//     before it is refused.
//   - judge the file's secret answers one owed value at a time, as each is reached
//     (fold the first loop of answerOwed into the asking loop): the secret row is
//     asked the three values before the origin before it is refused.
func TestARefusalThatDoesNotTurnOnTheAnswersAsksNothing(t *testing.T) {
	t.Parallel()
	const planted = "sk-PLANTED/value+=="
	rows := []struct {
		name string
		// answers is what the answers file beside the source holds before the
		// import; empty is no file.
		answers string
		// existing drafts the owing drafts into the output directory first, so an
		// Aggregate is already on disk there.
		existing bool
		refused  func(error) bool
	}{
		{
			name:     "an Aggregate already at a drafted path",
			existing: true,
			refused: func(err error) bool {
				var e *ExistingEntityError
				return errors.As(err, &e)
			},
		},
		{
			name:    "a secret's answer in the file that is not a name, after values the file leaves unanswered",
			answers: "release.yaml:\n  spec.components.api.features.cred.infra.origin: \"" + planted + "\"\n",
			refused: func(err error) bool {
				var e *SecretAnswerError
				return errors.As(err, &e) && !strings.Contains(err.Error(), planted)
			},
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			rw, source := owingWorkspace(t, false)
			outputDir := filepath.Join(rw.Root(), "drafts")
			answers := filepath.Join(filepath.Dir(source), AnswersFileName)
			if row.existing {
				owingImport(t, rw, source, outputDir, Options{})
			}
			if row.answers != "" {
				if err := os.WriteFile(answers, []byte(row.answers), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			before, beforeErr := os.ReadFile(answers)

			person := &scriptedAsk{replies: map[string][]string{
				"spec.components.api.values.model":               {"gpt-4.1"},
				"spec.manifest":                                  {"owing"},
				"spec.components.api.infra.image":                {"example/server:1.0.0"},
				"spec.components.api.features.cred.infra.origin": {"api-credentials"},
			}}
			_, err := owingImportErr(rw, source, outputDir, Options{Ask: person.ask})
			if err == nil || !row.refused(err) {
				t.Fatalf("the import was refused with %v; want the planted refusal", err)
			}
			if len(person.asked) != 0 {
				t.Errorf("a refused import asked %v before refusing", person.asked)
			}
			after, afterErr := os.ReadFile(answers)
			if string(after) != string(before) || (beforeErr == nil) != (afterErr == nil) {
				t.Errorf("the refused import changed the answers file\nbefore (%v):\n%s\nafter (%v):\n%s", beforeErr, before,
					afterErr, after)
			}
		})
	}
}

// TestAnAnswerGivenBeforeASyncConflictIsKept syncs drafts an author edited where
// the answers change them: the conflict is judged only on the answered drafts, so
// the questions are put first, and the sync is then refused. Every answer given is
// in the answers file after the refusal — the next run asks for none of them — the
// one left empty stays present and empty, an answer refused as a secret's never
// reaches the file, and no draft is written.
//
// 🔴 PLANTED DEFECT: hand answerOwed no keep (pass nil in answerDrafts): the
// answers file still holds every key empty after the refusal, and the answers
// typed are lost.
func TestAnAnswerGivenBeforeASyncConflictIsKept(t *testing.T) {
	t.Parallel()
	rw, source := owingWorkspace(t, false)
	outputDir := filepath.Join(rw.Root(), "drafts")
	owingImport(t, rw, source, outputDir, Options{})

	releasePath := filepath.Join(outputDir, "release.yaml")
	drafted, err := os.ReadFile(releasePath)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(drafted), `image: ""`, `image: "hand/edit:1"`, 1)
	if edited == string(drafted) {
		t.Fatalf("the drafted Release holds no empty image to edit:\n%s", drafted)
	}
	if err := os.WriteFile(releasePath, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	person := &scriptedAsk{replies: map[string][]string{
		"spec.components.api.values.model":               {""},
		"spec.manifest":                                  {"owing"},
		"spec.components.api.infra.image":                {"example/server:1.0.0"},
		"spec.components.api.features.cred.infra.origin": {"not/a name", "api-credentials"},
	}}
	_, err = owingImportErr(rw, source, outputDir, Options{Sync: true, Ask: person.ask})
	var conflict *SyncConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("the sync was refused with %v; want the conflict on the edited image", err)
	}
	if strings.Contains(err.Error(), "no files were written") {
		t.Errorf("the conflict says no file was written, while the answers file was: %v", err)
	}
	wantAsked := []string{
		"spec.components.api.values.model|",
		"spec.manifest|",
		"spec.components.api.infra.image|",
		"spec.components.api.features.cred.infra.origin|",
		"spec.components.api.features.cred.infra.origin|" + secretAnswerRefusal,
	}
	if !slices.Equal(person.asked, wantAsked) {
		t.Fatalf("the questions were\n  %v\nwant\n  %v", person.asked, wantAsked)
	}

	answers := filepath.Join(filepath.Dir(source), AnswersFileName)
	kept, _, err := readAnswers(answers)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string]string{
		"aggregate.yaml": {"spec.components.api.values.model": ""},
		"release.yaml": {
			"spec.manifest":                                  "owing",
			"spec.components.api.infra.image":                "example/server:1.0.0",
			"spec.components.api.features.cred.infra.origin": "api-credentials",
		},
	}
	for file, keys := range want {
		for key, answer := range keys {
			if got, ok := kept[file][key]; !ok || got != answer {
				t.Errorf("after the refusal the answers file holds %s %s as %q (present %v); want %q", file, key, got, ok,
					answer)
			}
		}
	}
	if written, _ := os.ReadFile(answers); strings.Contains(string(written), "not/a name") {
		t.Errorf("an answer refused as a secret's reached the answers file:\n%s", written)
	}
	if onDisk, _ := os.ReadFile(releasePath); string(onDisk) != edited {
		t.Errorf("the refused sync wrote the drafted Release:\n%s", onDisk)
	}

	// The next import, a person present, is asked only for the value left empty.
	again := &scriptedAsk{replies: map[string][]string{}}
	if _, err := owingImportErr(rw, source, filepath.Join(rw.Root(), "fresh"), Options{Ask: again.ask}); err != nil {
		t.Fatalf("the next import was refused: %v", err)
	}
	if wantAgain := []string{"spec.components.api.values.model|"}; !slices.Equal(again.asked, wantAgain) {
		t.Errorf("the next import asked %v; want only %v", again.asked, wantAgain)
	}
}

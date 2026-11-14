// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

import (
	"testing"
)

// TestGivenAnswersPutEachSentAnswerToTheValueItNames holds the seam a request's
// answers reach the import through: each owed value is answered by the sent
// answer naming its file and keys, and by nothing else; a refusal handed back is
// what the value's entry advises from then on, and a value nobody refused
// advises as the terminal does.
//
// 🔴 PLANTED DEFECT: match a sent answer on its file alone in GivenAnswers.Ask:
// the row sending the image's answer answers the model with it, and fails.
func TestGivenAnswersPutEachSentAnswerToTheValueItNames(t *testing.T) {
	t.Parallel()
	model := Owed{File: "aggregate.yaml", Keys: []string{"spec", "components", "api", "values", "model"}}
	image := Owed{File: "release.yaml", Keys: []string{"spec", "components", "api", "infra", "image"}}
	origin := Owed{File: "release.yaml", Keys: []string{"spec", "components", "api", "features", "cred", "infra",
		"origin"}, Sensitive: true}
	dotted := Owed{File: "release.yaml", Keys: []string{"spec", "a.b"}}

	for _, row := range []struct {
		name    string
		given   []Given
		owed    Owed
		refused string
		answer  string
		advice  string
	}{
		{name: "the answer naming the value", given: []Given{{File: "release.yaml", Keys: image.Keys,
			Answer: "example/server:1.0.0"}}, owed: image, answer: "example/server:1.0.0"},
		{name: "an answer for another key of the same file", given: []Given{{File: "release.yaml",
			Keys: image.Keys, Answer: "example/server:1.0.0"}}, owed: origin, advice: secretAdvice},
		{name: "an answer for the same keys in another file", given: []Given{{File: "release.yaml",
			Keys: model.Keys, Answer: "gpt-4.1"}}, owed: model},
		{name: "a key holding a full stop is one key", given: []Given{{File: "release.yaml",
			Keys: []string{"spec", "a", "b"}, Answer: "split"}, {File: "release.yaml", Keys: []string{"spec", "a.b"},
			Answer: "whole"}}, owed: dotted, answer: "whole"},
		{name: "the first of two answers naming one value", given: []Given{{File: "aggregate.yaml",
			Keys: model.Keys, Answer: "first"}, {File: "aggregate.yaml", Keys: model.Keys, Answer: "second"}},
			owed: model, answer: "first"},
		{name: "a secret's answer refused", given: []Given{{File: "release.yaml", Keys: origin.Keys,
			Answer: "sk-PLANTED"}}, owed: origin, refused: secretAnswerRefusal, advice: secretAnswerRefusal},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			g := NewGivenAnswers(row.given)
			ask := g.Ask()
			if ask == nil {
				t.Fatal("answers were sent and the seam is nil")
			}
			got, err := ask(row.owed, row.refused)
			if err != nil {
				t.Fatalf("the seam stopped the asking: %v", err)
			}
			want := row.answer
			if row.refused != "" {
				want = ""
			}
			if got != want {
				t.Errorf("the seam answered %q; want %q", got, want)
			}
			if advice := g.Advice(row.owed); advice != row.advice {
				t.Errorf("the entry advises %q; want %q", advice, row.advice)
			}
		})
	}

	t.Run("none sent", func(t *testing.T) {
		t.Parallel()
		if NewGivenAnswers(nil).Ask() != nil {
			t.Error("a request sending no answer was handed a seam")
		}
		var none *GivenAnswers
		if none.Ask() != nil || none.Advice(origin) != secretAdvice || none.Advice(image) != "" {
			t.Error("a nil set of answers does not advise as the owed value itself does")
		}
	})
}

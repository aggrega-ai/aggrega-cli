// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package composecore

import (
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// fullText is everything a refusal says: its own sentence and every cause beneath.
func fullText(err error) string {
	return err.Error() + "\n" + cerrors.CauseChain(err)
}

// TestFragmentRefusalsNameWhatTheAuthorWrote holds every refusal of a compose
// fragment value of the wrong shape to the author's words: the line, the field or
// entry, the shape written and the shape the position takes — never the program's
// own types, the decoder's text or a node-kind number. One fault per row.
func TestFragmentRefusalsNameWhatTheAuthorWrote(t *testing.T) {
	t.Parallel()
	const head = "services:\n  probe:\n    image: \"nginx:1.27\"\n"
	cases := []struct {
		name string
		body string
		want string
	}{
		{"an image written as a list", "services:\n  probe:\n    image: [nginx]\n",
			"line 3 (services.probe.image): a list is written where text is expected"},
		{"a depends_on item written as a list", head + "    depends_on:\n      - [db]\n",
			"depends_on: line 5: a list is written where text is expected"},
		{"a depends_on entry body written as a list", head + "    depends_on:\n      db: [healthy]\n",
			`depends_on: under key "db": line 5: a list is written where a map is expected`},
		{"a depends_on condition written as a list", head + "    depends_on:\n      db:\n        condition: [x]\n",
			`depends_on: under key "db": line 6: a list is written where text is expected`},
		{"depends_on written as a number", head + "    depends_on: 5\n",
			"depends_on: line 4: a number is written where a list or a map is expected"},
		{"a models item written as a list", head + "    models:\n      - [m]\n",
			"models: line 5: a list is written where text is expected"},
		{"a models entry body written as a list", head + "    models:\n      m: [x]\n",
			`models: under key "m": line 5: a list is written where a map is expected`},
		{"models written as a number", head + "    models: 5\n",
			"models: line 4: a number is written where a list or a map is expected"},
		{"a depends_on key written as a list", head + "    depends_on:\n      ? [db]\n      : {condition: service_started}\n",
			"depends_on: reading a key: line 5: a list is written where text is expected"},
		{"a models key written as a list", head + "    models:\n      ? [m]\n      : {}\n",
			"models: reading a key: line 5: a list is written where text is expected"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var err error
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("decoding panicked: %v", r)
					}
				}()
				_, err = decodeFragment([]byte(tc.body), "web", `fragment "web.compose-fragment.yaml"`)
			}()
			if err == nil {
				t.Fatalf("a fragment of the wrong shape was accepted:\n%s", tc.body)
			}
			text := fullText(err)
			if !strings.Contains(text, tc.want) {
				t.Errorf("refusal does not say %q:\n%s", tc.want, text)
			}
			for _, leak := range []string{"cannot unmarshal", "composecore.", "got kind", "!!"} {
				if strings.Contains(text, leak) {
					t.Errorf("refusal carries %q, which names nothing the author wrote:\n%s", leak, text)
				}
			}
		})
	}
}

// TestSourcedComposeKeyWrittenAsAListIsRefusedInTheAuthorsWords holds the refusal
// of a sourced compose file whose key is written as a list — which the decoder
// spells as a Go value, with no line — to the key's line, the field holding it and
// its shape.
func TestSourcedComposeKeyWrittenAsAListIsRefusedInTheAuthorsWords(t *testing.T) {
	t.Parallel()
	source := "services:\n  probe:\n    image: \"nginx:1.27\"\n    depends_on:\n      ? [db]\n      : {condition: service_started}\n"
	_, err := RenderSourcedCore([]byte(source), "probe-fragment.yaml", nil, nodeCtxFor("web"))
	if err == nil {
		t.Fatal("a key written as a list was accepted")
	}
	text := fullText(err)
	const want = "line 5 (services.probe.depends_on): reading a key: a list is written where text is expected"
	if !strings.Contains(text, want) {
		t.Errorf("refusal does not say %q:\n%s", want, text)
	}
	for _, leak := range []string{"interface {}", "invalid map key"} {
		if strings.Contains(text, leak) {
			t.Errorf("refusal carries %q, which names nothing the author wrote:\n%s", leak, text)
		}
	}
}

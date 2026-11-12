// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package selector

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestParseRefusesARequirementTheGrammarDoesNotDefine pins every refusal the
// parser makes, one fault per row. Each row asserts the refusal's identity (a
// *SyntaxError), the requirement it names — by position always, and by text
// unless the requirement is empty — and a fragment of the reason, so a row
// that started failing on a different fault would say so rather than pass.
func TestParseRefusesARequirementTheGrammarDoesNotDefine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		input           string
		wantPosition    int
		wantRequirement string
		wantReason      string
	}{
		// A "!" requirement names a key and nothing else.
		{"absence test whose key carries an equality", "!tier=frontend", 1, "!tier=frontend", `the label key "tier=frontend" carries "="`},
		{"absence test whose key carries an inequality", "!tier!=frontend", 1, "!tier!=frontend", `carries "!"`},
		{"absence test whose key carries a set", "!tier in (a)", 1, "!tier in (a)", "carries a space"},
		{"absence test naming no key", "!", 1, "!", `"!" is followed by no label key`},

		// Empty requirements.
		{"separators alone", " , , ", 1, "", "leading, trailing or doubled comma"},
		{"leading comma", ",tier=frontend", 1, "", "leading, trailing or doubled comma"},
		{"trailing comma", "tier=frontend,", 2, "", "leading, trailing or doubled comma"},
		{"doubled comma", "tier=frontend,,env=prod", 2, "", "leading, trailing or doubled comma"},

		// Keys carrying whitespace or one of the grammar's characters.
		{"set written without parentheses", "tier in frontend", 1, "tier in frontend", "carries a space"},
		{"colon-and-space spelling", "tier: frontend", 1, "tier: frontend", "carries a space"},
		{"brace and line break", "{\n}", 1, "{\n}", "carries a line break"},
		{"tab inside a key", "ti\ter=x", 1, "ti\ter=x", "carries a tab"},
		{"space inside an equality key", "my tier=frontend", 1, "my tier=frontend", `the label key "my tier" carries a space`},
		{"space inside an inequality key", "my tier!=frontend", 1, "my tier!=frontend", `the label key "my tier" carries a space`},
		{"space inside a set key", "my tier in (a)", 1, "my tier in (a)", `the label key "my tier" carries a space`},
		{"equality inside an inequality key", "a=b!=c", 1, "a=b!=c", `the label key "a=b" carries "="`},
		{"parenthesis inside a bare key", "tier(x)", 1, "tier(x)", `carries "("`},
		{"bang inside a bare key", "ti!er", 1, "ti!er", `carries "!"`},
		{"no key before an equality", "=frontend", 1, "=frontend", "names no label key before its operator"},

		// A value set that is not closed.
		{"set without its closing parenthesis", "env in (prod", 1, "env in (prod", "expected (values) after in"},

		// The refusal names the requirement it could not classify, wherever it sits.
		{"refusal in the second requirement", "env=prod, !tier=frontend", 2, "!tier=frontend", `carries "="`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Parse(%q) panicked: %v", tt.input, r)
				}
			}()
			reqs, err := Parse(tt.input)
			if err == nil {
				t.Fatalf("Parse(%q) = %+v, want a refusal", tt.input, reqs)
			}
			if reqs != nil {
				t.Errorf("Parse(%q) returned requirements %+v alongside its refusal", tt.input, reqs)
			}
			var syntax *SyntaxError
			if !errors.As(err, &syntax) {
				t.Fatalf("Parse(%q) error %v (%T) is not a *SyntaxError", tt.input, err, err)
			}
			if syntax.Position != tt.wantPosition {
				t.Errorf("Parse(%q) position = %d, want %d", tt.input, syntax.Position, tt.wantPosition)
			}
			if syntax.Requirement != tt.wantRequirement {
				t.Errorf("Parse(%q) requirement = %q, want %q", tt.input, syntax.Requirement, tt.wantRequirement)
			}
			if !strings.Contains(syntax.Reason, tt.wantReason) {
				t.Errorf("Parse(%q) reason = %q, want it to say %q", tt.input, syntax.Reason, tt.wantReason)
			}
			// The rendered message names the requirement, so a reader of any
			// surface that prints it is told which requirement to correct.
			wantNamed := fmt.Sprintf("requirement %d", tt.wantPosition)
			if !strings.Contains(err.Error(), wantNamed) {
				t.Errorf("Parse(%q) message %q does not name %q", tt.input, err.Error(), wantNamed)
			}
			if tt.wantRequirement != "" && !strings.Contains(err.Error(), fmt.Sprintf("%q", tt.wantRequirement)) {
				t.Errorf("Parse(%q) message %q does not quote the requirement %q", tt.input, err.Error(), tt.wantRequirement)
			}
		})
	}
}

// TestParseRefusalSuggestsTheFormTheAuthorMeant pins the two hints: the
// inequality an absence test was written in place of, and the parentheses a
// set was written without.
func TestParseRefusalSuggestsTheFormTheAuthorMeant(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		{"!tier=frontend", `to exclude a value, write "tier!=frontend"`},
		{"tier in frontend", `a set is written in parentheses, as in "tier in (frontend)"`},
		{"tier notin frontend, backend", `as in "tier notin (frontend)"`},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			_, err := Parse(tt.input)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Parse(%q) error = %v, want it to suggest %q", tt.input, err, tt.want)
			}
		})
	}
}

// TestParseReadsEveryFormAndLeavesValuesOpaque is the positive control for the
// refusal table: every form parses to the requirement it names, and a value is
// read as the text after its operator whatever characters it carries. The
// double-equals row is the value side of the grammar stated as a fact: the
// value is "=frontend", not an alternative spelling of equality.
func TestParseReadsEveryFormAndLeavesValuesOpaque(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  []Requirement
	}{
		{"empty", "", nil},
		{"whitespace only", " \t\n ", nil},
		{"equality", "tier=frontend", []Requirement{{Key: "tier", Operator: OpEquals, Values: []string{"frontend"}}}},
		{"inequality", "tier!=frontend", []Requirement{{Key: "tier", Operator: OpNotEquals, Values: []string{"frontend"}}}},
		{"set", "tier in (a, b)", []Requirement{{Key: "tier", Operator: OpIn, Values: []string{"a", "b"}}}},
		{"excluded set", "tier notin (a)", []Requirement{{Key: "tier", Operator: OpNotIn, Values: []string{"a"}}}},
		{"presence", "component.aggrega.ai/category", []Requirement{{Key: "component.aggrega.ai/category", Operator: OpExists}}},
		{"absence", "!legacy", []Requirement{{Key: "legacy", Operator: OpDoesNotExist}}},
		{"absence with space after the bang", "! legacy", []Requirement{{Key: "legacy", Operator: OpDoesNotExist}}},
		{"surrounding whitespace", "  key = value  ", []Requirement{{Key: "key", Operator: OpEquals, Values: []string{"value"}}}},
		{"key with the usual punctuation", "k8s.io/app_name-x=v", []Requirement{{Key: "k8s.io/app_name-x", Operator: OpEquals, Values: []string{"v"}}}},
		{"colon is not a grammar character", "tier:x", []Requirement{{Key: "tier:x", Operator: OpExists}}},
		{"value after a double equals", "tier==frontend", []Requirement{{Key: "tier", Operator: OpEquals, Values: []string{"=frontend"}}}},
		{"empty value", "tier=", []Requirement{{Key: "tier", Operator: OpEquals, Values: []string{""}}}},
		{"value carrying a space", "tier=front end", []Requirement{{Key: "tier", Operator: OpEquals, Values: []string{"front end"}}}},
		{"value carrying a bang", "tier=a!b", []Requirement{{Key: "tier", Operator: OpEquals, Values: []string{"a!b"}}}},
		{"inequality value carrying an equality", "tier!=x=y", []Requirement{{Key: "tier", Operator: OpNotEquals, Values: []string{"x=y"}}}},
		{"every form in one selector", "a=1, b!=2, c in (x), d notin (y), e, !f", []Requirement{
			{Key: "a", Operator: OpEquals, Values: []string{"1"}},
			{Key: "b", Operator: OpNotEquals, Values: []string{"2"}},
			{Key: "c", Operator: OpIn, Values: []string{"x"}},
			{Key: "d", Operator: OpNotIn, Values: []string{"y"}},
			{Key: "e", Operator: OpExists},
			{Key: "f", Operator: OpDoesNotExist},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := Parse(tt.input)
			if err != nil {
				t.Fatalf("Parse(%q) refused: %v", tt.input, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Parse(%q) = %+v, want %+v", tt.input, got, tt.want)
			}
		})
	}
}

// TestMatchStringRefusesTheSelectorsThatAdmittedWhatTheyWereWrittenToExclude
// drives the two shapes that used to match everything through the matcher:
// against a target labelled tier=frontend, a selector written to exclude
// frontends, and one made of separators alone, are refused rather than
// matched. The controls beside them show the same target evaluated normally.
func TestMatchStringRefusesTheSelectorsThatAdmittedWhatTheyWereWrittenToExclude(t *testing.T) {
	t.Parallel()
	target := makeLabels("tier", "frontend", "env", "prod")
	tests := []struct {
		selector string
		want     bool
		refused  bool
	}{
		{"!tier=frontend", false, true},
		{" , , ", false, true},
		{"tier!=frontend", false, false},
		{"tier=frontend", true, false},
		{"!legacy", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.selector, func(t *testing.T) {
			t.Parallel()
			got, err := MatchString(tt.selector, target)
			var syntax *SyntaxError
			if tt.refused != errors.As(err, &syntax) {
				t.Fatalf("MatchString(%q) error = %v, want refused=%v", tt.selector, err, tt.refused)
			}
			if !tt.refused && err != nil {
				t.Fatalf("MatchString(%q) error = %v", tt.selector, err)
			}
			if got != tt.want {
				t.Errorf("MatchString(%q) = %v, want %v", tt.selector, got, tt.want)
			}
		})
	}
}

// TestMatchAnyOfRefusesAMalformedStringItem pins that a string item the
// grammar does not define is refused by the disjunction as well, as a
// *SyntaxError, rather than read as an item that holds.
func TestMatchAnyOfRefusesAMalformedStringItem(t *testing.T) {
	t.Parallel()
	target := makeLabels("tier", "frontend")
	got, err := MatchAnyOf([]types.SelectorTerm{types.StringTerm("!tier=frontend")}, target)
	var syntax *SyntaxError
	if !errors.As(err, &syntax) {
		t.Fatalf("MatchAnyOf error = %v, want a *SyntaxError", err)
	}
	if got {
		t.Error("a refused item satisfied the disjunction")
	}
}

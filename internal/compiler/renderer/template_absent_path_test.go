// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// A value template that prints a path where it always runs and iterates the same
// path halts when the data holds nothing there: no single value both prints as
// nothing and iterates as nothing, and leaving the path absent would print the
// engine's placeholder text into the artifact. The halt carries its own condition
// rather than the general run failure, names the path, and is reported under the
// template's own name. A present path read both ways, and a guarded print over an
// absent one, render as they always did.
func TestEvalTemplate_AbsentPathPrintedAndIteratedHalts(t *testing.T) {
	t.Parallel()
	values := types.OrderedMap[any]{}
	values.Set("list", []any{"a", "b"})
	ctx := &types.TemplateContext{Context: types.ContextBlock{InstanceID: "db"}, Values: values}

	halting := []struct {
		tmpl string
		path string
	}{
		{`{{ range .values.absent_list }}x{{ end }}|{{ .values.absent_list }}`, ".values.absent_list"},
		{`{{ .values.absent_list }}|{{ range $.values.absent_list }}x{{ end }}`, ".values.absent_list"},
		{`{{ ($.values).absent_list }}|{{ range .values.absent_list }}x{{ end }}`, "$.values.absent_list"},
		{`{{ .workload }}{{ range .workload }}x{{ end }}`, ".workload"},
	}
	for _, tc := range halting {
		t.Run(tc.tmpl, func(t *testing.T) {
			t.Parallel()
			_, err := EvalTemplate("probe-template", tc.tmpl, ctx, redact.Keys{})
			if !errors.Is(err, gotmpl.ErrAbsentPathReadAndIterated()) {
				t.Fatalf("got %v, want the absent-path-read-and-iterated halt", err)
			}
			if errors.Is(err, ErrValueTemplateExecutionFailed()) {
				t.Errorf("the halt must carry its own condition, not the general run failure: %v", err)
			}
			if code, _ := cerrors.CodeOf(err); code != "absent-path-read-and-iterated" {
				t.Errorf("the head carries %q, want absent-path-read-and-iterated: %v", code, err)
			}
			msg := cerrors.CauseChain(err)
			for _, want := range []string{"(path " + tc.path + ")", `"probe-template"`} {
				if !strings.Contains(msg, want) {
					t.Errorf("the report does not carry %s: %s", want, msg)
				}
			}
		})
	}

	// The refusal's text is the path as the template spells it and nothing the data
	// holds, so a template that also reads a value declared sensitive is still told
	// which path it was refused for, rather than a sentence saying the text is withheld.
	t.Run("a template reading a value declared sensitive still names the path", func(t *testing.T) {
		t.Parallel()
		secret := types.OrderedMap[any]{}
		secret.Set("password", "s3cret")
		sctx := &types.TemplateContext{Context: types.ContextBlock{InstanceID: "db"}, Values: secret}
		_, err := EvalTemplate("probe-template",
			`{{ .values.password }}{{ .values.absent_list }}{{ range .values.absent_list }}x{{ end }}`,
			sctx, redact.Keys{Values: map[string]bool{"password": true}})
		if !errors.Is(err, gotmpl.ErrAbsentPathReadAndIterated()) {
			t.Fatalf("got %v, want the absent-path-read-and-iterated halt", err)
		}
		msg := cerrors.CauseChain(err)
		if !strings.Contains(msg, "(path .values.absent_list)") {
			t.Errorf("the report does not name the path: %s", msg)
		}
		if strings.Contains(msg, "s3cret") {
			t.Errorf("the report carries the value declared sensitive: %s", msg)
		}
	})

	rendering := []struct {
		name string
		tmpl string
		want string
	}{
		{"present, read both ways", `{{ .values.list }}|{{ range .values.list }}{{ . }}{{ end }}`, "[a b]|ab"},
		{"absent, the print guarded", `{{ if .values.absent_list }}{{ .values.absent_list }}{{ end }}|{{ range .values.absent_list }}x{{ end }}`, "|"},
		{"absent, iterated only", `{{ range .values.absent_list }}x{{ else }}none{{ end }}`, "none"},
	}
	for _, tc := range rendering {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := EvalTemplate("probe-template", tc.tmpl, ctx, redact.Keys{})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("rendered %q, want %q", got, tc.want)
			}
		})
	}
}

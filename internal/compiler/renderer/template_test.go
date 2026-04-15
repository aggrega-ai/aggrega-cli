// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import "testing"

func TestEvalTemplate_Simple(t *testing.T) {
	t.Parallel()
	out, err := EvalTemplate("test", `hello {{ .Name }}`, map[string]string{"Name": "world"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "hello world" {
		t.Errorf("got %q", out)
	}
}

func TestEvalTemplate_Sprig(t *testing.T) {
	t.Parallel()
	out, err := EvalTemplate("test", `{{ "hello" | upper }}`, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "HELLO" {
		t.Errorf("got %q", out)
	}
}

func TestEvalTemplate_MissingKeyZero(t *testing.T) {
	t.Parallel()
	// After JSON round-trip, map[string]string becomes map[string]interface{}.
	// Missing keys return nil (zero of interface{}), which renders as "<no value>".
	// Templates should use Sprig default for optional values.
	out, err := EvalTemplate("test", `value={{ default "" .Missing }}`, map[string]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "value=" {
		t.Errorf("got %q, expected 'value='", out)
	}
}

func TestEvalTemplate_ParseError(t *testing.T) {
	t.Parallel()
	_, err := EvalTemplate("bad", `{{ .Invalid }{`, nil)
	if err == nil {
		t.Fatal("expected parse error")
	}
}

func TestEvalTemplate_JSON(t *testing.T) {
	t.Parallel()
	data := map[string]interface{}{
		"Port": 8080,
		"Host": "localhost",
	}
	out, err := EvalTemplate("json", `{"port": {{ .Port }}, "host": "{{ .Host }}"}`, data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != `{"port": 8080, "host": "localhost"}` {
		t.Errorf("got %q", out)
	}
}

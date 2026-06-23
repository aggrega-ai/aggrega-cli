// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package jsonpatch

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// suiteCase is one entry of the vendored json-patch/json-patch-tests
// suite. A case carries a document, a patch, and exactly one of an
// expected result or an error expectation (or neither, meaning "applies
// without error, result unchecked"); disabled cases are skipped.
type suiteCase struct {
	Comment  string          `json:"comment"`
	Doc      json.RawMessage `json:"doc"`
	Patch    json.RawMessage `json:"patch"`
	Expected json.RawMessage `json:"expected"`
	Error    *string         `json:"error"`
	Disabled bool            `json:"disabled"`
}

// TestUpstreamConformance runs the vendored community suite. The engine
// patches object-rooted manifests, so cases whose document or expected
// result is not a JSON object are out of domain and skipped with a
// logged count — the engine is deliberately narrower than general RFC
// 6902, and the skip is explicit rather than silent.
func TestUpstreamConformance(t *testing.T) {
	files := []string{
		filepath.Join("testdata", "json-patch-tests", "tests.json"),
		filepath.Join("testdata", "json-patch-tests", "spec_tests.json"),
	}

	var (
		successMatch   int
		successNoCheck int
		errorMatch     int
		outOfDomain    int
		disabled       int
	)

	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read suite %s: %v", file, err)
		}
		var cases []suiteCase
		if err := json.Unmarshal(raw, &cases); err != nil {
			t.Fatalf("parse suite %s: %v", file, err)
		}

		for i, c := range cases {
			name := c.Comment
			if name == "" {
				name = "case"
			}
			t.Run(label(file, i, name), func(t *testing.T) {
				if c.Disabled {
					disabled++
					t.Skip("disabled upstream")
				}
				hasError := c.Error != nil
				hasExpected := isPresent(c.Expected)
				if !isJSONObject(c.Doc) || (hasExpected && !isJSONObject(c.Expected)) {
					outOfDomain++
					t.Skip("out of domain: non-object document or result root")
				}

				doc := types.NewOrderedMap[any]()
				if err := json.Unmarshal(c.Doc, doc); err != nil {
					t.Fatalf("decode doc: %v", err)
				}
				ops := decodeOps(t, c.Patch)

				applyErr := Apply(doc, ops)
				switch {
				case hasError:
					if applyErr == nil {
						t.Fatalf("expected error (%v), got success; doc=%s", deref(c.Error), mustMarshal(t, doc))
					}
					errorMatch++
				case hasExpected:
					if applyErr != nil {
						t.Fatalf("unexpected error: %v", applyErr)
					}
					if !deepEqual(doc, canonicalValue(t, c.Expected)) {
						t.Fatalf("result mismatch\n got: %s\nwant: %s", mustMarshal(t, doc), string(c.Expected))
					}
					successMatch++
				default:
					if applyErr != nil {
						t.Fatalf("unexpected error: %v", applyErr)
					}
					successNoCheck++
				}
			})
		}
	}

	t.Logf("upstream conformance: %d success-match, %d success-unchecked, %d error-match, %d out-of-domain-skip, %d disabled-skip",
		successMatch, successNoCheck, errorMatch, outOfDomain, disabled)
}

// decodeOps maps a suite patch array onto []Op, decoding each op's value
// through the canonical document model so numbers are json.Number and
// objects/arrays carry order — the form deepEqual and the engine expect.
func decodeOps(t *testing.T, raw json.RawMessage) []Op {
	t.Helper()
	var rawOps []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &rawOps); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	ops := make([]Op, 0, len(rawOps))
	for _, ro := range rawOps {
		var op Op
		if v, ok := ro["op"]; ok {
			_ = json.Unmarshal(v, &op.Op)
		}
		if v, ok := ro["path"]; ok {
			_ = json.Unmarshal(v, &op.Path)
		}
		if v, ok := ro["from"]; ok {
			_ = json.Unmarshal(v, &op.From)
		}
		if v, ok := ro["value"]; ok {
			op.Value = canonicalValue(t, v)
		}
		ops = append(ops, op)
	}
	return ops
}

// canonicalValue decodes an arbitrary JSON fragment into the canonical
// document model by wrapping it in a one-key object and reading it back.
func canonicalValue(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	wrap := types.NewOrderedMap[any]()
	if err := json.Unmarshal([]byte(`{"v":`+string(raw)+`}`), wrap); err != nil {
		t.Fatalf("decode value %s: %v", string(raw), err)
	}
	v, _ := wrap.Get("v")
	return v
}

func isJSONObject(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) > 0 && t[0] == '{'
}

func isPresent(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) > 0 && string(t) != "null"
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func label(file string, i int, comment string) string {
	base := filepath.Base(file)
	r := make([]rune, 0, len(comment))
	for _, c := range comment {
		if c == ' ' || c == '/' {
			r = append(r, '_')
			continue
		}
		r = append(r, c)
	}
	trimmed := string(r)
	if len(trimmed) > 40 {
		trimmed = trimmed[:40]
	}
	return base + "_" + itoa(i) + "_" + trimmed
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package wasm_test

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
	schemas "github.com/aggrega-ai/aggrega-schemas"
)

// Each host-function reply envelope carries the machine-matchable failure
// identity under hostfunc_error_code, and its value set is the shared host-function
// error-code vocabulary declared once and generated into the per-language
// constants the host sets. These tests hold the three surfaces that name that
// set — the declared vocabulary, the field's own documentation, and the codes
// the host can actually emit — to one membership, in both directions each. A
// one-way check passes while the sets differ, which is how a value set and the
// codes in play drift apart unnoticed.

// hostfuncErrorCodes reads the declared vocabulary out of the shipped schema.
// The tests read the set rather than restating it: a second list here would
// agree with the schema on the day it was written and drift afterwards, which is
// the failure mode a single shared vocabulary exists to prevent.
func hostfuncErrorCodes(t *testing.T) []string {
	t.Helper()
	var doc struct {
		Properties struct {
			Code struct {
				Enum        []string `json:"enum"`
				Description string   `json:"description"`
			} `json:"code"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(schemas.WasmHostfuncErrorV1beta1), &doc); err != nil {
		t.Fatalf("unmarshal host-function error schema: %v", err)
	}
	if len(doc.Properties.Code.Enum) == 0 {
		t.Fatalf("the host-function error-code vocabulary declares no value set")
	}
	return doc.Properties.Code.Enum
}

// hostfuncErrorCodeDescription reads the vocabulary field's documentation.
func hostfuncErrorCodeDescription(t *testing.T) string {
	t.Helper()
	var doc struct {
		Properties struct {
			Code struct {
				Description string `json:"description"`
			} `json:"code"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(schemas.WasmHostfuncErrorV1beta1), &doc); err != nil {
		t.Fatalf("unmarshal host-function error schema: %v", err)
	}
	if doc.Properties.Code.Description == "" {
		t.Fatalf("the error-code field carries no description")
	}
	return doc.Properties.Code.Description
}

// TestHostfuncErrorCode_DocumentedSetMatchesDeclaredSet holds the field's
// documentation and its value set to the same membership. The documentation
// explains what each code means, which a value set cannot express, so both
// surfaces name every code — and a restatement that agrees on the day it is
// written is exactly what drifts afterwards. Both directions are asserted: a
// code declared but never explained, and a code explained but no longer
// declared, each fail here rather than reaching a plugin author as a value set
// and a prose list that disagree.
func TestHostfuncErrorCode_DocumentedSetMatchesDeclaredSet(t *testing.T) {
	description := hostfuncErrorCodeDescription(t)

	declared := map[string]bool{}
	for _, code := range hostfuncErrorCodes(t) {
		declared[code] = true
		if !strings.Contains(description, code+":") {
			t.Errorf("the declared code %q is not explained in the field's description", code)
		}
	}
	// Every "<token>: " in the description introduces a code's explanation, so an
	// explained token that is not declared is a stale entry.
	explained := regexp.MustCompile(`(^|[.\s])([a-z][a-z0-9_]*): `)
	for _, m := range explained.FindAllStringSubmatch(description, -1) {
		if !declared[m[2]] {
			t.Errorf("the field's description explains %q, which the value set does not declare", m[2])
		}
	}
}

// TestHostfuncErrorCode_EmittedSetMatchesDeclaredSet holds the codes the host
// can set on a reply envelope to the declared vocabulary, both ways. A code the
// host emits that is not declared is a value a guest is never told to expect; a
// code declared that no host emits is a value a guest is told to branch on and
// never receives. The emitted side is read through the generated constants,
// which are the values the emission sites pass.
func TestHostfuncErrorCode_EmittedSetMatchesDeclaredSet(t *testing.T) {
	declared := map[string]bool{}
	for _, code := range hostfuncErrorCodes(t) {
		declared[code] = true
	}
	emitted := []string{
		abi.CodeFetchRequestUnreadable,
		abi.CodeFetchInputUnavailable,
		abi.CodeFetchFailed,
		abi.CodeRequestUnreadable,
		abi.CodeRequestUndecodable,
		abi.CodeSourceRootUnavailable,
		abi.CodeSourceReadFailed,
		abi.CodeUnknownSelectionOp,
		abi.CodeSourceEscape,
		abi.CodeProducerConsumptionReentry,
		abi.CodeNoSourceParser,
		abi.CodeNoParserForFormat,
		abi.CodeParseFailed,
	}
	seen := map[string]bool{}
	for _, code := range emitted {
		if !declared[code] {
			t.Errorf("the host can emit %q, which the vocabulary does not declare", code)
		}
		seen[code] = true
	}
	var unemitted []string
	for code := range declared {
		if !seen[code] {
			unemitted = append(unemitted, code)
		}
	}
	sort.Strings(unemitted)
	if len(unemitted) != 0 {
		t.Errorf("the vocabulary declares %v, which no host emission site produces", unemitted)
	}
}

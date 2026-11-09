// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

// What a diagnostic carries for the READER, over the real wire and per layer.
//
// The entry has three identifier keys and they are not all present together. What
// decides their presence is whether a registered halting condition underlies the
// entry, so these cases drive the four states a client can actually receive rather
// than the three a reading of the struct would suggest: a registered condition, a
// code that names a class instead, a registered condition reported from the layer
// whose ordinary content is classes, and no code at all.
//
// Every assertion reads the RAW JSON. A typed decode cannot tell an omitted key
// from a key carrying the empty string, and the difference between those two is
// the whole contract a client branches on.

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/haltref"
	"github.com/aggrega-ai/aggrega-cli/internal/lintops"
	"github.com/aggrega-ai/aggrega-cli/pkg/externalinputs"
)

// shortCodeOnWire reports the value of the entry's quotable-code key and whether
// the key is present at all, keeping the two questions apart.
func shortCodeOnWire(t *testing.T, entry map[string]json.RawMessage) (string, bool) {
	t.Helper()
	raw, present := entry["shortCode"]
	if !present {
		return "", false
	}
	var short string
	if err := json.Unmarshal(raw, &short); err != nil {
		t.Fatalf("shortCode is not a JSON string: %s", raw)
	}
	return short, true
}

// pointerOnWire reports the address the entry's code-description key resolves to
// and whether the key is present at all.
//
// It decodes the nested structure rather than the whole value so that a key
// present but carrying no address fails here, which is the shape that would render
// a client's "learn more" affordance as a link to nowhere.
func pointerOnWire(t *testing.T, entry map[string]json.RawMessage) (string, bool) {
	t.Helper()
	raw, present := entry["codeDescription"]
	if !present {
		return "", false
	}
	var described struct {
		Href string `json:"href"`
	}
	if err := json.Unmarshal(raw, &described); err != nil {
		t.Fatalf("codeDescription is not the protocol's structure: %s", raw)
	}
	return described.Href, true
}

// rawEntry marshals one constructed diagnostic and decodes it back as the raw
// keys a client receives.
//
// It exists for the layers whose entries are built by a projection rather than
// returned from a request: the bytes it produces are the bytes the request path
// would emit for the same value, because the request path marshals this same
// struct, and going through a request would only add a workspace fixture between
// the projection and the assertion.
func rawEntry(t *testing.T, d Diagnostic) map[string]json.RawMessage {
	t.Helper()
	encoded, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal the diagnostic: %v", err)
	}
	var entry map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &entry); err != nil {
		t.Fatalf("decode the marshalled diagnostic: %v", err)
	}
	return entry
}

// TestDiagnosticCarriesTheQuotableCodeAndThePointer is the primary proof for the
// layer that reports registered conditions: an entry naming one carries all three
// identifier keys, and each carries the value the registry declares.
//
// The expectations are read off the registry through the same accessors any Go
// consumer of the same failure would use, so a renumbering moves the assertion
// with the condition instead of failing a file that has nothing to do with it.
func TestDiagnosticCarriesTheQuotableCodeAndThePointer(t *testing.T) {
	t.Parallel()
	const code = "parameter-contract-violation"
	entries := rawDiagnostics(t, "parameter-type-mismatch",
		[]string{"release.yaml", "manifest.yaml", "aggregate.yaml"})
	entry := findCodeOnWire(t, entries, code)

	short, present := shortCodeOnWire(t, entry)
	if !present {
		t.Fatalf("an entry naming %s carries no quotable code, so a reader has nothing to look up", code)
	}
	// The condition's own declaration is the authority for the value, and it is
	// reached the way a consumer reaches it rather than quoted.
	want, ok := conditionShortCodeFor(t, code)
	if !ok {
		t.Fatalf("%s is not registered, so this case no longer names a condition", code)
	}
	if short != want {
		t.Errorf("shortCode = %q; the registry declares %q for %s", short, want, code)
	}

	href, present := pointerOnWire(t, entry)
	if !present {
		t.Fatalf("an entry naming %s carries no reference, so no client can offer a way to read more", code)
	}
	if href != haltref.ExplanationURL() {
		t.Errorf("codeDescription.href = %q; want the published address (%q)", href, haltref.ExplanationURL())
	}

	// The message is where none of this travels. Without this the two keys above
	// could be satisfied while a client was still expected to read prose.
	message := messageOnWire(t, entry)
	for _, leaked := range []string{short, href} {
		if strings.Contains(message, leaked) {
			t.Errorf("the message text carries %q; each identifier travels as its own key and nowhere else:\n%s",
				leaked, message)
		}
	}
}

// conditionShortCodeFor resolves a descriptive code to the short code its
// condition declares, through the same read every surface performs.
func conditionShortCodeFor(t *testing.T, code string) (string, bool) {
	t.Helper()
	condition, err := haltref.Resolve(code)
	if err != nil {
		return "", false
	}
	return condition.ShortCode, true
}

// TestDiagnosticOmitsBothReaderKeysForAClassCode covers the codes that name a
// CLASS rather than a registered condition: a render advisory's class, and the
// identity of a publishing-conventions rule no condition is registered under —
// one from a linter's own rule set.
//
// Both keys are absent, and the absence is the statement. A short code minted for
// such a code would be an identifier no lookup resolves, and a reference would
// resolve to a page that does not describe what the entry reported — which is the
// dead pointer a client would render as a working link.
//
// The two layers are driven separately because they reach the wire through
// different projections: one maps the compiler's advisory channel, the other one
// conventions finding. One passing says nothing about the other. The conventions
// row goes through the projection itself, because whether a rule names a
// condition is decided there.
func TestDiagnosticOmitsBothReaderKeysForAClassCode(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		layer string
		entry Diagnostic
	}{
		{
			layer: "a render advisory's class",
			entry: Diagnostic{
				Line: 1, Column: 1,
				Severity:       editorSeverityWarning,
				Message:        "target does not fully support one field",
				ConditionCodes: classCode("unsupported-field"),
			},
		},
		{
			layer: "the identity of a conventions rule no condition names",
			entry: conventionDiagnostic(lintops.Finding{
				Severity: abi.SeverityWarn,
				Message:  "the entity's name does not follow this linter's own naming shape",
				RuleID:   "rule-of-another-linter",
			}),
		},
	} {
		tc := tc
		t.Run(tc.layer, func(t *testing.T) {
			t.Parallel()
			entry := rawEntry(t, tc.entry)

			// The positive control: the entry does carry a code, so the two
			// absences below are about those keys and not about an entry that
			// reports nothing at all.
			code, present := codeOnWire(t, entry)
			if !present || code == "" {
				t.Fatalf("the entry reports no machine-matchable code, so the assertions below "+
					"would be about an empty entry: %v", entry)
			}
			if short, present := shortCodeOnWire(t, entry); present {
				t.Errorf("a code naming a class carries the quotable-code key (%q); it must be omitted, "+
					"because no lookup resolves a short code for it", short)
			}
			if href, present := pointerOnWire(t, entry); present {
				t.Errorf("a code naming a class carries a reference (%q); it must be omitted, because "+
					"the page it resolves to does not document a class", href)
			}
		})
	}
}

// TestDiagnosticOmitsEveryIdentifierWhenNothingIsNamed is the case a client has to
// tell apart from the one above: not a code that resolves to nothing further, but
// no code at all.
//
// A buffer no YAML parser will read is the class that names nothing — the parse
// failure comes from the YAML library and no condition of this toolchain's own
// stands behind it — so all three keys are absent rather than present carrying
// empty values.
func TestDiagnosticOmitsEveryIdentifierWhenNothingIsNamed(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	srv := newTestServer(t, tmp, nil, nil)
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/validate", validateParams{
		File:    filepath.Join(tmp, "aggregate.yaml"),
		Content: "apiVersion: aggrega.ai/v1beta1\nkind: Aggregate\n  bad: [unclosed\n",
	}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("unexpected envelope error: %+v", resp.Error)
	}
	var envelope struct {
		Diagnostics []map[string]json.RawMessage `json:"diagnostics"`
	}
	if err := json.Unmarshal(resp.Result, &envelope); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(envelope.Diagnostics) != 1 {
		t.Fatalf("expected one parse diagnostic; got %d", len(envelope.Diagnostics))
	}
	entry := envelope.Diagnostics[0]

	// The exact key set, rather than three absence checks. An absence check per key
	// passes on an entry that grew a fourth identifier nobody decided about, and
	// this is the case where an invented key would be least visible.
	got := make([]string, 0, len(entry))
	for k := range entry {
		got = append(got, k)
	}
	for _, forbidden := range []string{"code", "shortCode", "codeDescription"} {
		if _, present := entry[forbidden]; present {
			t.Errorf("the %q key is present on a failure naming no condition; every identifier must be "+
				"omitted. The entry carried: %v", forbidden, got)
		}
	}
	// The entry is a real diagnostic, so the absences above are about the
	// identifiers and not about an empty response.
	if message := messageOnWire(t, entry); !strings.Contains(message, "yaml:") {
		t.Errorf("expected the parser's own text; got %q", message)
	}
}

// TestDiagnosticCarriesTheReaderKeysWhereverTheConditionIsRegistered is the case
// that separates the real rule from the one a reader is likeliest to infer.
//
// The keys are decided by whether a REGISTERED condition underlies the entry, not
// by which layer published it. The two are nearly the same thing — the build
// layer's ordinary content is advisory classes — and the one build-layer entry
// that names a registered condition is what makes them different. An
// implementation keyed on the layer would omit both reader keys here while every
// other case in this file still passed.
func TestDiagnosticCarriesTheReaderKeysWhereverTheConditionIsRegistered(t *testing.T) {
	t.Parallel()
	diags := externalInputDivergenceDiagnostics([]string{"https://inputs.example/x"}, "release.yaml")
	if len(diags) != 1 {
		t.Fatalf("expected one advisory; got %d", len(diags))
	}
	entry := rawEntry(t, diags[0])

	want, ok := conditionShortCodeFor(t, mustCodeOf(t, externalinputs.ErrAcquisitionDisabled()))
	if !ok {
		t.Fatal("the declined-reference condition is not registered, so this case no longer exercises " +
			"a registered condition on the build layer")
	}
	short, present := shortCodeOnWire(t, entry)
	if !present {
		t.Fatal("a build-layer entry naming a registered condition carries no quotable code; the keys " +
			"are decided by the condition behind the entry, never by the layer that published it")
	}
	if short != want {
		t.Errorf("shortCode = %q; the registry declares %q", short, want)
	}
	if href, present := pointerOnWire(t, entry); !present {
		t.Error("a build-layer entry naming a registered condition carries no reference")
	} else if href != haltref.ExplanationURL() {
		t.Errorf("codeDescription.href = %q; want %q", href, haltref.ExplanationURL())
	}
}

// mustCodeOf reads the descriptive code off a condition, failing where the chain
// carries none — which would make whatever the caller went on to assert vacuous.
func mustCodeOf(t *testing.T, err error) string {
	t.Helper()
	code, ok := conditionCodesOfCode(err)
	if !ok {
		t.Fatalf("the condition reports no descriptive code: %v", err)
	}
	return code
}

// conditionCodesOfCode reads the descriptive code through the same chokepoint the
// wire fills its keys from, so a test cannot assert against a value the wire does
// not use.
func conditionCodesOfCode(err error) (string, bool) {
	codes := conditionCodesOf(err)
	return codes.Code, codes.Code != ""
}

// TestTheReaderKeysAreFilledFromOnePlace pins the chokepoint itself, so that a
// site filling the identifier keys by hand fails here rather than by omission
// somewhere a case above does not reach.
//
// A construction site cannot name the promoted keys in a literal — the compiler
// refuses it — so the only way in is one of the two constructors. This asserts
// what each of them decides, which is the rule every site therefore inherits.
func TestTheReaderKeysAreFilledFromOnePlace(t *testing.T) {
	t.Parallel()

	t.Run("a registered condition yields all three", func(t *testing.T) {
		t.Parallel()
		codes := conditionCodesOf(externalinputs.ErrAcquisitionDisabled())
		switch {
		case codes.Code == "":
			t.Error("no descriptive code")
		case codes.ShortCode == "":
			t.Error("no quotable code")
		case codes.CodeDescription == nil:
			t.Error("no reference")
		case codes.CodeDescription.Href != haltref.ExplanationURL():
			t.Errorf("href = %q; want %q", codes.CodeDescription.Href, haltref.ExplanationURL())
		}
	})

	t.Run("a chain naming no condition yields none", func(t *testing.T) {
		t.Parallel()
		codes := conditionCodesOf(errPlainForCodes)
		if codes != (ConditionCodes{}) {
			t.Errorf("a chain naming no condition produced %+v; want the zero value, so every key is "+
				"omitted", codes)
		}
	})

	t.Run("a class code yields the machine key alone", func(t *testing.T) {
		t.Parallel()
		codes := classCode("unsupported-field")
		if codes.Code != "unsupported-field" {
			t.Errorf("Code = %q; want the class code verbatim", codes.Code)
		}
		if codes.ShortCode != "" || codes.CodeDescription != nil {
			t.Errorf("a class code produced reader keys (%+v); a class resolves nowhere, so both are "+
				"omitted", codes)
		}
	})

	// The pointer is present exactly when the quotable code is, and that
	// equivalence is what keeps a reference from ever resolving to nothing. Both
	// directions, because either alone is satisfied by a constructor that always
	// omits or always fills.
	t.Run("the reference and the quotable code stand or fall together", func(t *testing.T) {
		t.Parallel()
		for _, codes := range []ConditionCodes{
			conditionCodesOf(externalinputs.ErrAcquisitionDisabled()),
			conditionCodesOf(errPlainForCodes),
			classCode("unsupported-field"),
		} {
			if (codes.ShortCode != "") != (codes.CodeDescription != nil) {
				t.Errorf("%+v carries one of the two reader keys without the other", codes)
			}
		}
	})
}

// errPlainForCodes is an error naming no registered condition: the negative
// control for the reads above. It is declared here rather than taken from a
// production path so the case cannot start passing because some other package's
// condition changed.
var errPlainForCodes = errPlain{}

type errPlain struct{}

func (errPlain) Error() string { return "a failure no condition of this toolchain stands behind" }

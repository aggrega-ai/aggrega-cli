// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/stringformat"
)

// supportedFormatList is the listing every refusal of an unsupported name carries.
var supportedFormatList = strings.Join(stringformat.Names(), ", ")

// TestLoadAggregateParameterContract_RefusesAnUnsupportedFormat drives a format
// name outside the supported set through the contract's load, with no Release
// value anywhere, at a parameter's own declaration and at every place a
// sub-contract sits, and asserts the gate that refuses it by its identifier.
//
// A parameter's own declaration is the schema's to refuse, since the schema closes
// the name there, so that row asserts the envelope's condition and that the
// supported names are listed. A nested sub-contract is not described by the
// schema, so those rows assert the contract condition, the key the refusal points
// at, and the listing.
func TestLoadAggregateParameterContract_RefusesAnUnsupportedFormat(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name      string
		body      string
		want      error
		wantField string
		wantInMsg []string
	}{
		{
			name:      "misspelt name on a parameter's own declaration",
			body:      "    contact:\n      type: string\n      format: emial\n",
			want:      ErrYAMLLoad(),
			wantInMsg: []string{"contact", "format", "emial", strings.Join(stringformat.Names(), " ")},
		},
		{
			name:      "password on a parameter's own declaration",
			body:      "    secret:\n      type: string\n      format: password\n",
			want:      ErrYAMLLoad(),
			wantInMsg: []string{"secret", "format", "password"},
		},
		{
			name:      "misspelt name under items",
			body:      "    allowed_hosts:\n      type: array\n      items:\n        type: string\n        format: hostnme\n",
			want:      ErrParameterContractViolation(),
			wantField: ".spec.parameters.allowed_hosts.items.format",
			wantInMsg: []string{`parameter "allowed_hosts.items"`, `format "hostnme" is not supported`, supportedFormatList},
		},
		{
			name:      "password under properties",
			body:      "    db:\n      type: object\n      properties:\n        password:\n          type: string\n          format: password\n",
			want:      ErrParameterContractViolation(),
			wantField: ".spec.parameters.db.properties.password.format",
			wantInMsg: []string{`parameter "db.properties.password"`, `format "password" is not supported`, supportedFormatList},
		},
		{
			name:      "unsupported name under valuesType",
			body:      "    tags:\n      type: object\n      valuesType:\n        type: string\n        format: ipv5\n",
			want:      ErrParameterContractViolation(),
			wantField: ".spec.parameters.tags.valuesType.format",
			wantInMsg: []string{`parameter "tags.valuesType"`, `format "ipv5" is not supported`},
		},
		{
			name: "unsupported name under items inside properties",
			body: "    db:\n      type: object\n      properties:\n        hosts:\n          type: array\n" +
				"          items:\n            type: string\n            format: url\n",
			want:      ErrParameterContractViolation(),
			wantField: ".spec.parameters.db.properties.hosts.items.format",
			wantInMsg: []string{`parameter "db.properties.hosts.items"`, `format "url" is not supported`},
		},
	}

	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			t.Parallel()
			var contractErr error
			func() {
				defer func() {
					if p := recover(); p != nil {
						t.Fatalf("the contract load panicked on an unsupported format: %v", p)
					}
				}()
				_, contractErr = LoadAggregateParameterContract(writeContractAggregate(t, r.body))
			}()
			if contractErr == nil {
				t.Fatalf("expected the contract load to refuse; got nil")
			}
			if !errors.Is(contractErr, r.want) {
				t.Fatalf("want errors.Is(err, %v); got %v", r.want, contractErr)
			}
			if r.wantField != "" {
				var re *ResolverError
				if !errors.As(contractErr, &re) {
					t.Fatalf("expected one *ResolverError finding; got %T: %v", contractErr, contractErr)
				}
				if re.Field != r.wantField {
					t.Errorf("Field = %q; want %q", re.Field, r.wantField)
				}
			}
			msg := contractErr.Error()
			for _, s := range r.wantInMsg {
				if !strings.Contains(msg, s) {
					t.Errorf("error does not carry %q:\n  %s", s, msg)
				}
			}
		})
	}
}

// TestRefuseMalformedParameterContract_RefusesAnUnsupportedFormatAtEveryDepth
// drives the load-time pass directly with a contract that never went through the
// envelope, which is the one way an unsupported name reaches it on a parameter's
// own declaration, and pins that the pass refuses it there as it does when nested.
func TestRefuseMalformedParameterContract_RefusesAnUnsupportedFormatAtEveryDepth(t *testing.T) {
	t.Parallel()
	var contract types.OrderedMap[any]
	contract.Set("contact", map[string]any{"type": "string", "format": "emial"})
	err := refuseMalformedParameterContract(contract)
	if !errors.Is(err, ErrParameterContractViolation()) {
		t.Fatalf("want ErrParameterContractViolation; got %v", err)
	}
	var re *ResolverError
	if !errors.As(err, &re) || re.Field != ".spec.parameters.contact.format" {
		t.Fatalf("want one finding at .spec.parameters.contact.format; got %v", err)
	}
	if !strings.Contains(err.Error(), supportedFormatList) {
		t.Errorf("the refusal does not list the supported formats: %v", err)
	}
}

// TestLoadAggregateParameterContract_EverySupportedFormatLoads is the positive
// control for the refusals above: every supported name, on a parameter's own
// declaration and nested, loads.
func TestLoadAggregateParameterContract_EverySupportedFormatLoads(t *testing.T) {
	t.Parallel()
	for _, name := range stringformat.Names() {
		body := "    own:\n      type: string\n      format: " + name + "\n" +
			"    list:\n      type: array\n      items:\n        type: string\n        format: " + name + "\n" +
			"    obj:\n      type: object\n      properties:\n        field:\n          type: string\n          format: " + name + "\n" +
			"    map:\n      type: object\n      valuesType:\n        type: string\n        format: " + name + "\n"
		if _, err := LoadAggregateParameterContract(writeContractAggregate(t, body)); err != nil {
			t.Errorf("format %q: expected the contract to load; got %v", name, err)
		}
	}
}

// TestValidateParametersAgainstContract_ChecksFormat drives Release values against
// each supported format, conforming and not, on a parameter's own declaration and
// under every nesting, and pins that a refusal names the parameter, the value and
// the format.
func TestValidateParametersAgainstContract_ChecksFormat(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name     string
		format   string
		good     string
		bad      string
		wrap     func(string) any
		body     func(format string) string
		wantPath string
	}{
		{name: "email, own declaration", format: "email", good: "ops@example.com", bad: "not an email {\n}"},
		{name: "uri, own declaration", format: "uri", good: "https://example.com/x", bad: "::::"},
		{name: "hostname, own declaration", format: "hostname", good: "db.example.com", bad: "*"},
		{name: "ipv4, own declaration", format: "ipv4", good: "192.0.2.1", bad: "192.0.2.256"},
		{name: "ipv6, own declaration", format: "ipv6", good: "2001:db8::1", bad: "fe80::1%eth0"},
		{name: "uuid, own declaration", format: "uuid", good: "2eb8aa08-aa98-11ea-b4aa-73b441d16380", bad: "2eb8aa08"},
		{name: "date-time, own declaration", format: "date-time", good: "2026-09-28T12:00:00Z", bad: "yesterday"},
		{name: "date, own declaration", format: "date", good: "2026-02-28", bad: "2026-02-30"},
		{
			name: "hostname under items", format: "hostname", good: "api.example.com", bad: "*",
			wrap: func(v string) any { return []any{"ok.example.com", v} },
			body: func(f string) string {
				return "    p:\n      type: array\n      items:\n        type: string\n        format: " + f + "\n"
			},
			wantPath: "p.1",
		},
		{
			name: "uri under properties", format: "uri", good: "https://example.com", bad: "::::",
			wrap: func(v string) any { return map[string]any{"endpoint": v} },
			body: func(f string) string {
				return "    p:\n      type: object\n      properties:\n        endpoint:\n          type: string\n          format: " + f + "\n"
			},
			wantPath: "p.endpoint",
		},
		{
			name: "email under valuesType", format: "email", good: "a@example.com", bad: "nobody",
			wrap: func(v string) any { return map[string]any{"owner": v} },
			body: func(f string) string {
				return "    p:\n      type: object\n      valuesType:\n        type: string\n        format: " + f + "\n"
			},
			wantPath: "p.owner",
		},
	}

	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			t.Parallel()
			body := "    p:\n      type: string\n      format: " + r.format + "\n"
			if r.body != nil {
				body = r.body(r.format)
			}
			wrap := r.wrap
			if wrap == nil {
				wrap = func(v string) any { return v }
			}
			wantPath := r.wantPath
			if wantPath == "" {
				wantPath = "p"
			}
			contract, err := LoadAggregateParameterContract(writeContractAggregate(t, body))
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if errs := ValidateParametersAgainstContract(contract, map[string]any{"p": wrap(r.good)}, "test.yaml"); len(errs) != 0 {
				t.Errorf("a conforming %s value %q was refused: %v", r.format, r.good, errs)
			}
			errs := ValidateParametersAgainstContract(contract, map[string]any{"p": wrap(r.bad)}, "test.yaml")
			if len(errs) != 1 {
				t.Fatalf("a non-conforming %s value %q: want one violation; got %v", r.format, r.bad, errs)
			}
			if !errors.Is(errs[0], ErrParameterContractViolation()) {
				t.Errorf("want ErrParameterContractViolation; got %v", errs[0])
			}
			if errs[0].Field != ".spec.parameters."+wantPath {
				t.Errorf("Field = %q; want %q", errs[0].Field, ".spec.parameters."+wantPath)
			}
			for _, s := range []string{`parameter "` + wantPath + `"`, "is not a valid " + r.format + ":"} {
				if !strings.Contains(errs[0].Message, s) {
					t.Errorf("message does not carry %q: %s", s, errs[0].Message)
				}
			}
			if own := stringformat.Check(r.format, r.bad).Error(); !strings.Contains(errs[0].Message, own) {
				t.Errorf("message does not carry the checkers' own refusal %q: %s", own, errs[0].Message)
			}
		})
	}
}

// TestValidateParametersAgainstContract_FormatDoesNotJudgeAWrongType pins that a
// value of the wrong type is refused for its type alone: a format judges strings,
// so it adds no second violation to a value that was never one.
func TestValidateParametersAgainstContract_FormatDoesNotJudgeAWrongType(t *testing.T) {
	t.Parallel()
	contract, err := LoadAggregateParameterContract(writeContractAggregate(t,
		"    p:\n      type: string\n      format: email\n"))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	errs := ValidateParametersAgainstContract(contract, map[string]any{"p": 42}, "test.yaml")
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "expected type string") {
		t.Fatalf("want exactly the type violation; got %v", errs)
	}
}

// TestValidateParametersAgainstContract_RefusesAnUnsupportedFormatItWasHanded
// pins the value check's defensive arm: a contract that bypassed the load and
// carries an unsupported name is refused in the load's own words rather than
// accepting every value as though the name constrained it.
func TestValidateParametersAgainstContract_RefusesAnUnsupportedFormatItWasHanded(t *testing.T) {
	t.Parallel()
	var contract types.OrderedMap[any]
	contract.Set("p", map[string]any{"type": "string", "format": "password"})
	errs := ValidateParametersAgainstContract(contract, map[string]any{"p": "hunter2"}, "test.yaml")
	if len(errs) != 1 || !errors.Is(errs[0], ErrParameterContractViolation()) {
		t.Fatalf("want one contract violation; got %v", errs)
	}
	for _, s := range []string{`parameter "p"`, `format "password" is not supported`, supportedFormatList} {
		if !strings.Contains(errs[0].Message, s) {
			t.Errorf("message does not carry %q: %s", s, errs[0].Message)
		}
	}
}

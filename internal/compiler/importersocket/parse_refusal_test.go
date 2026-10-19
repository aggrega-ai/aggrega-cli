// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package importersocket

import (
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/plugins/parsercore"
)

// TestParseRefusalReadsTheParsersHaltingDiagnostics holds ParseRefusal to the
// one reading of whether a parse refused its source: a parse carrying no
// diagnostic, or only ones that do not halt, refused nothing; one carrying
// halting diagnostics refused it, with each one's message in the parser's order.
// The compiler's own JSON parser refuses a source cut short, and the refusal
// carries its reason.
func TestParseRefusalReadsTheParsersHaltingDiagnostics(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name string
		out  abi.ParseOutput
		want string
	}{
		{"no diagnostic", abi.ParseOutput{Tree: []byte(`{}`)}, ""},
		{"a warning alone", abi.ParseOutput{Tree: []byte(`{}`),
			Diagnostics: []abi.Diagnostic{{Severity: abi.SeverityWarn, Message: "odd"}}}, ""},
		{"two halts beside a warning", abi.ParseOutput{Tree: []byte(`null`), Diagnostics: []abi.Diagnostic{
			{Severity: abi.SeverityHalt, Message: "first"}, {Severity: abi.SeverityWarn, Message: "odd"},
			{Severity: abi.SeverityHalt, Message: "second"}}}, "first; second"},
		{"the JSON parser on a source cut short", parsercore.ParseJSON([]byte(`{"a": [`)), "json parse: unexpected EOF"},
	} {
		err := ParseRefusal(row.out)
		switch {
		case row.want == "" && err != nil:
			t.Errorf("%s: ParseRefusal = %v, want none", row.name, err)
		case row.want != "" && (err == nil || err.Error() != row.want):
			t.Errorf("%s: ParseRefusal = %v, want %q", row.name, err, row.want)
		}
	}
}

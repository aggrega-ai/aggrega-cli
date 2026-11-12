// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package workspaceconfig

import (
	"testing"

	schemas "github.com/aggrega-ai/aggrega-schemas"
)

// TestTheTransportEnumAgreesWithTheTransportConstants holds the configuration
// schema's list of transports in step with the two constants the command and
// Default read.
//
// The two are independent statements of one closed set: the schema's enum is
// what refuses a value when a configuration file loads, and the constants are
// what the --transport flag is checked against and what Default seeds. A
// transport added to one and not the other is either a value a file cannot
// write while the flag accepts it, or one a file may write that the command
// then refuses as unknown — and neither is visible from the side it was edited
// on.
//
// The order is compared too: the schema lists the default first, which is the
// order the reference pages present the two in.
//
// The comparison is written across the two representations rather than by
// generating one from the other, for the reason the deny-reach check beside it
// states: a schema emitted from the constants could not disagree with them, and
// disagreement is the entire signal.
func TestTheTransportEnumAgreesWithTheTransportConstants(t *testing.T) {
	t.Parallel()

	declared, err := configSchemaEnum(schemas.AggregaConfigV1beta1, "the transport setting",
		[]string{"properties", "mcp", "properties", "transport"})
	if err != nil {
		t.Fatalf("read the transport enum: %v", err)
	}
	want := []string{MCPTransportStdio, MCPTransportHTTP}
	if len(declared) != len(want) {
		t.Fatalf("the configuration schema admits %d transports, %v, and the code declares %d, %v",
			len(declared), declared, len(want), want)
	}
	for i := range want {
		if declared[i] != want[i] {
			t.Errorf("at position %d the configuration schema admits %q and the code declares %q; "+
				"a transport a file names and one the flag takes must be the same word", i, declared[i], want[i])
		}
	}

	// The default is the other half of the same fact. The defaults-agreement
	// check compares the schema's default with Default's value; this pins that
	// the value both agree on is the first of the two, so a default moved to the
	// other transport fails here by name rather than as a pair that still agree.
	if got := Default().MCP.Transport; got != MCPTransportStdio {
		t.Errorf("Default seeds the transport %q, want %q: a workspace that never names a transport "+
			"is served over standard input and output", got, MCPTransportStdio)
	}
}

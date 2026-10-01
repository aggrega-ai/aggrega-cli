// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/pluginops"
)

// TestThePluginAddRoleRefusalStatesTheGovernanceRolesHint drives the addition
// method for the governance type with no role and with a role that is not one,
// and pins that each is refused in this surface's own terms and states the roles
// hint — the one wording the command line and the daemon name the governance
// roles in, which the plugin operations hold to the role enumeration the
// configuration schema declares. A refusal listing the roles by hand stops
// containing the hint the moment a role is added, so this fails rather than
// letting the daemon offer fewer roles than the configuration accepts.
//
// The unknown role is the row that needs the surface's own check: left to the
// shared layer, it is refused in the command line's terms, naming a flag a
// caller of this method never sends.
func TestThePluginAddRoleRefusalStatesTheGovernanceRolesHint(t *testing.T) {
	t.Parallel()
	srv, _ := pluginWriteFixture(t)

	hint := pluginops.GovernanceRolesHint()
	if hint == "" {
		t.Fatal("the governance roles hint is empty, so a refusal containing it would prove nothing")
	}
	want := "plugin add: the governance type requires role, naming the attachment role (" + hint + ")"

	for _, tc := range []struct {
		name   string
		params map[string]any
	}{
		{"no role", map[string]any{"type": "governance", "query": "x"}},
		{"a role that is not one", map[string]any{"type": "governance", "query": "x", "role": "nonsense"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := callDaemonMethod(t, srv, "aggrega/pluginAdd", tc.params)
			if resp.Error == nil {
				t.Fatalf("a governance addition with %s was accepted", tc.name)
			}
			if resp.Error.Code != jsonRPCInvalidParams {
				t.Errorf("the refusal's code = %d; want %d (invalid params)", resp.Error.Code, jsonRPCInvalidParams)
			}
			if !strings.Contains(resp.Error.Message, want) {
				t.Errorf("the refusal is not this surface's, stating the governance roles\nwant it to contain: %s\ngot: %s",
					want, resp.Error.Message)
			}
			if strings.Contains(resp.Error.Message, "--role") {
				t.Errorf("the refusal names the command line's --role flag, which a caller of this method never sends: %s",
					resp.Error.Message)
			}
		})
	}
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/pluginops"
)

// TestPluginAddHelpStatesTheGovernanceRolesHint pins that each place the help of
// `plugin add` states the governance roles — the long description and the --role
// flag's usage — states the roles hint: the one wording the command line and the
// daemon name the governance roles in, which the plugin operations hold to the
// role enumeration the configuration schema declares. A help text listing the roles by hand stops
// containing the hint the moment a role is added, so this fails rather than
// letting the help offer fewer roles than the configuration accepts.
func TestPluginAddHelpStatesTheGovernanceRolesHint(t *testing.T) {
	hint := pluginops.GovernanceRolesHint()
	if hint == "" {
		t.Fatal("the governance roles hint is empty, so a help text containing it would prove nothing")
	}

	cmd := newPluginAddCmd()
	roleFlag := cmd.Flags().Lookup("role")
	if roleFlag == nil {
		t.Fatal("plugin add registers no --role flag, so it offers no governance role at all")
	}

	for _, row := range []struct {
		surface string
		text    string
	}{
		{surface: "the long description", text: cmd.Long},
		{surface: "the --role flag usage", text: roleFlag.Usage},
	} {
		t.Run(row.surface, func(t *testing.T) {
			if !strings.Contains(row.text, hint) {
				t.Errorf("%s does not state the governance roles %q:\n%s", row.surface, hint, row.text)
			}
		})
	}
}

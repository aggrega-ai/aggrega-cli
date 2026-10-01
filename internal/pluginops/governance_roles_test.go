// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginops

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	schemas "github.com/aggrega-ai/aggrega-schemas"

	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
)

// TestTheGovernanceRolesHintNamesEveryRoleTheSchemaDeclares holds the one wording
// the command line and the daemon state the governance roles in to the role
// enumeration the configuration schema declares for a governance attachment. The
// expected set is read out of the schema bytes the binary embeds and is never
// restated here, so a role the schema gains or loses fails this test until the
// hint offers exactly the schema's set, in the schema's order.
//
// This is the one place the schema is read for that set. Each place those two
// surfaces state the roles — the command's help, and the refusal each returns for
// a request naming no known role — is checked for this hint, so a place that stops
// naming a declared role fails its own check, and a role vocabulary drifting from
// the schema fails this one.
func TestTheGovernanceRolesHintNamesEveryRoleTheSchemaDeclares(t *testing.T) {
	declared := governanceRolesTheSchemaDeclares(t)
	hint := GovernanceRolesHint()

	if missing := rolesNotNamedIn(hint, declared); len(missing) > 0 {
		t.Errorf("the roles hint does not name %v, which the configuration schema declares as governance roles: %q",
			missing, hint)
	}
	// A hint carrying one extra role still contains every declared one, so the
	// surplus is found from the vocabulary the hint is built from.
	known := make(map[string]bool, len(declared))
	for _, role := range declared {
		known[role] = true
	}
	var extra []string
	for _, role := range plugintype.GovernanceRoles() {
		if !known[string(role)] {
			extra = append(extra, string(role))
		}
	}
	if len(extra) > 0 {
		t.Errorf("the roles hint offers %v, which the configuration schema does not declare, "+
			"so a configuration naming one would be refused", extra)
	}
	if want := choiceList(declared); hint != want {
		t.Errorf("roles hint = %q; want %q, the schema's roles in the schema's order", hint, want)
	}
}

// TestTheGovernanceRoleRefusalNamesEveryRoleTheSchemaDeclares drives an addition
// for the governance type with no role and with a role nothing declares, and pins
// that the refusal names every role the configuration schema declares, in the
// roles hint's words — so a caller refused for its role is told every value it
// could have sent rather than a subset of them.
func TestTheGovernanceRoleRefusalNamesEveryRoleTheSchemaDeclares(t *testing.T) {
	declared := governanceRolesTheSchemaDeclares(t)
	rw := resolveScratch(t, scratchConfig)

	for _, tc := range []struct {
		name string
		role string
	}{
		{name: "no role", role: ""},
		{name: "a role nothing declares", role: "nonsense"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Add(context.Background(), rw, AddOptions{Socket: "governance", Query: "x", Role: tc.role})
			if err == nil {
				t.Fatal("a governance addition with no known role was accepted")
			}
			msg := err.Error()
			if missing := rolesNotNamedIn(msg, declared); len(missing) > 0 {
				t.Errorf("the refusal does not name %v, which the configuration schema declares as governance roles: %s",
					missing, msg)
			}
			if !strings.Contains(msg, "("+GovernanceRolesHint()+")") {
				t.Errorf("the refusal does not state the roles in the hint's words %q: %s", GovernanceRolesHint(), msg)
			}
		})
	}
}

// rolesNotNamedIn returns the roles text does not name, in the order given.
func rolesNotNamedIn(text string, roles []string) []string {
	var missing []string
	for _, role := range roles {
		if !strings.Contains(text, role) {
			missing = append(missing, role)
		}
	}
	return missing
}

// governanceRolesTheSchemaDeclares reads the role enumeration the configuration
// schema declares on a governance attachment, in the schema's order. It fails
// rather than returning an empty set when the path is gone, because an empty
// expectation would pass any text.
func governanceRolesTheSchemaDeclares(t *testing.T) []string {
	t.Helper()
	path := []string{"$defs", "PluginRef", "properties", "role", "enum"}

	var cur any
	if err := json.Unmarshal([]byte(schemas.AggregaConfigV1alpha1), &cur); err != nil {
		t.Fatalf("parse the configuration schema: %v", err)
	}
	for _, key := range path {
		obj, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("configuration schema path %v: the value holding %q is not an object", path, key)
		}
		next, found := obj[key]
		if !found {
			t.Fatalf("configuration schema path %v: key %q not found", path, key)
		}
		cur = next
	}
	values, ok := cur.([]any)
	if !ok || len(values) == 0 {
		t.Fatalf("configuration schema path %v does not hold a non-empty list: %v", path, cur)
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("configuration schema path %v: member %v is not a string", path, v)
		}
		out = append(out, s)
	}
	return out
}

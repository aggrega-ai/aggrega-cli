// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package arm

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
)

// TestArmNotInjectedFoldsTheCaseOfProperties: ARM reads a resource's member
// names regardless of case, so a neighbour's value is written under Properties
// and PROPERTIES as under properties, and nowhere outside it.
func TestArmNotInjectedFoldsTheCaseOfProperties(t *testing.T) {
	typ := &schemadraft.Type{Name: "Microsoft.Web/staticSites"}
	for _, c := range []struct {
		path    []string
		refused bool
	}{
		{[]string{"properties", "targetId"}, false},
		{[]string{"Properties", "targetId"}, false},
		{[]string{"PROPERTIES"}, false},
		{[]string{"tags", "owner"}, true},
		{[]string{"propertiesX", "owner"}, true},
	} {
		p := &schemadraft.Property{Name: c.path[len(c.path)-1], Path: c.path}
		if got := Format.Rules.NotInjected(typ, p); (got != "") != c.refused {
			t.Errorf("at %v the rule says %q, want a reason: %v", c.path, got, c.refused)
		}
		if got := Format.Rules.WhyNotInjected(typ, p); (got != "") != c.refused {
			t.Errorf("at %v WhyNotInjected says %q, want a reason: %v", c.path, got, c.refused)
		}
	}
}

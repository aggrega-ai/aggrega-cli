// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package plugintype_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
)

// TestHostExportsAgreeWithTheExportRules holds the two rules that name an export
// beside the host-export lists — a type's exact minimum export, and a governance
// role's default — to those lists, and the linter's prefix rule to every export
// its list names. The rules read the same declarations the lists hold, so this
// fails only once someone spells an export a second time in a rule; it is the
// check that makes that edit visible rather than a convention that hopes nobody
// makes it.
func TestHostExportsAgreeWithTheExportRules(t *testing.T) {
	t.Parallel()

	for _, e := range plugintype.Entries() {
		if e.MinExport.Name == "" {
			continue
		}
		if e.MinExport.IsPrefix {
			if len(e.HostExports) == 0 {
				t.Errorf("plugin type %q carries a prefix rule %q but lists no host export, so the host would call "+
					"nothing it chose", e.Type, e.MinExport.Name)
			}
			for _, name := range e.HostExports {
				if !strings.HasPrefix(name, e.MinExport.Name) {
					t.Errorf("plugin type %q lists host export %q, which its own minimum-export rule (prefix %q) "+
						"refuses at load", e.Type, name, e.MinExport.Name)
				}
			}
			continue
		}
		if !contains(e.HostExports, e.MinExport.Name) {
			t.Errorf("plugin type %q requires export %q at load but its host-export list %v does not hold it",
				e.Type, e.MinExport.Name, e.HostExports)
		}
	}

	governance, ok := plugintype.Lookup(plugintype.Governance)
	if !ok {
		t.Fatal("the registry carries no governance type")
	}
	var defaults []string
	for _, role := range plugintype.GovernanceRoles() {
		export, ok := plugintype.GovernanceRoleExport(role)
		if !ok {
			t.Fatalf("role %q has no default export", role)
		}
		defaults = append(defaults, export)
	}
	if strings.Join(defaults, ",") != strings.Join(governance.HostExports, ",") {
		t.Errorf("the governance host-export list is %v; want the role defaults in role order, %v",
			governance.HostExports, defaults)
	}
}

// TestHostExportsNameEachExportOnce refuses an empty name and a name listed
// twice anywhere in the registry. Each listed export becomes one compiler
// identifier, so a name listed under two types would have to be told apart by
// something other than its name, and nothing reads the lists that way.
func TestHostExportsNameEachExportOnce(t *testing.T) {
	t.Parallel()

	seen := map[string]plugintype.Type{}
	total := 0
	for _, e := range plugintype.Entries() {
		for _, name := range e.HostExports {
			total++
			if name == "" {
				t.Errorf("plugin type %q lists an empty host export", e.Type)
				continue
			}
			if prev, dup := seen[name]; dup {
				t.Errorf("host export %q is listed under plugin types %q and %q", name, prev, e.Type)
				continue
			}
			seen[name] = e.Type
		}
	}
	if total == 0 {
		t.Fatal("the registry lists no host export at all, so this check proves nothing")
	}
}

// TestHostExportsCannotBeChangedThroughAnAccessor writes into the list each
// accessor returns and reads the registry again. A list shared with the registry
// would let any caller change which exports the host may call, so both accessors
// hand out a copy.
func TestHostExportsCannotBeChangedThroughAnAccessor(t *testing.T) {
	t.Parallel()

	before, ok := plugintype.Lookup(plugintype.Linter)
	if !ok || len(before.HostExports) == 0 {
		t.Fatal("the linter type lists no host export, so there is nothing to overwrite")
	}
	want := before.HostExports[0]

	viaLookup, _ := plugintype.Lookup(plugintype.Linter)
	viaLookup.HostExports[0] = "overwritten_through_lookup"
	for _, e := range plugintype.Entries() {
		if e.Type == plugintype.Linter {
			e.HostExports[0] = "overwritten_through_entries"
		}
	}

	after, _ := plugintype.Lookup(plugintype.Linter)
	if after.HostExports[0] != want {
		t.Fatalf("the registry's linter host export changed to %q through an accessor; want %q",
			after.HostExports[0], want)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

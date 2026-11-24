// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armimport

import (
	"regexp"
	"slices"
	"strings"
	"testing"
)

// TestTheAVMIndexIsOrderedByUniquePrefix requires the index to hold its rows in
// prefix order with no prefix or module twice, each row naming all four of its
// fields, the prefix of a resource module and the module path under avm/res/.
func TestTheAVMIndexIsOrderedByUniquePrefix(t *testing.T) {
	t.Parallel()
	if len(avmIndex) == 0 {
		t.Fatal("the index holds no module")
	}
	modules := map[string]bool{}
	for i, m := range avmIndex {
		if m.Prefix == "" || m.Module == "" || m.Namespace == "" || m.Type == "" {
			t.Errorf("the row of %q leaves a field empty: %#v", m.Prefix, m)
		}
		if !strings.HasPrefix(m.Prefix, "46d3xbcp.res.") || !strings.HasPrefix(m.Module, "avm/res/") {
			t.Errorf("the row %#v is not a resource module's", m)
		}
		if i > 0 && avmIndex[i-1].Prefix >= m.Prefix {
			t.Errorf("the prefix %q follows %q: the rows are not in order of unique prefix", m.Prefix, avmIndex[i-1].Prefix)
		}
		if modules[m.Module] {
			t.Errorf("the module %s is in the index twice", m.Module)
		}
		modules[m.Module] = true
	}
	if !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(AVMIndexCommit) {
		t.Errorf("the index names the commit %q, which is not a full commit name", AVMIndexCommit)
	}
}

// TestTheAVMIndexIsFoundByPrefixAndByModule requires each row to be found by
// its prefix and by its module path, and a prefix or a path the index does not
// hold, or one that differs from a row's only in case, not to be.
func TestTheAVMIndexIsFoundByPrefixAndByModule(t *testing.T) {
	t.Parallel()
	for _, m := range avmIndex {
		if got, ok := AVMModuleByPrefix(m.Prefix); !ok || got != m {
			t.Errorf("the prefix %s found %#v, %v; want %#v", m.Prefix, got, ok, m)
		}
		if got, ok := AVMModuleByPath(m.Module); !ok || got != m {
			t.Errorf("the module %s found %#v, %v; want %#v", m.Module, got, ok, m)
		}
	}
	for _, absent := range []string{"", "46d3xbcp.res.absent-0000", "avm/res/absent/module",
		strings.ToUpper(avmIndex[0].Module)} {
		if got, ok := AVMModuleByPrefix(absent); ok {
			t.Errorf("the prefix %q found %#v", absent, got)
		}
		if got, ok := AVMModuleByPath(absent); ok {
			t.Errorf("the module %q found %#v", absent, got)
		}
	}
	if !slices.IsSortedFunc(avmIndex, func(a, b AVMModule) int { return strings.Compare(a.Prefix, b.Prefix) }) {
		t.Error("the index is not in prefix order, which the prefix search needs")
	}
}

// TestAModulesResourceTypeIsItsNamespaceAndType requires the type a row gives
// as its module's to join its provider namespace and its resource type with a
// slash, the Container Apps module's being Microsoft.App/containerApps.
func TestAModulesResourceTypeIsItsNamespaceAndType(t *testing.T) {
	t.Parallel()
	m, ok := AVMModuleByPath("avm/res/app/container-app")
	if !ok {
		t.Fatal("the index holds no row of the Container Apps module")
	}
	if got := m.ResourceType(); got != "Microsoft.App/containerApps" {
		t.Errorf("the module's resource type is %q, want Microsoft.App/containerApps", got)
	}
}

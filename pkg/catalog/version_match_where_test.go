// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"errors"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

type whereEntry struct{ name, version string }

func whereIndex(entries ...whereEntry) map[string]whereEntry {
	idx := map[string]whereEntry{}
	for _, e := range entries {
		idx[e.name+":"+e.version] = e
	}
	return idx
}

func whereMeta(e whereEntry) (string, string) { return e.name, e.version }

func admitOnly(versions ...string) func(string) bool {
	return func(v string) bool {
		for _, w := range versions {
			if v == w {
				return true
			}
		}
		return false
	}
}

func whereXIndex() map[string]whereEntry {
	return whereIndex(
		whereEntry{"core/x", "1.0.0"},
		whereEntry{"core/x", "1.1.0"},
		whereEntry{"core/x", "1.2.0-rc.1"},
		whereEntry{"core/x", "2.0.0"},
	)
}

func TestSelectWhereTakesHighestAdmitted(t *testing.T) {
	all := func(string) bool { return true }
	cases := []struct {
		ref    string
		admits func(string) bool
		want   string
	}{
		{"core/x", admitOnly("1.0.0", "1.1.0"), "core/x:1.1.0"},
		{"core/x:^1.0.0", all, "core/x:1.1.0"},
		{"core/x:1.0.0", all, "core/x:1.0.0"},
	}
	for _, tc := range cases {
		key, ok, err := selectEntityVersionWhere(whereXIndex(), tc.ref, whereMeta, tc.admits)
		if err != nil || !ok || key != tc.want {
			t.Errorf("%s: got (%q, %v, %v), want %q", tc.ref, key, ok, err, tc.want)
		}
	}
}

func TestSelectWhereConflictIsItsOwnCondition(t *testing.T) {
	_, ok, err := selectEntityVersionWhere(whereXIndex(), "core/x:2.0.0", whereMeta, admitOnly("1.0.0"))
	if ok || !errors.Is(err, ErrConflictingVersionConstraints()) {
		t.Fatalf("got ok=%v err=%v, want the conflict condition", ok, err)
	}
	if errors.Is(err, ErrUnsatisfiableEntityVersion()) {
		t.Fatalf("conflict must not be E0103: %v", err)
	}
}

func TestSelectWhereKeepsReferenceFaultsWhateverThePredicate(t *testing.T) {
	none := func(string) bool { return false }
	_, ok, err := selectEntityVersionWhere(whereXIndex(), "core/x:3.0.0", whereMeta, none)
	if ok || !errors.Is(err, ErrUnsatisfiableEntityVersion()) {
		t.Fatalf("unsatisfiable reference: ok=%v err=%v", ok, err)
	}
	key, ok, err := selectEntityVersionWhere(whereXIndex(), "core/y", whereMeta, none)
	if key != "" || ok || err != nil {
		t.Fatalf("absent base: got (%q, %v, %v)", key, ok, err)
	}
}

func TestSelectWhereReportsMalformedPinNotConflict(t *testing.T) {
	pin := types.ParseVersionPin("core/x:1.x")
	if pin.Fault() == nil {
		t.Fatal("fixture pin core/x:1.x must be malformed")
	}
	_, ok, err := selectEntityVersionWhere(whereXIndex(), "core/x:1.x", whereMeta, func(string) bool { return false })
	if ok || err == nil || errors.Is(err, ErrConflictingVersionConstraints()) || err.Error() != pin.Fault().Error() {
		t.Fatalf("got ok=%v err=%v, want the malformed-pin fault", ok, err)
	}
}

func TestSelectWhereKeepsPrereleaseAndFallbackRules(t *testing.T) {
	_, ok, err := selectEntityVersionWhere(whereXIndex(), "core/x", whereMeta, admitOnly("1.2.0-rc.1"))
	if ok || !errors.Is(err, ErrConflictingVersionConstraints()) {
		t.Fatalf("pre-release admitted alone: ok=%v err=%v, want the conflict condition", ok, err)
	}

	versionless := whereIndex(whereEntry{"core/z", ""})
	key, ok, err := selectEntityVersionWhere(versionless, "core/z", whereMeta, nil)
	if err != nil || !ok || key != "core/z:" {
		t.Fatalf("version-less fallback: got (%q, %v, %v)", key, ok, err)
	}
}

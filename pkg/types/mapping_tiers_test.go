// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package types

import (
	"reflect"
	"strings"
	"testing"
)

// TestTiersNamesEveryTierAMappingCarries holds MappingTier.Tiers to the type it
// presents. Tiers is the one statement of which tiers a mapping carries, read by
// the lookup that picks an instance's realization and by the lock-time walk that
// pins every realization a mapping binds; a tier field it left out would be read by
// neither of them, and a reader that went to the field directly would let the two
// disagree. So every field of the realization-binding shape — an
// OrderedMap[string] — must appear among the tiers under the key its YAML tag
// writes, carrying that field's own entries, and the tiers must be in the order a
// lookup consults them, instance name first.
//
// Each field is given an entry of its own before Tiers is read, so a tier wired to
// the wrong field reports the wrong entry rather than passing on two empty maps.
func TestTiersNamesEveryTierAMappingCarries(t *testing.T) {
	t.Parallel()

	var tier MappingTier
	tierType := reflect.TypeOf(tier)
	bindingShape := reflect.TypeOf(OrderedMap[string]{})
	want := map[string]string{} // YAML key -> the one key set in that field
	for i := 0; i < tierType.NumField(); i++ {
		f := tierType.Field(i)
		if f.Type != bindingShape {
			continue
		}
		key, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		entry := "entry-of-" + key
		m := reflect.ValueOf(&tier).Elem().Field(i).Addr().Interface().(*OrderedMap[string])
		m.Set(entry, "realization-of-"+key)
		want[key] = entry
	}
	if len(want) == 0 {
		t.Fatal("calibration: MappingTier carries no OrderedMap[string] field, so this guard compares nothing")
	}

	tiers := tier.Tiers()
	got := map[string]string{}
	var order []string
	for _, tr := range tiers {
		order = append(order, tr.Key)
		keys := tr.Entries.Keys()
		if len(keys) != 1 {
			t.Errorf("tier %q carries %d entries; want the one set in its field", tr.Key, len(keys))
			continue
		}
		got[tr.Key] = keys[0]
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Tiers() presents %v; the type's tier fields hold %v", got, want)
	}
	if strings.Join(order, ",") != "name,type" {
		t.Errorf("Tiers() order = %v; want the instance-name tier first, then the type tier", order)
	}
	for _, tr := range tiers {
		if wantByName := tr.Key == "name"; tr.ByInstanceName != wantByName {
			t.Errorf("tier %q ByInstanceName = %v; want %v", tr.Key, tr.ByInstanceName, wantByName)
		}
	}

	var absent *MappingTier
	if n := len(absent.Tiers()); n != 0 {
		t.Errorf("a nil mapping presents %d tiers; want none", n)
	}
}

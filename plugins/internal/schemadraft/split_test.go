// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package schemadraft

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// prop is a top-level property of the kinds given, in the vendor's spelling.
func prop(name string, kinds ...Kind) *Property {
	return &Property{Name: name, Path: []string{name}, Kinds: kinds}
}

// member is a property nested under the path parent.
func member(parent []string, name string, kinds ...Kind) *Property {
	return &Property{Name: name, Path: append(slices.Clone(parent), name), Kinds: kinds}
}

// named is a format rule matching the properties of the names given.
func named(names ...string) func(*Type, *Property) bool {
	return func(_ *Type, p *Property) bool { return slices.Contains(names, p.Name) }
}

// testRules places location, takes QueueName for identity and
// MasterUserPassword for a secret.
var testRules = Rules{
	Placement: named("location"),
	Identity:  named("QueueName"),
	Secret:    named("MasterUserPassword"),
}

func typeOf(props ...*Property) *Type {
	return &Type{Name: "Test::Service::Thing", Description: "A thing.", Properties: props}
}

// placedAs returns what Split placed under the vendor name, failing where it
// placed nothing or more than one.
func placedAs(t *testing.T, placed []Placed, vendor string) Placed {
	t.Helper()
	var found []Placed
	for _, p := range placed {
		if strings.Join(p.Property.Path, ".") == vendor {
			found = append(found, p)
		}
	}
	if len(found) != 1 {
		t.Fatalf("placed %d entries for %s, want 1: %+v", len(found), vendor, placed)
	}
	return found[0]
}

func TestSplitReadBackScalarIsProvided(t *testing.T) {
	arn := prop("Arn", KindString)
	arn.ReadOnly = true
	placed, ds := Split(typeOf(arn), testRules)
	got := placedAs(t, placed, "Arn")
	if got.Pillar != PillarProvides || got.Key != "arn" || len(ds) != 0 {
		t.Fatalf("Arn placed %+v, diagnostics %+v; want provides key arn", got, ds)
	}
}

// TestSplitReadBackNestedScalarIsProvided holds a format whose read-back
// attributes are addressed by a dotted path to providing each nested scalar
// under the key its joined path gives, and every other format to listing the
// object as not provided.
func TestSplitReadBackNestedScalarIsProvided(t *testing.T) {
	endpoint := func() *Property {
		e := prop("Endpoint", KindObject)
		address := member(e.Path, "Address", KindString)
		port := member(e.Path, "Port", KindInteger)
		zones := member(e.Path, "Zones", KindArray)
		address.ReadOnly, port.ReadOnly, zones.ReadOnly = true, true, true
		e.Properties = []*Property{address, port, zones}
		return e
	}
	nested := testRules
	nested.NestedProvides = true
	placed, _ := Split(typeOf(endpoint()), nested)
	if got := placedAs(t, placed, "Endpoint.Address"); got.Pillar != PillarProvides || got.Key != "endpoint_address" {
		t.Errorf("Endpoint.Address placed %+v, want provides key endpoint_address", got)
	}
	if got := placedAs(t, placed, "Endpoint.Port"); got.Pillar != PillarProvides || got.Key != "endpoint_port" {
		t.Errorf("Endpoint.Port placed %+v, want provides key endpoint_port", got)
	}
	if got := placedAs(t, placed, "Endpoint.Zones"); got.Pillar != PillarNotProvided {
		t.Errorf("Endpoint.Zones placed %+v, want not provided", got)
	}

	placed, _ = Split(typeOf(endpoint()), testRules)
	if got := placedAs(t, placed, "Endpoint"); got.Pillar != PillarNotProvided || len(placed) != 1 {
		t.Errorf("without nested provides placed %+v, want Endpoint alone, not provided", placed)
	}
}

func TestSplitReadBackNonScalarIsNotProvided(t *testing.T) {
	zones := prop("Zones", KindArray)
	zones.ReadOnly = true
	placed, _ := Split(typeOf(zones), testRules)
	if got := placedAs(t, placed, "Zones"); got.Pillar != PillarNotProvided {
		t.Fatalf("Zones placed %+v, want not provided", got)
	}
}

func TestSplitSensitiveReadBackIsWithheld(t *testing.T) {
	token := prop("Token", KindString)
	token.ReadOnly, token.Sensitive = true, true
	placed, _ := Split(typeOf(token), testRules)
	if got := placedAs(t, placed, "Token"); got.Pillar != PillarWithheld {
		t.Fatalf("Token placed %+v, want withheld", got)
	}
}

func TestSplitSecretIsSecret(t *testing.T) {
	password := prop("MasterUserPassword", KindString)
	password.Required = true
	placed, _ := Split(typeOf(password), testRules)
	got := placedAs(t, placed, "MasterUserPassword")
	if got.Pillar != PillarSecret || got.Key != "master_user_password" || !got.Required {
		t.Fatalf("MasterUserPassword placed %+v, want a required secret keyed master_user_password", got)
	}
}

func TestSplitPlacementIsTypedInfra(t *testing.T) {
	location := prop("location", KindString)
	location.Required = true
	placed, _ := Split(typeOf(location), testRules)
	got := placedAs(t, placed, "location")
	if got.Pillar != PillarInfra || got.Key != "location" || !got.Required || !strings.HasPrefix(got.Rule, "placement") {
		t.Fatalf("location placed %+v, want required infra by the placement rule", got)
	}
}

func TestSplitIdentityIsValues(t *testing.T) {
	name := prop("QueueName", KindString)
	placed, _ := Split(typeOf(name), testRules)
	got := placedAs(t, placed, "QueueName")
	if got.Pillar != PillarValues || got.Key != "queue_name" || got.Required || !strings.HasPrefix(got.Rule, "identity") {
		t.Fatalf("QueueName placed %+v, want optional values key queue_name by the identity rule", got)
	}
}

func TestSplitOtherRequiredIsRequiredInfra(t *testing.T) {
	class := prop("InstanceClass", KindString)
	class.Required = true
	placed, _ := Split(typeOf(class), testRules)
	got := placedAs(t, placed, "InstanceClass")
	if got.Pillar != PillarInfra || got.Key != "instance_class" || !got.Required || !strings.HasPrefix(got.Rule, "required") {
		t.Fatalf("InstanceClass placed %+v, want required infra key instance_class by the required rule", got)
	}
}

func TestSplitCreateOnlyIsReplacedOnChange(t *testing.T) {
	key := prop("KmsKeyId", KindString)
	key.CreateOnly = true
	name := prop("QueueName", KindString)
	name.CreateOnly = true
	placed, _ := Split(typeOf(key, name), testRules)
	if got := placedAs(t, placed, "KmsKeyId"); !strings.Contains(got.Rule, "replaced on change") {
		t.Errorf("KmsKeyId rule %q, want it to say replaced on change", got.Rule)
	}
	if got := placedAs(t, placed, "QueueName"); strings.Contains(got.Rule, "replaced on change") {
		t.Errorf("identity QueueName rule %q says replaced on change", got.Rule)
	}
}

func TestSplitDeprecatedIsMarked(t *testing.T) {
	old := prop("OldSetting", KindString)
	old.Deprecated = true
	placed, _ := Split(typeOf(old), testRules)
	if got := placedAs(t, placed, "OldSetting"); !strings.Contains(got.Rule, "deprecated") {
		t.Fatalf("OldSetting rule %q, want it to say deprecated", got.Rule)
	}
}

func TestSplitWritableIsOverride(t *testing.T) {
	delay := prop("DelaySeconds", KindInteger)
	placed, _ := Split(typeOf(delay), testRules)
	got := placedAs(t, placed, "DelaySeconds")
	if got.Pillar != PillarOverrides || got.Key != "DelaySeconds" || got.Required {
		t.Fatalf("DelaySeconds placed %+v, want an override under the vendor's spelling", got)
	}
}

// TestSplitReportsEachCutOnce holds Split to one constraint-dropped warning for
// a cut in an override, which no Component key describes, and one for a cut in
// a typed infra key.
func TestSplitReportsEachCutOnce(t *testing.T) {
	policy := prop("Policy", KindObject)
	policy.Open, policy.Cut = true, "cycle"
	config := prop("Config", KindObject)
	config.Required, config.Open, config.Cut = true, true, "depth"
	_, ds := Split(typeOf(config, policy), testRules)
	if len(ds) != 2 {
		t.Fatalf("diagnostics %+v, want one for each cut", ds)
	}
	for i, vendor := range []string{"Config", "Policy"} {
		if ds[i].Code != codeConstraintDropped || ds[i].Severity != "warn" || ds[i].SourceRef != vendor {
			t.Errorf("diagnostic %d = %+v, want a constraint-dropped warning on %s", i, ds[i], vendor)
		}
	}
}

func TestSplitIsDeterministic(t *testing.T) {
	build := func() *Type {
		a, b, c := prop("Zeta", KindString), prop("Alpha", KindString), prop("Mid", KindBoolean)
		a.Required = true
		c.ReadOnly = true
		return typeOf(b, c, a)
	}
	first, d1 := Split(build(), testRules)
	second, d2 := Split(build(), testRules)
	if !reflect.DeepEqual(first, second) || !reflect.DeepEqual(d1, d2) {
		t.Fatalf("two splits of one type differ:\n%+v\n%+v", first, second)
	}
}

// onlyWarning returns the one constraint-dropped warning of ds.
func onlyWarning(t *testing.T, ds []aggrega.Diagnostic) aggrega.Diagnostic {
	t.Helper()
	if len(ds) != 1 || ds[0].Code != codeConstraintDropped || ds[0].Severity != "warn" {
		t.Fatalf("diagnostics = %+v, want one constraint-dropped warning", ds)
	}
	return ds[0]
}

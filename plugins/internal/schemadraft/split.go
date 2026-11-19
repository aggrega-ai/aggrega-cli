// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package schemadraft

import (
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// Pillar is where a property of a resource is placed in its draft.
type Pillar int

// The places a property takes.
const (
	// PillarValues is a key of the Component's values: the resource's identity.
	PillarValues Pillar = iota
	// PillarInfra is a typed key of the Component's infra: a placement or
	// another property the vendor requires.
	PillarInfra
	// PillarOverrides is a member of infra.overrides under the vendor's own
	// spelling: every other writable property.
	PillarOverrides
	// PillarSecret is a secret: a featuresSchema slot of the Component, or a
	// deploy-time parameter the Recipe declares.
	PillarSecret
	// PillarProvides is a key of the Component's provides: a scalar the vendor
	// reads back.
	PillarProvides
	// PillarWithheld is a sensitive property the vendor reads back, which
	// provides do not carry.
	PillarWithheld
	// PillarNotProvided is a property the vendor reads back that is not a
	// scalar, which provides do not carry.
	PillarNotProvided
	// PillarInject is an input the Recipe reads off the instance's inject pipe.
	// Split places none.
	PillarInject
)

// Placed is one property of a resource and where its draft places it.
type Placed struct {
	Property *Property
	Pillar   Pillar
	// Key is snake_case under values, infra, provides and a secret, and the
	// vendor's spelling under overrides, or its path, dotted, where an override
	// placed before it holds its name; for a property neither drafted nor
	// provided it is the vendor's path, dotted.
	Key string
	// Rule is composed text naming the rule that placed the property, and
	// where they hold, that it is replaced on change, that it is deprecated
	// and why its key carries a numeric suffix.
	Rule string
	// Renamed is the composed text naming the property and the one holding the
	// key its name gives, where its key carries the kit's numeric suffix or, for
	// an override, is its dotted path; Rule carries it too. It is empty
	// otherwise.
	Renamed string
	// Required is set for a key a draft requires. A provides key is never
	// required, since it is published rather than read.
	Required bool
}

// Rules is what a format decides; the order of the rules Split applies is the
// core's.
type Rules struct {
	// Placement, Identity and Secret each report whether a property falls under
	// that rule; a nil rule takes none.
	Placement, Identity, Secret func(t *Type, p *Property) bool
	// SecretSlots drafts a secret as a featuresSchema slot of the Component;
	// otherwise the Recipe declares it as a deploy-time parameter.
	SecretSlots bool
	// NestedProvides provides a read-back scalar nested in objects as well as
	// one at the top level, under the key of its joined path, for a format
	// whose provider addresses a nested attribute by a dotted path.
	NestedProvides bool
	// NotProvided returns why the format leaves out of provides a property the
	// vendor reads back, members and all, or "" to place it as rule 1 does; a
	// nil rule leaves out none.
	NotProvided func(t *Type, p *Property) string
}

// overridesKey is the infra key holding every writable property no other rule
// places.
const overridesKey = "overrides"

// The composed text naming each rule.
const (
	ruleProvides    = "read back: published under provides"
	ruleWithheld    = "read back and sensitive: withheld, since provides are piped as plain strings"
	ruleNotProvided = "read back and not a scalar: not provided, since provides are piped as strings"
	// ruleLeftOut opens the rule of a read-back property the format leaves out
	// of provides, followed by its reason.
	ruleLeftOut     = "read back and not provided: "
	ruleSecretSlot  = "secret: delivered by an attachment on its featuresSchema slot"
	ruleSecretParam = "secret: declared by the Recipe as a deploy-time parameter"
	rulePlacement   = "placement: where the resource is deployed"
	ruleIdentity    = "identity: the resource's own name or identifier"
	ruleRequired    = "required: the vendor schema requires it"
	ruleOverride    = "override: writable, set under infra.overrides in the vendor's spelling"
	// ruleOverridesMap names the rule of infra.overrides itself.
	ruleOverridesMap = "override: every writable property no other rule places, each under the vendor's own name"

	noteReplacedOnChange = "replaced on change"
	noteDeprecated       = "deprecated"
)

// Split places each property of t by the first of these rules that takes it:
//
//  1. read back only: one the format leaves out (Rules.NotProvided) is listed
//     as not provided, for its reason; a sensitive one is withheld; a scalar is
//     provided, under the snake_case key of its name (with
//     Rules.NestedProvides, a scalar nested in objects too, under the key of
//     its joined path); any other is listed as not provided;
//  2. secret (Rules.Secret);
//  3. placement (Rules.Placement), typed in infra;
//  4. identity (Rules.Identity), in values;
//  5. any other property the vendor requires, typed and required in infra;
//  6. every other writable property, in infra.overrides under its vendor name,
//     or under its vendor path, dotted, where an override placed before it
//     holds that name, as the root kind and properties.kind of one resource
//     both would.
//
// An object every member of which is read back is read back. Placement and
// identity are required where the vendor requires them; a provides key is
// never required, whatever the vendor says of it. A create-only property
// outside identity is marked replaced on change, unless it is read back, which
// a draft writes no value for; a deprecated one is marked deprecated.
// Keys are taken in the vendor's order, each pillar its own space, infra's
// holding overrides already; a name reaching a taken key takes the kit's
// numeric suffix, and its rule and its Renamed name both.
//
// It returns the constraint-dropped warnings of carrying each key it types — in
// values, infra and provides — into its pillar, for an override one for each
// cut in it, and one for each member below a key it drafts that the format reads
// as a secret, since only a property at the top of the resource becomes one.
func Split(t *Type, r Rules) ([]Placed, []aggrega.Diagnostic) {
	s := &splitter{t: t, r: r, values: newKeySpace(), infra: newKeySpace(overridesKey), provides: newKeySpace(),
		secrets: newKeySpace(), overrides: overrideKeys{}}
	for _, p := range t.Properties {
		s.place(p)
	}
	return s.placed, s.diags
}

// splitter holds what Split has placed so far and the key space of each pillar.
type splitter struct {
	t                                *Type
	r                                Rules
	values, infra, provides, secrets *keySpace
	overrides                        overrideKeys
	placed                           []Placed
	diags                            []aggrega.Diagnostic
}

func (s *splitter) place(p *Property) {
	if readBack(p) {
		s.readBack(p, []string{p.Name})
		return
	}
	switch {
	case ruled(s.r.Secret, s.t, p):
		rule := ruleSecretParam
		if s.r.SecretSlots {
			rule = ruleSecretSlot
		}
		s.keyed(p, p.Name, PillarSecret, s.secrets, rule, p.Required)
	case ruled(s.r.Placement, s.t, p):
		s.nestedSecrets(p, "infra."+s.keyed(p, p.Name, PillarInfra, s.infra, rulePlacement, p.Required))
	case ruled(s.r.Identity, s.t, p):
		s.nestedSecrets(p, "values."+s.keyed(p, p.Name, PillarValues, s.values, ruleIdentity, p.Required))
	case p.Required:
		s.nestedSecrets(p, "infra."+s.keyed(p, p.Name, PillarInfra, s.infra, ruleRequired, true))
	default:
		key, renamed := s.overrides.take(p)
		s.placed = append(s.placed, Placed{Property: p, Pillar: PillarOverrides, Key: key,
			Rule: noted(ruleOverride, p.CreateOnly, p.Deprecated, renamed), Renamed: renamed})
		s.diags = append(s.diags, cuts(p, vendorPath(p))...)
		s.nestedSecrets(p, "infra."+overridesKey+"."+key)
	}
	if s.r.NestedProvides {
		s.nestedReadBack(p, []string{p.Name})
	}
}

// readBack places p, read back only, reached by the vendor names names.
func (s *splitter) readBack(p *Property, names []string) {
	vendor := strings.Join(names, ".")
	var why string
	if s.r.NotProvided != nil {
		why = s.r.NotProvided(s.t, p)
	}
	switch {
	case why != "":
		s.placed = append(s.placed, Placed{Property: p, Pillar: PillarNotProvided, Key: vendor, Rule: ruleLeftOut + why})
	case p.Sensitive:
		s.placed = append(s.placed, Placed{Property: p, Pillar: PillarWithheld, Key: vendor, Rule: ruleWithheld})
	case scalar(p):
		s.keyed(p, vendor, PillarProvides, s.provides, ruleProvides, false)
	case s.r.NestedProvides && kindAlone(p, KindObject) && !p.Open && p.Cut == "" && len(p.Properties) > 0:
		for _, m := range p.Properties {
			s.readBack(m, append(names[:len(names):len(names)], m.Name))
		}
	default:
		s.placed = append(s.placed, Placed{Property: p, Pillar: PillarNotProvided, Key: vendor, Rule: ruleNotProvided})
	}
}

// nestedReadBack provides the read-back members of the writable object p,
// reached through objects alone, which a dotted path addresses.
func (s *splitter) nestedReadBack(p *Property, names []string) {
	if !kindAlone(p, KindObject) || p.Open || p.Cut != "" {
		return
	}
	for _, m := range p.Properties {
		path := append(names[:len(names):len(names)], m.Name)
		if readBack(m) {
			s.readBack(m, path)
		} else {
			s.nestedReadBack(m, path)
		}
	}
}

// nestedSecrets warns, under constraint-dropped, of each member of p at any
// depth — an item of an array included — that the format reads as a secret
// (Rules.Secret) and that a draft writes, naming its vendor path and where, the
// key p is drafted under. Only a property at the top of the resource is drafted
// as a secret, so a Release sets such a member as a plain value inside that key.
// A member read back is no input, and is not warned of; nor is anything below a
// member warned of already.
func (s *splitter) nestedSecrets(p *Property, where string) {
	if s.r.Secret == nil {
		return
	}
	var walk func(m *Property, path string)
	members := func(m *Property, path string) {
		if m.Items != nil {
			walk(m.Items, path+"[]")
		}
		for _, c := range m.Properties {
			walk(c, path+"."+c.Name)
		}
	}
	walk = func(m *Property, path string) {
		switch {
		case readBack(m):
		case s.r.Secret(s.t, m):
			s.diags = append(s.diags, constraintDropped(path, "the vendor schema's sensitivity as a secret",
				"a draft makes a secret of a property at the top of the resource alone, so a Release sets this one "+
					"as a plain value inside "+where))
		default:
			members(m, path)
		}
	}
	members(p, vendorPath(p))
}

// keyed places p under a key taken in space for the vendor name, records the
// warnings of carrying it into its pillar, and returns the key.
func (s *splitter) keyed(p *Property, vendor string, pillar Pillar, space *keySpace, rule string, required bool) string {
	key, renamed := space.take(vendor)
	rule = noted(rule, p.CreateOnly && pillar != PillarValues && pillar != PillarProvides, p.Deprecated, renamed)
	s.placed = append(s.placed, Placed{Property: p, Pillar: pillar, Key: key, Rule: rule, Renamed: renamed,
		Required: required})
	if pillar != PillarSecret {
		_, d := Carry(p, key)
		s.diags = append(s.diags, d...)
	}
	return key
}

// noted appends to rule what holds beside it: that the property is replaced
// on change, that it is deprecated, and why its key carries a suffix.
func noted(rule string, replaced, deprecated bool, renamed string) string {
	parts := []string{rule}
	if replaced {
		parts = append(parts, noteReplacedOnChange)
	}
	if deprecated {
		parts = append(parts, noteDeprecated)
	}
	if renamed != "" {
		parts = append(parts, renamed)
	}
	return strings.Join(parts, "; ")
}

// overrideKeys holds the keys taken under infra.overrides, where each
// writable property no other rule places is read in the vendor's spelling.
type overrideKeys map[string]bool

// take returns the key the override p is read under: its vendor name, or
// where an override taken before it holds that name, its vendor path, dotted,
// with composed text saying why. A format whose properties of one name sit at
// several paths, such as a resource's root keys and the members of its
// properties, so reads each under a key of its own.
func (o overrideKeys) take(p *Property) (key, renamed string) {
	key = p.Name
	if o[key] {
		key = vendorPath(p)
		renamed = fmt.Sprintf("%s is read under its path, since the override %s holds its name", key, p.Name)
	}
	o[key] = true
	return key, renamed
}

func ruled(rule func(*Type, *Property) bool, t *Type, p *Property) bool {
	return rule != nil && rule(t, p)
}

// readBack reports whether the vendor only ever reads p back: it is read-only,
// or a closed object every member of which is read back.
func readBack(p *Property) bool {
	if p.ReadOnly {
		return true
	}
	if !kindAlone(p, KindObject) || p.Open || p.Cut != "" || len(p.Properties) == 0 {
		return false
	}
	for _, m := range p.Properties {
		if !readBack(m) {
			return false
		}
	}
	return true
}

// scalar reports whether every kind p takes is a string, a number or a boolean.
func scalar(p *Property) bool {
	if len(p.Kinds) == 0 {
		return false
	}
	for _, k := range p.Kinds {
		switch k {
		case KindString, KindInteger, KindNumber, KindBoolean:
		default:
			return false
		}
	}
	return true
}

// kindAlone reports whether p takes the one kind k.
func kindAlone(p *Property, k Kind) bool { return len(p.Kinds) == 1 && p.Kinds[0] == k }

// vendorPath is the vendor's path of p, dotted, or its name where it has none.
func vendorPath(p *Property) string {
	if len(p.Path) == 0 {
		return p.Name
	}
	return strings.Join(p.Path, ".")
}

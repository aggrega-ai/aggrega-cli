// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package schemadraft

import (
	"bytes"
	"encoding/json"
	"slices"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// WrittenProperty is one property as a source writes it.
type WrittenProperty struct {
	// Path is the vendor path from the resource body root: each segment a
	// member's key, or an array item's index written in decimal.
	Path []string
	// Value is the value as written, lowered; nil for an inject or a secret.
	Value json.RawMessage
	// Secret is set for a property fed by a secure parameter, or named by the
	// format's credential-word rule.
	Secret bool
	// Inject is the property inject key, InjectKey of Path, where a neighbour
	// supplies the value; empty otherwise.
	Inject string
	// Placement is set for where the resource is deployed, and Identity for
	// its own name.
	Placement, Identity bool
}

// WrittenResource is a resource as a source writes it.
type WrittenResource struct {
	// ID is the source's identifier of the resource, which the decision drafting
	// it and the entities it drafts refer to; empty refers to its type.
	ID               string
	Type, APIVersion string
	// Provider is the provider the source names for the resource, with its
	// locked version where it states one; nil where the source names none.
	Provider   *ProviderAddress
	Properties []WrittenProperty
	// Provides are the resource's id and name, plus every attribute another
	// node references, each under the key its consumers read; no two share a
	// key.
	Provides []Provide
}

// FromWritten builds the Type and Inputs a writer drafts from, for w as its
// source writes it: identity → values and placement → typed infra, each under
// the snake_case key Split gives it and required; secret → the format's secret,
// required; inject → InputSource{Pillar: PillarInject, Key: Inject, Mechanism:
// MechanismProperty}, required; every other → PillarOverrides under the vendor
// spelling, as Split keys an override. Each property is named by the last
// segment of its path, marked Written, and typed from the value written: a
// string, integer, number, boolean, object or array, an object passed whole; a
// property written with no value, an inject or a secret, is a string, as the
// property mechanism pipes one and a secure string parameter feeds one; a value
// that is not JSON, or null, is of any kind.
//
// The inputs are the properties Split places, in w's order, followed by those
// a neighbour supplies, in w's order. The Type holds every property it reads
// and, read back as strings and marked Written, each attribute w provides that
// w does not write; its primary identifier is the attribute of the first
// provide marked as the identifier. FromWritten applies no format's rule: every
// property with a path is carried, and one with none, which names no property,
// is left out. DraftResource applies the format's Rules.LeftOut, and halts on a
// resource FromWritten would read past.
func FromWritten(w WrittenResource) (*Type, []Input) {
	d := placeWritten(w, Rules{})
	res, _ := resourceFor(d.t, d.placed)
	return d.t, res.Inputs
}

// writtenDraft is a resource as its source writes it, placed for its draft.
type writtenDraft struct {
	t      *Type
	placed []Placed
	// left holds each written property the format's Rules.LeftOut leaves out,
	// with why.
	left []leftProperty
	// diags are what Split reports of placing the properties.
	diags []aggrega.Diagnostic
}

// leftProperty is a written property a draft leaves out: its dotted path and
// why.
type leftProperty struct{ path, why string }

// note is the composed text naming l and why it is left out.
func (l leftProperty) note() string { return l.path + " is left out: " + l.why }

// placeWritten builds the type of w and places its properties as FromWritten
// describes, through Split for every property but those a neighbour supplies,
// with the secret rule of rules, leaving out each property rules.LeftOut gives a
// reason for. It places each provide under its own key and the rule Split gives
// a property read back.
func placeWritten(w WrittenResource, rules Rules) writtenDraft {
	d := writtenDraft{t: &Type{Name: w.Type, APIVersion: w.APIVersion}}
	if w.Provider != nil {
		provider := *w.Provider
		d.t.Provider = &provider
	}
	placeable := &Type{Name: w.Type, APIVersion: w.APIVersion}
	flags := map[*Property]WrittenProperty{}
	var injects []Placed
	for _, wp := range w.Properties {
		if len(wp.Path) == 0 {
			continue
		}
		kind := kindOf(wp.Value)
		p := &Property{Name: wp.Path[len(wp.Path)-1], Path: slices.Clone(wp.Path), Kinds: []Kind{kind},
			Sensitive: wp.Secret, Open: kind == KindObject, Written: true}
		if rules.LeftOut != nil {
			if why := rules.LeftOut(d.t, p); why != "" {
				d.left = append(d.left, leftProperty{path: vendorPath(p), why: why})
				continue
			}
		}
		d.t.Properties = append(d.t.Properties, p)
		if wp.Inject != "" {
			p.Required = true
			injects = append(injects, Placed{Property: p, Pillar: PillarInject, Key: wp.Inject, Rule: injectRule(p),
				Required: true})
			continue
		}
		p.Required = wp.Identity || wp.Placement || wp.Secret
		flags[p] = wp
		placeable.Properties = append(placeable.Properties, p)
	}
	d.placed, d.diags = Split(placeable, Rules{
		Placement:   func(_ *Type, p *Property) bool { return flags[p].Placement },
		Identity:    func(_ *Type, p *Property) bool { return flags[p].Identity },
		Secret:      func(_ *Type, p *Property) bool { return flags[p].Secret },
		SecretSlots: rules.SecretSlots,
	})
	d.placed = append(d.placed, injects...)

	for _, pr := range w.Provides {
		attribute := pr.Attribute
		if len(attribute) == 0 {
			attribute = []string{pr.Key}
		}
		p := d.attribute(attribute)
		if pr.Identifier && len(d.t.PrimaryIdentifier) == 0 {
			d.t.PrimaryIdentifier = [][]string{slices.Clone(p.Path)}
		}
		d.placed = append(d.placed, Placed{Property: p, Pillar: PillarProvides, Key: pr.Key, Rule: ruleProvides})
	}
	return d
}

// attribute is the property of d's type at path: the one written there, or one
// read back, a string, added to the type where none is.
func (d *writtenDraft) attribute(path []string) *Property {
	for _, p := range d.t.Properties {
		if slices.Equal(p.Path, path) {
			return p
		}
	}
	p := &Property{Name: path[len(path)-1], Path: slices.Clone(path), Kinds: []Kind{KindString}, ReadOnly: true,
		Written: true}
	d.t.Properties = append(d.t.Properties, p)
	return p
}

// kindOf is the kind of the JSON value raw, as FromWritten types a property.
func kindOf(raw json.RawMessage) Kind {
	v := bytes.TrimSpace(raw)
	switch {
	case len(v) == 0:
		return KindString
	case !json.Valid(v):
		return KindAny
	}
	switch v[0] {
	case '"':
		return KindString
	case '{':
		return KindObject
	case '[':
		return KindArray
	case 't', 'f':
		return KindBoolean
	case 'n':
		return KindAny
	}
	if bytes.ContainsAny(v, ".eE") {
		return KindNumber
	}
	return KindInteger
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package schemadraft

import (
	"fmt"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// recipeOnlyMatch is what a Recipe drafted for a catalog Component found of
// that Component in the type it realizes.
type recipeOnlyMatch struct {
	// Matched are the Component's values keys a property carries the name of,
	// and Unmatched the others, each in the Component's order.
	Matched, Unmatched []string
	// Required are the vendor names of the properties the vendor requires that
	// no values key matches, read from infra.overrides, in the vendor's order.
	Required []string
}

// recipeOnlyResource is the resource a Recipe implementing the catalog
// Component c writes for the type t, whose properties placed holds as Split
// placed them, and the rule of each input in the inputs' order.
//
// Each values key of c equal, both snake-cased, to the name of a writable
// property that is not a secret is read from values, the first such property in
// the vendor's order taking it. Every other writable property that is not a
// secret is read from infra.overrides under its vendor name, since the draft
// does not see c's infra; one the vendor requires is required there. A secret
// is read as Split placed it. Each provides key of c equal, both snake-cased,
// to the joined path of a scalar read back is published from that attribute;
// every other is unmapped, offered the attributes read back that no key maps.
func recipeOnlyResource(t *Type, placed []Placed, c aggrega.CatalogComponent) (Resource, []string, recipeOnlyMatch) {
	var m recipeOnlyMatch
	byProperty := map[*Property]string{}
	for _, key := range c.Values {
		i := slices.IndexFunc(placed, func(pl Placed) bool {
			_, taken := byProperty[pl.Property]
			return !taken && writable(pl.Pillar) && SnakeKey(pl.Property.Name) == SnakeKey(key)
		})
		if i < 0 {
			m.Unmatched = append(m.Unmatched, key)
			continue
		}
		byProperty[placed[i].Property] = key
		m.Matched = append(m.Matched, key)
	}

	r := Resource{Type: t}
	var rules []string
	var readBack []Placed
	for _, pl := range placed {
		p := pl.Property
		switch {
		case pl.Pillar == PillarSecret:
			r.Inputs = append(r.Inputs, Input{Property: p, Source: InputSource{Pillar: PillarSecret, Key: pl.Key},
				Required: pl.Required, Secret: true})
			rules = append(rules, pl.Rule)
		case writable(pl.Pillar):
			if key, ok := byProperty[p]; ok {
				r.Inputs = append(r.Inputs, Input{Property: p, Source: InputSource{Pillar: PillarValues, Key: key},
					Required: p.Required})
				rules = append(rules, noted(fmt.Sprintf("matched by name to the values key %s of %s", key, c.Name),
					false, p.Deprecated, ""))
				continue
			}
			rule := ruleOverride
			if p.Required {
				rule = fmt.Sprintf("required: the vendor schema requires it and %s declares no values key of its "+
					"name, so the Release sets it under infra.overrides", c.Name)
				m.Required = append(m.Required, p.Name)
			}
			r.Inputs = append(r.Inputs, Input{Property: p, Source: InputSource{Pillar: PillarOverrides, Key: p.Name},
				Required: p.Required})
			rules = append(rules, noted(rule, p.CreateOnly, p.Deprecated, ""))
		case pl.Pillar == PillarProvides:
			readBack = append(readBack, pl)
		}
	}

	mapped := map[*Property]bool{}
	for _, key := range c.Provides {
		i := slices.IndexFunc(readBack, func(pl Placed) bool {
			return !mapped[pl.Property] && SnakeKey(strings.Join(attributePath(pl.Property), ".")) == SnakeKey(key)
		})
		if i < 0 {
			r.Unmapped = append(r.Unmapped, Unmapped{Key: key})
			continue
		}
		mapped[readBack[i].Property] = true
		r.Provides = append(r.Provides, provideOf(t, key, readBack[i].Property))
	}
	var candidates []string
	for _, pl := range readBack {
		if !mapped[pl.Property] {
			candidates = append(candidates, strings.Join(attributePath(pl.Property), "."))
		}
	}
	for i := range r.Unmapped {
		r.Unmapped[i].Candidates = candidates
	}
	return r, rules, m
}

// writable reports whether a property placed in pillar is one a Recipe writes
// from a pillar of the Component: values, infra or infra.overrides.
func writable(pillar Pillar) bool {
	return pillar == PillarValues || pillar == PillarInfra || pillar == PillarOverrides
}

// providesUnmapped warns of a provides key of the catalog Component component
// no attribute of t read back matches.
func providesUnmapped(component string, t *Type, u Unmapped) aggrega.Diagnostic {
	offer := typeName(t) + " reads back no attribute it could be mapped to"
	if len(u.Candidates) > 0 {
		offer = "map it to an attribute " + typeName(t) + " reads back, one of " + strings.Join(u.Candidates, ", ")
	}
	return aggrega.Diagnostic{
		Severity: "warn",
		Code:     codeProvidesUnmapped,
		Message: fmt.Sprintf("the Component %s provides %s, and no attribute %s reads back carries its name, so the "+
			"drafted Recipe's provides.%s halts the build until it is mapped: %s", component, u.Key, typeName(t),
			u.Key, offer),
		SourceRef: t.Source,
	}
}

// componentDraftedNew warns that the Component resource_types names for t is
// one the catalog the import reads does not hold, so it is drafted new.
func componentDraftedNew(component string, t *Type) aggrega.Diagnostic {
	return aggrega.Diagnostic{
		Severity: "warn",
		Code:     codeComponentDraftedNew,
		Message: fmt.Sprintf("the catalog the import reads holds no Component %s, which resource_types names for %s, "+
			"so the import drafts it new under that name; where a registry the workspace pulls from publishes a "+
			"Component of that name, this draft takes its place in this workspace", component, typeName(t)),
		SourceRef: t.Source,
	}
}

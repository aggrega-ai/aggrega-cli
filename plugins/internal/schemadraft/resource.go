// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package schemadraft

import (
	"slices"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/alphanumeric"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// InputSource is where a Recipe reads one input of its resource from.
type InputSource struct {
	// Pillar is PillarValues, PillarInfra, PillarOverrides, PillarSecret or
	// PillarInject.
	Pillar Pillar
	// Key is the snake_case key under values, infra or a secret's slot, the
	// vendor's name under infra.overrides, and the key on the inject pipe.
	Key string
	// Mechanism is the member of .context.inject an inject input is read from.
	Mechanism string
}

// Template is the template expression reading the input, always through index,
// so an input that is absent halts naming the key rather than reading as empty.
// A secret reads the value the attachment on its featuresSchema slot delivers on
// the env mechanism, under the key the attachment's values.key names; a format
// that declares a secret as a deploy-time parameter reads that parameter and
// does not read this. A pillar no input is read from gives no expression.
func (s InputSource) Template() string {
	key := strconv.Quote(s.Key)
	switch s.Pillar {
	case PillarValues:
		return "(index .values " + key + ")"
	case PillarInfra:
		return "(index .infra " + key + ")"
	case PillarOverrides:
		return "(index .infra " + strconv.Quote(overridesKey) + " " + key + ")"
	case PillarSecret:
		return "(index (optional (default dict .context.inject) " + strconv.Quote(secretInject) + " dict) " +
			"(index .features " + key + ` "values" "key"))`
	case PillarInject:
		return "(index (default dict .context.inject) " + strconv.Quote(s.Mechanism) + " " + key + ")"
	}
	return ""
}

// Present is the template expression testing that the input is set, with
// hasKey, so an input set to false, 0 or "" is present: for a secret, that an
// attachment fills its slot. It gates Template for an input that is not
// required. A pillar no input is read from gives no expression.
func (s InputSource) Present() string {
	key := strconv.Quote(s.Key)
	switch s.Pillar {
	case PillarValues:
		return "(hasKey .values " + key + ")"
	case PillarInfra:
		return "(hasKey .infra " + key + ")"
	case PillarOverrides:
		return "(hasKey (optional .infra " + strconv.Quote(overridesKey) + " dict) " + key + ")"
	case PillarSecret:
		return `(hasKey (optional . "features" dict) ` + key + ")"
	case PillarInject:
		return "(hasKey (optional (default dict .context.inject) " + strconv.Quote(s.Mechanism) + " dict) " + key + ")"
	}
	return ""
}

// Input is one property of a resource a Recipe writes, and where it reads it.
type Input struct {
	Property *Property
	Source   InputSource
	// Required is set for an input the Recipe always writes; one that is not is
	// written only where Present holds.
	Required bool
	// Secret is set for a secret, which a Recipe never writes into a file it
	// renders as plain text.
	Secret bool
}

// Provide is one key a Recipe publishes from an attribute the vendor reads back.
type Provide struct {
	Key string
	// Attribute is the vendor path of the attribute read back.
	Attribute []string
	// Identifier is set where the attribute is the resource's own reference: the
	// one path of the type's primary identifier.
	Identifier bool
}

// Unmapped is a provides key of a catalog Component no attribute the vendor
// reads back matches by name. Candidates are the vendor paths, dotted, of the
// attributes read back that no other key maps.
type Unmapped struct {
	Key        string
	Candidates []string
}

// Value is the provides value a writer writes for u, a resource of the type t:
// a fail naming the key, the type and the candidates, so a build halts on it
// until an author maps the key to an attribute. A key present with an empty
// value would pass the provides check silently.
func (u Unmapped) Value(t *Type) string {
	msg := "provides." + u.Key + " is not mapped to an attribute of " + typeName(t) + ": "
	if len(u.Candidates) > 0 {
		msg += strings.Join(u.Candidates, ", ")
	} else {
		msg += "it reads back none"
	}
	return "{{ fail " + strconv.Quote(msg) + " }}"
}

// Resource is what a target writer drafts a Recipe body from: the type, the
// inputs the Recipe writes, in the vendor's order, the keys it publishes, and on
// a Recipe drafted for a catalog Component, the provides keys of that Component
// no attribute matches.
type Resource struct {
	Type     *Type
	Inputs   []Input
	Provides []Provide
	Unmapped []Unmapped
}

// Writer returns the target parts of a Recipe draft — Locals, Provides,
// Artifacts, BoundaryArtifacts, Source, ValuesMapping, Files, and the target's
// own Assertions and Header lines — for the resource r, with what it reports
// about writing it. The core sets the Recipe's name, version, description,
// capability and implements, opens the header with the keys the Recipe reads,
// and writes the assertions every target shares before the writer's own, which
// take names other than theirs: overrides_this_recipe_reads,
// required_overrides_set and secrets_are_parameters.
type Writer func(r Resource) (importer.RecipeDraft, []aggrega.Diagnostic)

// SecretParameter is what follows the instance's name in the name of the
// deploy-time parameter a Recipe declares for the secret p, on a format that
// declares secrets as parameters: p's name in capitalized words, so the
// parameter's whole name, the instance's name in capitalized words followed by
// it, holds letters and digits alone.
func SecretParameter(p *Property) string {
	return alphanumeric.CapitalizedWords(p.Name)
}

// resourceFor is the resource a Recipe writes for the type t, whose properties
// placed holds as Split placed them: one input per property placed in values,
// infra, infra.overrides or as a secret, and one provide per scalar read back,
// each in the order placed holds it. It returns beside it the rule of each
// input, in the inputs' order.
func resourceFor(t *Type, placed []Placed) (Resource, []string) {
	r := Resource{Type: t}
	var rules []string
	for _, pl := range placed {
		switch pl.Pillar {
		case PillarValues, PillarInfra, PillarOverrides, PillarSecret:
			r.Inputs = append(r.Inputs, Input{Property: pl.Property, Source: InputSource{Pillar: pl.Pillar, Key: pl.Key},
				Required: pl.Required, Secret: pl.Pillar == PillarSecret})
			rules = append(rules, pl.Rule)
		case PillarProvides:
			r.Provides = append(r.Provides, provideOf(t, pl.Key, pl.Property))
		}
	}
	return r, rules
}

// provideOf publishes the attribute p of t, read back, under key.
func provideOf(t *Type, key string, p *Property) Provide {
	path := attributePath(p)
	identifier := len(t.PrimaryIdentifier) == 1 && slices.Equal(t.PrimaryIdentifier[0], path)
	return Provide{Key: key, Attribute: path, Identifier: identifier}
}

// attributePath is the vendor path of p from the resource body, or its name
// where the reader gives no path.
func attributePath(p *Property) []string {
	if len(p.Path) == 0 {
		return []string{p.Name}
	}
	return slices.Clone(p.Path)
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package composeimport

import (
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/draftyaml"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// rowsByRepository is the image table keyed by repository, read by key alone.
var rowsByRepository = func() map[string]imageRow {
	out := make(map[string]imageRow, len(imageTable))
	for _, r := range imageTable {
		out[r.Repository] = r
	}
	return out
}()

// assigned is one value a service's source sets that the draft writes as a value
// of its Component: the value's key, and the source's text.
type assigned struct {
	key, text string
	// variable is the environment variable the source sets it under.
	variable string
}

// candidate is one producer a consumer's variable may be read from: the
// producer, and the provides key publishing the value the variable holds.
type candidate struct {
	producer *service
	from     string
	// variable is the producer's own variable holding that value.
	variable string
}

// translate turns what each service's environment and image say into the facts
// the drafts write, project by project, for the import's target. A service is
// read against the image table where its decision references the Component its
// image's row names and the workspace's catalog holds it; a value is read as the
// source writes it, before the credential decision, so a variable's name and a
// match between two services' values are read whatever the decision writes:
//
//   - its image's tag is the infra key the row's Tag names;
//   - each variable the row's Values names whose value holds text, not a variable
//     reference, is that value of the Component, where the catalog's Component
//     declares it;
//   - each variable the row's Secrets names whose value is one variable reference
//     is a secret attached on that slot, read from the variable the reference
//     names, its key the variable the Recipes read it under;
//
// and, for every service, each variable of its environment not so read:
//
//   - a value that is one variable reference naming a variable a secret of the
//     project is read from is, on an instance of Server, a secret of its own under
//     the variable's name, where the target writes it as a value the Server takes;
//   - a value that is the name of another service of the project, whose row names
//     the provides key publishing its name, draws a connection to that service
//     delivering the variable from that key;
//   - a value equal to a value another service of the project sets for a value of
//     its Component that the Component publishes draws a connection delivering
//     the variable from that provides key. Where several services or keys hold
//     the value, the one whose key, or whose own variable, ends as the variable's
//     name ends is read; and, failing that, the one the service's other variables
//     have not already been read from; otherwise the variable is not read.
//
// A variable read as none of these is left in the comment above the service's
// entry in the Release, as the source writes it.
func translate(in aggrega.ImportInput, read []*project, types map[*service]string, referenced map[*service]bool) {
	for _, p := range read {
		for _, s := range p.services {
			s.translated = map[string]bool{}
			s.inject = map[string][]draftyaml.Injected{}
			repository, tag, ok := Reference(s.image)
			row, listed := rowsByRepository[repository]
			if !ok || !listed || !referenced[s] || types[s] != row.Component {
				continue
			}
			held := catalogComponent(in.Catalog, row.Component)
			if held == nil {
				continue
			}
			s.row, s.held = &row, held
			if row.Tag != "" {
				s.tag = tag
			}
			for _, e := range s.env {
				if e.value == nil {
					continue
				}
				if key, ok := row.Values[e.name]; ok && slices.Contains(held.Values, key) && !isReference(*e.value) {
					s.values = append(s.values, assigned{key: key, text: *e.value, variable: e.name})
					s.translated[e.name] = true
					continue
				}
				if slot, ok := row.Secrets[e.name]; ok {
					if variable := referenceName(*e.value); variable != "" {
						s.secrets = append(s.secrets, importer.Secret{Path: "features." + slot, Variable: variable,
							Key: e.name, Text: "the " + slot + " of " + s.key + ", which the source sets as " + e.name})
						s.translated[e.name] = true
					}
				}
			}
		}
		for _, s := range p.services {
			connect(in.Target, p, s, types[s])
		}
	}
}

// connect reads the variables of s that name another service of p, or a value
// another service publishes, and the references to a variable a secret of p is
// read from, as translate describes.
func connect(target string, p *project, s *service, typ string) {
	used := map[candidate]bool{}
	var deferred []envVar
	for _, e := range s.env {
		if e.value == nil || s.translated[e.name] {
			continue
		}
		v := *e.value
		if variable := referenceName(v); variable != "" {
			if typ == Server && secretVariable(p, variable) {
				secret := importer.Secret{Path: "features." + slotName(e.name), Variable: variable, Key: e.name,
					Text: "the value " + s.key + " reads as " + e.name}
				if !secret.Form(target).ByName {
					s.secrets = append(s.secrets, secret)
					s.translated[e.name] = true
				}
			}
			continue
		}
		if producer := serviceNamed(p, s, v); producer != nil {
			s.deliver(e.name, candidate{producer: producer, from: producer.row.Host})
			continue
		}
		cands := candidates(p, s, v)
		if c, ok := byName(e.name, cands); ok {
			s.deliver(e.name, c)
			used[keyOf(c)] = true
			continue
		}
		if len(cands) > 1 {
			deferred = append(deferred, e)
		}
	}
	for _, e := range deferred {
		var left []candidate
		for _, c := range candidates(p, s, *e.value) {
			if !used[keyOf(c)] {
				left = append(left, c)
			}
		}
		if len(distinct(left)) == 1 {
			s.deliver(e.name, left[0])
			used[keyOf(left[0])] = true
		}
	}
}

// deliver records that the connection from s to c's producer delivers the
// variable name from c's provides key.
func (s *service) deliver(name string, c candidate) {
	s.inject[c.producer.key] = append(s.inject[c.producer.key], draftyaml.Injected{In: name, From: c.from})
	s.translated[name] = true
}

// keyOf is a candidate as a consumer's other variables are told apart from it:
// its producer and its provides key.
func keyOf(c candidate) candidate {
	return candidate{producer: c.producer, from: c.from}
}

// distinct returns the candidates, each producer and provides key once.
func distinct(cands []candidate) []candidate {
	var out []candidate
	for _, c := range cands {
		if !slices.ContainsFunc(out, func(o candidate) bool { return keyOf(o) == keyOf(c) }) {
			out = append(out, keyOf(c))
		}
	}
	return out
}

// byName returns the one candidate a variable of a consumer is read from: the
// only one, or, of several, the only one whose provides key, or whose own
// variable, ends as name ends — the segment after its last underscore, compared
// without regard to case.
func byName(name string, cands []candidate) (candidate, bool) {
	all := distinct(cands)
	if len(all) == 1 {
		return all[0], true
	}
	last := func(s string) string { return strings.ToLower(s[strings.LastIndexByte(s, '_')+1:]) }
	var named []candidate
	for _, c := range cands {
		if last(c.from) == last(name) || last(c.variable) == last(name) {
			named = append(named, c)
		}
	}
	if named = distinct(named); len(named) == 1 {
		return named[0], true
	}
	return candidate{}, false
}

// candidates returns every producer of p, other than s, whose source sets value
// for a value of its Component the Component publishes, with that provides key.
func candidates(p *project, s *service, value string) []candidate {
	var out []candidate
	for _, o := range p.services {
		if o == s || o.row == nil {
			continue
		}
		for _, a := range o.values {
			if a.text != value {
				continue
			}
			if from, ok := o.row.Provides[a.key]; ok && slices.Contains(o.held.Provides, from) {
				out = append(out, candidate{producer: o, from: from, variable: a.variable})
			}
		}
	}
	return out
}

// serviceNamed returns the service of p, other than s, named value, where its
// row names the provides key publishing its name and its Component declares it;
// nil otherwise.
func serviceNamed(p *project, s *service, value string) *service {
	for _, o := range p.services {
		if o != s && o.name == value && o.row != nil && o.row.Host != "" && slices.Contains(o.held.Provides, o.row.Host) {
			return o
		}
	}
	return nil
}

// secretVariable reports whether a secret of a service of p is read from the
// variable named variable.
func secretVariable(p *project, variable string) bool {
	for _, o := range p.services {
		if slices.ContainsFunc(o.secrets, func(s importer.Secret) bool { return s.Variable == variable }) {
			return true
		}
	}
	return false
}

// slotName is the slot a Server's secret is attached on: the variable's name in
// lower case, each character other than a letter or a digit an underscore.
func slotName(variable string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r - 'A' + 'a'
		}
		return '_'
	}, variable)
}

// referenceName returns the variable a value that is one variable reference and
// nothing else names — ${NAME} or $NAME — and "" for any other value.
func referenceName(text string) string {
	if !isReference(text) {
		return ""
	}
	return strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(text, "$"), "{"), "}")
}

// catalogComponent returns the Component the catalog view an import is handed
// holds under name, nil where it holds none.
func catalogComponent(catalog *aggrega.CatalogProjection, name string) *aggrega.CatalogComponent {
	if catalog == nil {
		return nil
	}
	for i := range catalog.Components {
		if catalog.Components[i].Name == name {
			return &catalog.Components[i]
		}
	}
	return nil
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"strings"
)

// documentStage is the stage presenting one authored document rather than a
// compilation, and it is the only stage the carrier axis applies at.
//
// It is spelled here rather than read off the chain's root, because the axis
// belongs to what a stage HOLDS and not to where it sits. A stage presenting an
// authored document has two axes — when a fact is complete, and which kind of
// document carries it — while every later stage presents one compilation, which
// has one carrier and so nothing to distinguish. Reading the root instead would
// attach the axis to whichever stage happened to be first, which is a different
// claim that happens to agree today.
//
// A spelled name can be wrong, so deriveCarriers refuses a registry declaring no
// stage of this name rather than silently treating every fact as a later-stage
// one.
const documentStage = "document"

// authoredTypePrefix and authoredSpecSuffix bracket the carrier type a
// document-stage fact names when its document kind is derivable: the package the
// authored entity types are declared in, and the suffix each entity's spec type
// carries. What sits between them is the kind.
const (
	authoredTypePrefix = "types."
	authoredSpecSuffix = "Spec"
)

// carrierGroup is one authored document kind together with the document-stage
// facts that kind carries, in the order the registry declares them.
//
// The order is the registry's rather than sorted, for the reason every derived
// order here is: a generated artifact is byte-compared against its committed
// copy, so an order that is a property of the declarations reproduces and one
// that is a property of a map does not.
type carrierGroup struct {
	// Kind is the authored document kind — the name a user writes as the
	// document's kind.
	Kind string

	// Facts are the document-stage facts that kind carries.
	Facts []factDecl
}

// deriveCarrier reports the authored document kind a fact's source carrier
// names, and whether it names one at all.
//
// It reads the text before the last dot — the carrier type — and yields what
// sits between the authored types package and the spec suffix. A carrier that is
// not one of those spec types yields nothing and the fact declares its kind
// instead: a source carried on a resolver input, or on a structure nested inside
// an entity's spec, names no kind anywhere in its own text, so no wider
// derivation would find one and a wider one would only guess.
func deriveCarrier(sourceField string) (string, bool) {
	at := strings.LastIndex(sourceField, ".")
	if at < 0 {
		return "", false
	}
	carrierType := sourceField[:at]
	if !strings.HasPrefix(carrierType, authoredTypePrefix) ||
		!strings.HasSuffix(carrierType, authoredSpecSuffix) {
		return "", false
	}
	kind := strings.TrimSuffix(strings.TrimPrefix(carrierType, authoredTypePrefix), authoredSpecSuffix)
	if kind == "" {
		return "", false
	}
	return kind, true
}

// deriveCarriers resolves every document-stage fact's authored document kind and
// groups the facts by it.
//
// It takes the fact and stage sets as arguments rather than reading the registry,
// so that the refusals below can be shown to fire: a refusal nothing exercises is
// indistinguishable from a refusal that never triggers, and each of these guards
// a declaration a reviewer is meant to trust.
//
// Four shapes are refused, and none of them is available to the language.
//
// A carrier declared at a stage other than the document stage, because that
// stage's presentation is one compilation and a second axis over it would name a
// distinction that does not exist.
//
// A document-stage fact naming no kind at all — neither derived nor declared —
// because an omission and a deliberate value must not read the same to a
// reviewer, and a fact with no kind is a fact no implementation can be held to.
//
// A declaration disagreeing with the derivation, refused rather than settled by
// precedence: a precedence rule would make one of the two silently inert, and
// which one is inert is exactly what a reader would then have to know.
//
// And a declaration repeating what the derivation already yields, because a
// carrier with two homes is a carrier that can drift apart, and the agreement is
// what hides the drift until the day one of them moves.
func deriveCarriers(fs []factDecl, ss []stageDecl) ([]carrierGroup, error) {
	stageDeclared := false
	for _, s := range ss {
		if s.Name == documentStage {
			stageDeclared = true
			break
		}
	}
	if !stageDeclared {
		return nil, fmt.Errorf(
			"no stage named %q is declared, so the carrier axis has no stage to apply at; the axis "+
				"belongs to the stage presenting one authored document, so renaming that stage is a "+
				"rename here too", documentStage)
	}

	var out []carrierGroup
	index := make(map[string]int)
	for _, f := range fs {
		if f.Stage != documentStage {
			if f.Carrier != "" {
				return nil, fmt.Errorf(
					"fact %q declares carrier %q at stage %q, and the carrier axis applies at the %q "+
						"stage alone; every later stage presents one compilation, which has one "+
						"carrier and so nothing for a declaration to distinguish",
					f.Name, f.Carrier, f.Stage, documentStage)
			}
			continue
		}

		derived, derivable := deriveCarrier(f.SourceField)
		switch {
		case derivable && f.Carrier == derived:
			return nil, fmt.Errorf(
				"fact %q declares carrier %q, which its source carrier %q already derives; a carrier "+
					"is declared only where the derivation yields nothing, so that each fact's kind "+
					"has one home and the two cannot drift apart",
				f.Name, f.Carrier, f.SourceField)
		case derivable && f.Carrier != "":
			return nil, fmt.Errorf(
				"fact %q declares carrier %q while its source carrier %q derives %q; one of the two "+
					"is wrong and the declaration cannot settle which, so the disagreement is refused "+
					"rather than resolved by a precedence a reader would have to know",
				f.Name, f.Carrier, f.SourceField, derived)
		case !derivable && f.Carrier == "":
			return nil, fmt.Errorf(
				"fact %q is complete at the %q stage and names no authored kind: its source carrier "+
					"%q derives none, and none is declared. Declare Carrier — an omission and a "+
					"deliberate value must not read the same to a reviewer, and a fact whose kind is "+
					"unknown is one no implementation can be held to",
				f.Name, documentStage, f.SourceField)
		}

		kind := derived
		if !derivable {
			if err := validateCarrierShape(f); err != nil {
				return nil, err
			}
			kind = f.Carrier
		}

		if i, seen := index[kind]; seen {
			out[i].Facts = append(out[i].Facts, f)
			continue
		}
		index[kind] = len(out)
		out = append(out, carrierGroup{Kind: kind, Facts: []factDecl{f}})
	}
	return out, nil
}

// validateCarrierShape checks that a declared carrier reads as a document kind.
//
// The check is shape and nothing more. The kind is the name a user writes as the
// document's kind, so it is one capitalised word and never a Go type path — and
// a carrier mistakenly declared as the carrier TYPE, which is the shape the
// derivation reads, would render a Go identifier into a sentence naming an
// authored document. That mistake is invisible in the declaration itself, which
// is why it is refused here rather than left to a reader of the output.
func validateCarrierShape(f factDecl) error {
	if strings.ContainsAny(f.Carrier, "./ \t") {
		return fmt.Errorf(
			"fact %q declares carrier %q, which is not a document kind; the kind is the name written "+
				"as the document's own kind, so it carries no package qualifier, no field path and "+
				"no space", f.Name, f.Carrier)
	}
	if c := f.Carrier[0]; c < 'A' || c > 'Z' {
		return fmt.Errorf(
			"fact %q declares carrier %q, which is not a document kind; the kind is written as the "+
				"author writes it, capitalised", f.Name, f.Carrier)
	}
	return nil
}

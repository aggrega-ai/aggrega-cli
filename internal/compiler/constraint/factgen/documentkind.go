// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"strings"
)

// documentStage is the stage whose presentation VARIES BY AUTHORED DOCUMENT
// KIND, and it is the only stage the document-kind axis applies at.
//
// It is spelled here rather than read off the chain's root, because the axis
// belongs to what a stage HOLDS and not to where it sits. This stage presents
// one authored document of whichever kind a caller hands over, so it has two
// axes — when a fact is complete, and which kind of document carries it. Every
// other stage has a SINGLE document kind and so nothing for a second axis to
// distinguish, and that holds for two different reasons rather than one: a stage
// presenting one compilation has one kind because a compilation is one thing,
// and a stage presenting one authored document of a FIXED kind has one because
// the stage itself names the kind. Reading the root instead would attach
// the axis to whichever stage happened to be first, which is a different claim
// that happens to agree today.
//
// 🔴 The second of those reasons is a real stage and not a hypothetical: the
// catalog stage holds one authored Manifest beside the catalog it resolves
// against. It is single-kind, so it needs no declaration and makes none — and
// the refusal below cannot tell that from a stage that needs one and forgot,
// because it keys on a NON-EMPTY document kind. The registry's declaration of
// that stage carries what the first fact of a second kind there would owe.
//
// A spelled name can be wrong, so deriveDocumentKinds refuses a registry declaring no
// stage of this name rather than silently treating every fact as a later-stage
// one.
const documentStage = "document"

// authoredTypePrefix and authoredSpecSuffix bracket the source structure a
// document-stage fact names when its document kind is derivable: the package the
// authored entity types are declared in, and the suffix each entity's spec type
// carries. What sits between them is the kind.
const (
	authoredTypePrefix = "types."
	authoredSpecSuffix = "Spec"
)

// documentKindGroup is one authored document kind together with the document-stage
// facts that kind carries, in the order the registry declares them.
//
// The order is the registry's rather than sorted, for the reason every derived
// order here is: a generated artifact is byte-compared against its committed
// copy, so an order that is a property of the declarations reproduces and one
// that is a property of a map does not.
type documentKindGroup struct {
	// Kind is the authored document kind — the name a user writes as the
	// document's kind.
	Kind string

	// Facts are the document-stage facts that kind carries.
	Facts []factDecl
}

// deriveDocumentKind reports the authored document kind a fact's source field
// names, and whether it names one at all.
//
// It reads the text before the last dot — the source structure — and yields what
// sits between the authored types package and the spec suffix. A structure that
// is not one of those spec types yields nothing and the fact declares its kind
// instead: a source carried on a resolver input, or on a structure nested inside
// an entity's spec, names no kind anywhere in its own text, so no wider
// derivation would find one and a wider one would only guess.
func deriveDocumentKind(sourceField string) (string, bool) {
	at := strings.LastIndex(sourceField, ".")
	if at < 0 {
		return "", false
	}
	sourceType := sourceField[:at]
	if !strings.HasPrefix(sourceType, authoredTypePrefix) ||
		!strings.HasSuffix(sourceType, authoredSpecSuffix) {
		return "", false
	}
	kind := strings.TrimSuffix(strings.TrimPrefix(sourceType, authoredTypePrefix), authoredSpecSuffix)
	if kind == "" {
		return "", false
	}
	return kind, true
}

// deriveDocumentKinds resolves every document-stage fact's authored document kind and
// groups the facts by it.
//
// It takes the fact and stage sets as arguments rather than reading the registry,
// so that the refusals below can be shown to fire: a refusal nothing exercises is
// indistinguishable from a refusal that never triggers, and each of these guards
// a declaration a reviewer is meant to trust.
//
// Four shapes are refused, and none of them is available to the language.
//
// A document kind declared at a stage other than the document stage, because
// such a stage has a single kind — a compilation, or one authored document of
// the kind the stage itself fixes — and a second axis over it would name a
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
// kind with two homes is a kind that can drift apart, and the agreement is
// what hides the drift until the day one of them moves.
func deriveDocumentKinds(fs []factDecl, ss []stageDecl) ([]documentKindGroup, error) {
	stageDeclared := false
	for _, s := range ss {
		if s.Name == documentStage {
			stageDeclared = true
			break
		}
	}
	if !stageDeclared {
		return nil, fmt.Errorf(
			"no stage named %q is declared, so the document-kind axis has no stage to apply at; the axis "+
				"belongs to the stage presenting one authored document, so renaming that stage is a "+
				"rename here too", documentStage)
	}

	var out []documentKindGroup
	index := make(map[string]int)
	for _, f := range fs {
		if f.Stage != documentStage {
			if f.DocumentKind != "" {
				return nil, fmt.Errorf(
					"fact %q declares document kind %q at stage %q, and the document-kind axis applies at the %q "+
						"stage alone, because that is the only stage whose view varies by "+
						"authored document kind; every other stage has a single kind and so "+
						"nothing for a declaration to distinguish",
					f.Name, f.DocumentKind, f.Stage, documentStage)
			}
			continue
		}

		derived, derivable := deriveDocumentKind(f.SourceField)
		switch {
		case derivable && f.DocumentKind == derived:
			return nil, fmt.Errorf(
				"fact %q declares document kind %q, which its source field %q already derives; a kind "+
					"is declared only where the derivation yields nothing, so that each fact's kind "+
					"has one home and the two cannot drift apart",
				f.Name, f.DocumentKind, f.SourceField)
		case derivable && f.DocumentKind != "":
			return nil, fmt.Errorf(
				"fact %q declares document kind %q while its source field %q derives %q; one of the two "+
					"is wrong and the declaration cannot settle which, so the disagreement is refused "+
					"rather than resolved by a precedence a reader would have to know",
				f.Name, f.DocumentKind, f.SourceField, derived)
		case !derivable && f.DocumentKind == "":
			return nil, fmt.Errorf(
				"fact %q is complete at the %q stage and names no authored kind: its source field "+
					"%q derives none, and none is declared. Declare DocumentKind — an omission and a "+
					"deliberate value must not read the same to a reviewer, and a fact whose kind is "+
					"unknown is one no implementation can be held to",
				f.Name, documentStage, f.SourceField)
		}

		kind := derived
		if !derivable {
			if err := validateDocumentKindShape(f); err != nil {
				return nil, err
			}
			kind = f.DocumentKind
		}

		if i, seen := index[kind]; seen {
			out[i].Facts = append(out[i].Facts, f)
			continue
		}
		index[kind] = len(out)
		out = append(out, documentKindGroup{Kind: kind, Facts: []factDecl{f}})
	}
	return out, nil
}

// validateDocumentKindShape checks that a declared kind reads as a document kind.
//
// The check is shape and nothing more. The kind is the name a user writes as the
// document's kind, so it is one capitalised word and never a Go type path — and
// a kind mistakenly declared as the SOURCE STRUCTURE, which is the shape the
// derivation reads, would render a Go identifier into a sentence naming an
// authored document. That mistake is invisible in the declaration itself, which
// is why it is refused here rather than left to a reader of the output.
func validateDocumentKindShape(f factDecl) error {
	if strings.ContainsAny(f.DocumentKind, "./ \t") {
		return fmt.Errorf(
			"fact %q declares document kind %q, which is not a document kind; the kind is the name written "+
				"as the document's own kind, so it carries no package qualifier, no field path and "+
				"no space", f.Name, f.DocumentKind)
	}
	if c := f.DocumentKind[0]; c < 'A' || c > 'Z' {
		return fmt.Errorf(
			"fact %q declares document kind %q, which is not a document kind; the kind is written as the "+
				"author writes it, capitalised", f.Name, f.DocumentKind)
	}
	return nil
}

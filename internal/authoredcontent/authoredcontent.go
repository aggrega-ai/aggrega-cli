// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package authoredcontent carries the rule that decides who writes what a
// capability produces, and the one delivery mode that follows from it.
//
// The rule: authored content is reported; machine state is written.
//
// A capability whose product is content an author will own and go on editing
// carries a reporting delivery, which hands back the content the write would
// have produced and writes nothing — so a caller holding its own document model
// puts that content into it, where the author sees it before it is saved, can
// amend it, and can undo it. A capability whose product is state the machine
// keeps about itself writes directly, because nobody is going to edit it and
// there is no reason to route it through an author.
//
// It is a rule rather than a pair of exceptions, and that matters for the next
// capability someone adds. A carve-out list has to be extended by whoever adds
// that capability, and they will extend it by copying whichever entry looks
// nearest. A rule classifies it for them: ask whether the product is content the
// author will own, or state the machine keeps about itself.
//
// The line is drawn on the CONTENT, never on the file. Machine state written
// into a document an author owns is still machine state — a plugin's
// machine-owned records live inside that plugin's own entity file, and they
// belong on the written side even though the file around them is authored.
// Classified by the file, they would be handed to a reader to edit, which is
// exactly wrong for records the machine maintains. That case is the one that
// makes the rule worth stating rather than enumerating.
//
// The writing path is never removed when a reporting delivery is added. A caller
// at a terminal has no document model and no undo history, so reporting content
// to it would mean printing a file for the reader to save by hand, which is
// worse than writing it. The delivery is ADDED, not substituted.
package authoredcontent

import (
	"fmt"
	"io"

	"go.yaml.in/yaml/v3"
)

// Delivery says who writes a capability's product.
//
// DeliveryWrite is the zero value on purpose: a caller that states no delivery
// writes, so adding this type to a capability's options cannot change what an
// existing caller does. That is the mechanical form of "the delivery is added,
// not substituted".
type Delivery int

const (
	// DeliveryWrite puts the product on disk. It is what every capability did
	// before a reporting delivery existed and what a caller at a terminal still
	// wants, since a terminal has nowhere else to put a file.
	DeliveryWrite Delivery = iota

	// DeliveryReport returns the content the write would have produced and writes
	// nothing at all, leaving where it lands to the caller.
	DeliveryReport
)

// Reports reports whether the delivery returns content instead of writing it.
//
// Every branch reads the delivery through this predicate rather than comparing
// against the constant, so there is one spelling of the question and a third
// delivery added later has one place to be admitted.
func (d Delivery) Reports() bool { return d == DeliveryReport }

// DeliveryFor maps a caller's request to be reported to rather than written for
// onto the delivery it selects.
//
// Every surface that offers the mode routes through here rather than writing the
// two-line conditional itself. A capability's surface is where the request
// arrives, so without this each surface would carry its own copy of the mapping,
// and the copies would agree until one of them was the only place a third
// delivery got admitted.
func DeliveryFor(report bool) Delivery {
	if report {
		return DeliveryReport
	}
	return DeliveryWrite
}

// Document is one file's worth of content and the file it is for.
//
// The path travels WITH its content rather than in a separate channel, because
// a caller that received content and a list of paths separately would have to
// correlate them, and a capability producing several documents gives it nothing
// to correlate on. A report that did not say which file each document was for
// would let a caller write a machine-wide configuration into a workspace.
type Document struct {
	Path    string `yaml:"path"`
	Content string `yaml:"content"`
}

// report is the reported form's root: a document set under one key, so the
// answer is one parseable document however many files it describes.
type report struct {
	Documents []Document `yaml:"documents"`
}

// Report writes docs to out in the reported form — one YAML document carrying a
// documents list, each entry the path a document is for and that document's
// content verbatim.
//
// The form is YAML rather than a bespoke framing so a caller parses it with the
// parser it already has, and so content that itself begins with a comment or a
// document marker needs no escaping convention invented for it. The order is the
// order the capability produced the documents in, so the same inputs report the
// same bytes.
//
// A capability reporting nothing still reports the empty set rather than an
// empty stream: a caller can tell "no documents" from a truncated answer.
func Report(out io.Writer, docs []Document) error {
	if docs == nil {
		docs = []Document{}
	}
	enc := yaml.NewEncoder(out)
	enc.SetIndent(2)
	if err := enc.Encode(report{Documents: docs}); err != nil {
		_ = enc.Close()
		return fmt.Errorf("authoredcontent: encode reported documents: %w", err)
	}
	return enc.Close()
}

// Decode reads the reported form back into the document set it describes.
//
// It is the reading half of the one format, and it lives beside the writing half
// so a caller consuming a report does not re-derive the framing. A second,
// hand-rolled reader would agree with this one until the form gained a field,
// and the agreement is what would hide the divergence.
func Decode(data []byte) ([]Document, error) {
	var r report
	if err := yaml.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("authoredcontent: decode reported documents: %w", err)
	}
	return r.Documents, nil
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package pluginwire answers one question about a plugin export: what encoding
// does its payload wire carry, and how is an authored text payload turned into
// those bytes.
//
// It exists because that fact is not derivable where it is needed. A caller
// holding an export name and a payload the author wrote cannot tell from the
// name whether the guest on the other side reads text or the canonical binary
// encoding — the socket fixes that, one socket at a time, and there is no
// negotiation on the wire. So the fact is written down, and it is written down
// beside the payload codecs rather than here: the table in the generated file
// alongside this one is emitted from the same registrations that generate those
// codecs.
//
// The authoring surface stays text. Nobody writes canonical binary bytes by
// hand, so a caller takes text from its author and asks this package for the
// bytes the export actually reads.
package pluginwire

import "fmt"

// entry is one row of the generated table: an export, the payload shape that
// export receives, whether its wire is binary, and the encoder for that shape.
type entry struct {
	export string
	shape  string
	binary bool
	encode func(text []byte) ([]byte, error)
}

// EncodeForExport converts an authored text payload into the bytes the named
// export's wire carries.
//
// A text-wire export gets its bytes back unchanged, which is also what an export
// this package knows nothing about gets: a guest reads its own input and reports
// its own decode failure, so a name absent from the table is passed through
// rather than refused. A binary-wire export gets its payload read into that
// export's payload shape and re-emitted in the canonical binary encoding.
//
// An export that names more than one payload shape and carries a binary wire
// cannot have its payload encoded at all, because the shape is not recoverable
// from the name. That halts loudly and says which shapes it could not choose
// between, rather than encoding as one of them and producing bytes the guest
// silently misreads. Every export in the table names one shape, so the refusal
// guards the socket that adds a second rather than reporting on one today.
func EncodeForExport(export string, text []byte) ([]byte, error) {
	return encodeForExport(entries, export, text)
}

// encodeForExport is EncodeForExport over an explicit table, so each arm — the
// pass-through, the encode, and the refusal an export naming two shapes reaches
// once its wire is binary — can be driven without waiting for the generated
// table to grow a row of that shape.
func encodeForExport(table []entry, export string, text []byte) ([]byte, error) {
	var matched []entry
	for _, e := range table {
		if e.export == export {
			matched = append(matched, e)
		}
	}

	var binary []entry
	for _, e := range matched {
		if e.binary {
			binary = append(binary, e)
		}
	}
	if len(binary) == 0 {
		return text, nil
	}
	if len(matched) > 1 {
		return nil, fmt.Errorf("the %s export carries a binary payload wire and receives more than one "+
			"payload shape (%s), so the payload cannot be encoded from the export name alone",
			export, shapeList(matched))
	}
	return binary[0].encode(text)
}

// shapeList renders the payload shapes an export receives, in the table's own
// order, for the ambiguity message above.
func shapeList(matched []entry) string {
	names := make([]string, len(matched))
	for i, e := range matched {
		names[i] = e.shape
	}
	out := names[0]
	for _, n := range names[1:] {
		out += ", " + n
	}
	return out
}

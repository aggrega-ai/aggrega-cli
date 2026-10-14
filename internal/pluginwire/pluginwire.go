// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package pluginwire answers one question about a plugin export: does a socket
// call it with a payload in the canonical binary encoding, and how is an
// authored text payload turned into those bytes.
//
// It exists because that fact is not derivable where it is needed. A caller
// holding an export name and a payload the author wrote cannot tell from the
// name which payload shape the guest on the other side reads — a name is a
// symbol in the guest's export section and carries no shape with it. So the
// fact is written down, and it is written down beside the payload codecs rather
// than here: the table in the generated file alongside this one is emitted from
// the same registrations that generate those codecs.
//
// The authoring surface stays text. Nobody writes canonical binary bytes by
// hand, so a caller takes text from its author and asks this package for the
// bytes the export actually reads.
package pluginwire

import "fmt"

// entry is one row of the generated table: an export a socket calls with a
// payload in the binary encoding, the payload shape that export receives, and the
// encoder for that shape.
type entry struct {
	export string
	shape  string
	encode func(text []byte) ([]byte, error)
}

// EncodeForExport converts an authored text payload into the bytes the named
// export's wire carries.
//
// An export the table lists gets its payload read into that export's payload
// shape and re-emitted in the canonical binary encoding. An export the table
// does not list gets its bytes back unchanged: the describe export, which takes
// no payload; a CLI plugin's command export, whose payload crosses as JSON; and
// any export no socket calls by default, such as an entry function an attachment
// or a pipeline step names. A guest reads its own input and reports its own
// decode failure, so a name absent from the table is passed through rather than
// refused.
//
// An export that names more than one payload shape cannot have its payload
// encoded at all, because the shape is not recoverable from the name. That halts
// loudly and says which shapes it could not choose between, rather than encoding
// as one of them and producing bytes the guest silently misreads. Every export
// in the table names one shape, so the refusal guards the socket that adds a
// second rather than reporting on one today.
func EncodeForExport(export string, text []byte) ([]byte, error) {
	return encodeForExport(entries, export, text)
}

// encodeForExport is EncodeForExport over an explicit table, so each arm — the
// pass-through, the encode, and the refusal an export naming two shapes reaches
// — can be driven without waiting for the generated table to grow a row of that
// shape.
func encodeForExport(table []entry, export string, text []byte) ([]byte, error) {
	var matched []entry
	for _, e := range table {
		if e.export == export {
			matched = append(matched, e)
		}
	}

	switch len(matched) {
	case 0:
		return text, nil
	case 1:
		return matched[0].encode(text)
	default:
		return nil, fmt.Errorf("the %s export carries a binary payload wire and receives more than one "+
			"payload shape (%s), so the payload cannot be encoded from the export name alone",
			export, shapeList(matched))
	}
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

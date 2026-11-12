// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package pluginwire answers one question about a plugin export: does a socket
// call it with a payload in the canonical binary encoding, how is an authored
// text payload turned into those bytes, and how is the export's answer in that
// encoding turned back into text a person can read.
//
// It exists because that fact is not derivable where it is needed. A caller
// holding an export name and a payload the author wrote cannot tell from the
// name which payload shape the guest on the other side reads, nor which shape it
// answers with — a name is a symbol in the guest's export section and carries no
// shape with it. So the fact is written down, and it is written down beside the
// payload codecs rather than here: the table in the generated file alongside this
// one is emitted from the same registrations that generate those codecs.
//
// The authoring surface stays text. Nobody writes or reads canonical binary
// bytes by hand, so a caller takes text from its author, asks this package for
// the bytes the export actually reads, and asks it again to turn what the export
// answered back into text.
package pluginwire

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
)

// entry is one row of the generated table: an export a socket calls with a
// payload in the binary encoding, the payload shape that export receives and the
// encoder for that shape, and the shape it answers with and a constructor of an
// empty value of that shape for an answer to be decoded into.
type entry struct {
	export    string
	shape     string
	encode    func(text []byte) ([]byte, error)
	output    string
	newOutput func() wasm.GuestOutputCBOR
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

// DecodeForExport converts the answer the named export returned into JSON text,
// the mirror of EncodeForExport.
//
// An export the table lists answers in the canonical binary encoding: its answer
// is decoded into that export's output shape and written out as indented JSON,
// one object followed by a newline. The decode is the host's own strict
// socket-output decode, so an answer that is not the export's shape is refused
// exactly as a socket call refuses it, naming the export and what did not fit,
// rather than printed as whatever part of it could be read. An answer the decode
// accepts but JSON cannot write — a field the shape carries as a JSON document,
// left empty where the shape requires it — is refused too, naming the export and
// the field, because printing it would mean inventing a value the guest never
// returned. An export the table does not list gets its answer back unchanged,
// for the reason EncodeForExport passes its payload through: its wire is not
// this table's to know.
//
// An export the table lists more than once is refused, for the reason
// EncodeForExport refuses it.
func DecodeForExport(export string, answer []byte) ([]byte, error) {
	return decodeForExport(entries, export, answer)
}

// decodeForExport is DecodeForExport over an explicit table, for the reason
// encodeForExport takes one.
func decodeForExport(table []entry, export string, answer []byte) ([]byte, error) {
	var matched []entry
	for _, e := range table {
		if e.export == export {
			matched = append(matched, e)
		}
	}

	switch len(matched) {
	case 0:
		return answer, nil
	case 1:
	default:
		return nil, fmt.Errorf("the %s export carries a binary payload wire and receives more than one "+
			"payload shape (%s), so its answer cannot be decoded from the export name alone",
			export, shapeList(matched))
	}

	v := matched[0].newOutput()
	if err := wasm.DecodeGuestOutputCBOR(export, answer, v); err != nil {
		return nil, err
	}
	// The text is for a person to read, so markup characters in a message stay as
	// written rather than escaped.
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		if field, why, ok := unprintableDocumentField(v); ok {
			return nil, fmt.Errorf("the %s export answered with a %s whose %q %s, and a %s carries %q as a JSON "+
				"document, so the answer cannot be printed as JSON", export, matched[0].output, field, why,
				matched[0].output, field)
		}
		return nil, fmt.Errorf("the %s export answered with a %s that cannot be printed as JSON: %w",
			export, matched[0].output, err)
	}
	return out.Bytes(), nil
}

// rawDocumentType is the type an answer shape carries a JSON document in.
var rawDocumentType = reflect.TypeOf(json.RawMessage(nil))

// unprintableDocumentField names the first top-level field of a decoded answer
// that carries a JSON document the answer cannot be printed with — one left empty
// where the shape always writes it, or one holding bytes that are not JSON — and
// says which of the two it is. The strict decode reads which keys an answer
// holds, not whether a required one is filled, so a guest can return such an
// answer and pass it; naming the field is what lets its author see what to set.
// It reports false where no top-level field explains the failure.
func unprintableDocumentField(v any) (field, why string, ok bool) {
	rv := reflect.Indirect(reflect.ValueOf(v))
	if rv.Kind() != reflect.Struct {
		return "", "", false
	}
	rt := rv.Type()
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if f.Type != rawDocumentType || !f.IsExported() {
			continue
		}
		name, opts, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		doc := rv.Field(i).Bytes()
		switch {
		case len(doc) == 0 && !strings.Contains(opts, "omitempty"):
			return name, "is empty", true
		case len(doc) > 0 && !json.Valid(doc):
			return name, "is not a JSON document", true
		}
	}
	return "", "", false
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

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package parsercore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	"github.com/aggrega-ai/aggrega-abi/go/pkg/abi"
)

// errTrailing reports content after a document's top-level value.
var errTrailing = errors.New("unexpected trailing content after the top-level value")

// ParseJSON reads JSON source into a canonical JSON tree. The document may be an
// object, an array, or a scalar, matching JSON's own top-level shape. Numbers
// decode into json.Number so integer precision survives the round trip, and the
// re-encoded tree carries object keys in sorted order. Malformed JSON, or trailing
// content after the first value, yields an error diagnostic and a null tree rather
// than a crash.
func ParseJSON(src []byte) abi.ParseOutput {
	value, err := decodeJSON(src)
	if err != nil {
		return errorOutput("json parse: " + err.Error())
	}
	return treeOutput(value)
}

// decodeJSON decodes src as one JSON value with nothing after it, numbers as
// json.Number.
func decodeJSON(src []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(src))
	dec.UseNumber()

	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errTrailing
	}
	return value, nil
}

// position returns where in text the decoding error err was met, as " at line
// L, column C" naming the character the decoder refused: lines counted by line
// feeds, columns by characters, both from one. An error met at no character —
// a document cut short, content after the value — has no position, and is
// empty.
func position(text []byte, err error) string {
	var syntaxErr *json.SyntaxError
	if !errors.As(err, &syntaxErr) || syntaxErr.Offset < 1 || syntaxErr.Offset > int64(len(text)) {
		return ""
	}
	at := int(syntaxErr.Offset) - 1
	line := 1 + bytes.Count(text[:at], []byte("\n"))
	start := bytes.LastIndexByte(text[:at], '\n') + 1
	return fmt.Sprintf(" at line %d, column %d", line, utf8.RuneCount(text[start:at])+1)
}

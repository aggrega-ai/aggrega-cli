// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package parsercore

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/aggrega-ai/aggrega-abi/go/pkg/abi"
)

// ParseJSON reads JSON source into a canonical JSON tree. The document may be an
// object, an array, or a scalar, matching JSON's own top-level shape. Numbers
// decode into json.Number so integer precision survives the round trip, and the
// re-encoded tree carries object keys in sorted order. Malformed JSON, or trailing
// content after the first value, yields an error diagnostic and a null tree rather
// than a crash.
func ParseJSON(src []byte) abi.ParseOutput {
	dec := json.NewDecoder(bytes.NewReader(src))
	dec.UseNumber()

	var value any
	if err := dec.Decode(&value); err != nil {
		return errorOutput("json parse: " + err.Error())
	}
	if _, err := dec.Token(); err != io.EOF {
		return errorOutput("json parse: unexpected trailing content after the top-level value")
	}
	return treeOutput(value)
}

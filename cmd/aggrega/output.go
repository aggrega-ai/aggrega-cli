// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// structuredFormat is the byte shape a structured payload is emitted in.
//
// The shape is an explicit argument of every writeStructured call rather than a
// package-wide default, because the CLI's structured surfaces do not all agree on
// it and the disagreement is part of each surface's contract. Indented output is
// the norm: it is readable and diffable, and a consumer that does not want the
// whitespace can strip it. Compact output is the contract wherever a consumer
// reads the payload with line-oriented tools whose patterns match only the
// unspaced "key":"value" form; re-indenting such a surface breaks that consumer
// while the payload stays valid JSON, so nothing in the Go build or test run
// would notice. Naming the shape at each call site is what makes that contract
// visible where it is depended on and hard to change by accident.
type structuredFormat int

const (
	// structuredIndented emits two-space-indented JSON. It is the zero value, so
	// a surface that expresses no preference gets the readable form.
	structuredIndented structuredFormat = iota
	// structuredCompact emits single-line JSON.
	structuredCompact
)

// writeStructured writes payload to w as JSON in the requested byte shape,
// followed by exactly one newline, and is the single path a command's structured
// output takes. cmdName is the command's invocation path without the binary name
// ("plugin list", "version", "search"); it prefixes a marshal failure so the
// diagnostic names the surface that produced it.
//
// Both shapes emit one JSON document and one trailing newline and nothing else,
// so a consumer reading the stream sees one document per invocation. The compact
// shape is byte-identical to what encoding/json's streaming encoder produces for
// the same value, which is what lets a surface move onto this seam without its
// bytes changing.
func writeStructured(w io.Writer, cmdName string, shape structuredFormat, payload any) error {
	var (
		data []byte
		err  error
	)
	switch shape {
	case structuredCompact:
		data, err = json.Marshal(payload)
	case structuredIndented:
		data, err = json.MarshalIndent(payload, "", "  ")
	default:
		data, err = json.MarshalIndent(payload, "", "  ")
	}
	if err != nil {
		return fmt.Errorf("aggrega %s: marshal output: %w", cmdName, err)
	}
	_, err = w.Write(append(data, '\n'))
	return err
}

// parseFormat validates an --output value against the set its command accepts,
// returning the value unchanged when it is a member so the caller switches on a
// readable name rather than an ordinal. An unrecognized value yields an error
// naming both the rejected value and the accepted set.
//
// This is the one home for that rejection: every command that accepts --output
// refuses an unknown value with the same wording, so the sentence is not
// restated per command and cannot drift between them. allowed is given in the
// order the command's help lists it and the error names it in that order, so the
// diagnostic and the help agree.
func parseFormat(cmdName, got string, allowed ...string) (string, error) {
	for _, a := range allowed {
		if got == a {
			return got, nil
		}
	}
	return "", fmt.Errorf("aggrega %s: unknown --output value %q; expected one of %s",
		cmdName, got, strings.Join(allowed, ", "))
}

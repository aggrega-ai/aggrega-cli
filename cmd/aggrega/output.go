// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// writeStructured writes payload to w as two-space-indented JSON followed by
// exactly one newline, and is the single path a command's structured output
// takes. cmdName is the command's invocation path without the binary name
// ("plugin list", "version", "search"); it prefixes a marshal failure so the
// diagnostic names the surface that produced it.
//
// Every structured surface emits this one byte shape, and the shape is not a
// per-command choice: a reader who has learned what one command's --output json
// looks like has learned what all of them look like, and a diff of two runs shows
// the field that changed rather than a single re-flowed line. Indented output is
// also what a person reads without a formatter, while a consumer that wants the
// whitespace gone strips it — which costs the consumer one filter and costs the
// reader nothing.
//
// The call emits one JSON document and one trailing newline and nothing else, so
// a consumer reading the stream sees one document per invocation. A consumer that
// matches on the emitted text rather than parsing it should strip whitespace
// before matching: the field names and the values are the contract, and the
// surrounding layout is not something to depend on.
func writeStructured(w io.Writer, cmdName string, payload any) error {
	data, err := json.MarshalIndent(payload, "", "  ")
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

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package importops

import (
	"encoding/json"
	"fmt"
)

// UndetectedFormatError reports a source whose shape detection cannot classify.
//
// It is a distinct type rather than a plain error because the two surfaces that
// meet this condition correct it differently: a reader at a terminal passes a
// flag, and a caller on a protocol sends a field. Its message is the terminal's
// remedy, verbatim as the command line has always printed it, so lifting this
// subsystem left the command line's output unchanged; a protocol surface
// recognises the condition through this type and words its own refusal around
// the field a request can actually carry.
//
// Matching on the type rather than on the message text is what keeps the two in
// step: a surface that compared the sentence would keep compiling and silently
// stop matching the first time either wording moved.
type UndetectedFormatError struct {
	// Source is the source path as it was received.
	Source string
}

// Error renders the terminal's remedy for a source whose format was not
// recognised: the file, and the flag that names a format explicitly.
func (e *UndetectedFormatError) Error() string {
	return fmt.Sprintf("cannot detect the source format of %q; pass --format to name it explicitly "+
		"(for example --format langflow or --format flowise)", e.Source)
}

// Path returns the source path the detection could not classify, so a surface
// re-wording this refusal names the same file without parsing the message.
func (e *UndetectedFormatError) Path() string { return e.Source }

// SelectFormat resolves the source format for one import: an explicit format the
// caller named wins outright; otherwise the format is detected from the source's
// own top-level shape.
//
// It is the only implementation of that precedence, and every surface reaches it
// — a surface that wants to name a format supplies one, and a surface that
// relies on detection supplies the empty string. Giving the naming surface its
// own path would have been a second implementation of the same two-line rule,
// and the two would then have disagreed about which of an explicit format and a
// detected one wins.
//
// It mirrors the extension-versus-explicit precedence the parser socket uses,
// with content sniffing standing in for the extension fallback because the
// supported source formats share the .json extension.
func SelectFormat(explicit, source string, content []byte) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	return DetectFormat(source, content)
}

// DetectFormat selects the source format from a source's top-level JSON shape
// when the caller did not name one. It disambiguates the two supported
// agent-definition formats, which both use the .json extension, by where the
// flow graph sits in the top-level object:
//
//   - langflow — the graph is nested under a top-level "data" object, so the
//     top-level "data.nodes" is an array.
//   - flowise — the graph is flat at the top level, so top-level "nodes" and
//     "edges" are both arrays and there is no "data.nodes".
//
// When neither shape matches — a different JSON shape, or content that is not
// JSON at all — it does not guess: it returns an *UndetectedFormatError naming
// the source, so the caller is told to name the format rather than left with a
// guessed, wrong importer.
//
// Detection reads only content; it makes no network or filesystem access, so it
// is deterministic and reproducible.
func DetectFormat(source string, content []byte) (string, error) {
	var probe struct {
		Data struct {
			Nodes json.RawMessage `json:"nodes"`
		} `json:"data"`
		Nodes json.RawMessage `json:"nodes"`
		Edges json.RawMessage `json:"edges"`
	}
	if err := json.Unmarshal(content, &probe); err != nil {
		return "", &UndetectedFormatError{Source: source}
	}

	// Langflow wins first: a graph nested under data.nodes is the disambiguator,
	// so a file carrying it is langflow regardless of any flat top-level keys.
	if isJSONArray(probe.Data.Nodes) {
		return "langflow", nil
	}
	// Flowise: a flat graph — nodes and edges arrays directly at the top level,
	// with no data.nodes wrapper (already excluded above).
	if isJSONArray(probe.Nodes) && isJSONArray(probe.Edges) {
		return "flowise", nil
	}
	return "", &UndetectedFormatError{Source: source}
}

// isJSONArray reports whether raw is a JSON array by its first non-whitespace
// byte. It intentionally rejects a JSON null (which json.Unmarshal into a slice
// would accept) and an absent field (empty raw), so only a genuine array value
// counts toward format detection.
func isJSONArray(raw json.RawMessage) bool {
	for _, b := range raw {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue
		case '[':
			return true
		default:
			return false
		}
	}
	return false
}

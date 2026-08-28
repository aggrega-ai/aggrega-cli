// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package deprecation defines the on-the-wire format of a deprecation marker:
// the OCI artifactType that identifies a deprecation referrer and the payload
// that carries the marker's severity and message. Both the authoring side
// (attaching a marker to a published artifact) and the consuming side
// (discovering a marker at resolution time) import this package, so the format
// has one home and cannot drift between the two.
//
// A deprecation marker is a normal signed OCI referrer of the artifact it
// deprecates. The published artifact is immutable; the marker is a sibling
// manifest pointing back at it, so a marker can be attached retroactively
// without re-publishing the artifact.
package deprecation

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
)

// ArtifactType is the OCI artifactType of a deprecation referrer. A consumer
// discovers deprecation markers by listing referrers of this artifactType on
// an artifact's manifest, the same discovery mechanism signatures use with
// their own artifactType.
const ArtifactType = "application/vnd.aggrega.deprecation.v1+json"

// Severity classifies how a deprecation marker affects consumers of the
// deprecated artifact. The three values form a closed set; Validate rejects
// anything else.
type Severity string

const (
	// SeverityWarning marks the artifact as deprecated while leaving it
	// usable: a consumer is advised to migrate but is not blocked.
	SeverityWarning Severity = "warning"
	// SeverityError marks the artifact as deprecated and no longer fit for
	// new use: a consumer should treat resolving it as an error condition.
	SeverityError Severity = "error"
	// SeverityRetracted marks the artifact as withdrawn: it must not be used,
	// the strongest of the three severities.
	SeverityRetracted Severity = "retracted"
)

// StatedAs returns the phrase a notice uses to state an artifact's recorded
// severity to a reader.
//
// A notice names the severity VALUE the author wrote, so a reader can match what
// they are told against what a publisher recorded and against the halt threshold
// they configure. `retracted` is stated as retracted; the two advisory severities
// are stated as deprecated and name which of the two they are. Calling a retraction
// "deprecated" is the case this exists to prevent: the two mean different things —
// discouraged but still selected, against excluded from selection — and reporting
// the stronger as the weaker understates what happened.
//
// It is the single home of that wording, so two surfaces reporting the same
// recorded severity cannot come to describe it differently. A severity outside the
// closed set is stated verbatim rather than guessed at, which keeps a malformed
// value legible as itself instead of being reported as one of the three.
func (s Severity) StatedAs() string {
	switch s {
	case SeverityRetracted:
		return "retracted"
	case SeverityWarning, SeverityError:
		return "deprecated (" + string(s) + ")"
	default:
		return string(s)
	}
}

// severityRank orders the three severities from least to most severe:
// warning (0) < error (1) < retracted (2). An unknown severity ranks below
// warning (-1) so it never satisfies a threshold. It is the single definition
// of the severity ordering the build-time halt threshold compares against.
func severityRank(s Severity) int {
	switch s {
	case SeverityWarning:
		return 0
	case SeverityError:
		return 1
	case SeverityRetracted:
		return 2
	default:
		return -1
	}
}

// MeetsThreshold reports whether severity is at least as severe as threshold in
// the ordered set warning < error < retracted. It is the build-time gate's one
// comparison: a recorded deprecation halts when its severity MeetsThreshold the
// configured halt_on_deprecated_severity, and emits an advisory otherwise. An
// unknown severity or an unknown threshold never meets: a malformed value can
// neither trigger nor suppress a halt.
func MeetsThreshold(severity, threshold Severity) bool {
	sr := severityRank(severity)
	tr := severityRank(threshold)
	if sr < 0 || tr < 0 {
		return false
	}
	return sr >= tr
}

// Marker is the payload of a deprecation referrer. It carries the severity of
// the deprecation, a required human-readable reason, an optional replacement
// artifact reference, and the timestamp the marker was issued. The message
// field is retained for audit and for the recorded build-time diagnostic; it
// defaults to the reason when an author supplies no separate message. The JSON
// field order is fixed (severity, message, reason, replacement, issuedOn) and
// the optional fields are omitted when empty, so Marshal produces byte-identical
// output for identical inputs.
//
// issuedOn is the ordering key when more than one marker is attached to the same
// artifact: the marker with the newest issuedOn is the authoritative one. It is
// an RFC-3339 UTC timestamp, set by the authoring command at attachment time.
type Marker struct {
	Severity    Severity `json:"severity"`
	Message     string   `json:"message,omitempty"`
	Reason      string   `json:"reason"`
	Replacement string   `json:"replacement,omitempty"`
	IssuedOn    string   `json:"issuedOn,omitempty"`
}

// Validate reports whether the marker is well-formed: its severity is one of the
// three known values, its reason is non-empty, and its issuedOn — when present —
// is a valid RFC-3339 timestamp. The replacement and message fields are
// optional. issuedOn is tolerated empty so a marker that predates the ordering
// key still decodes (it sorts oldest); the authoring command always sets it.
// Both the authoring side (before attaching) and the consuming side (after
// decoding) call Validate, so the well-formedness rule has one definition.
func (m Marker) Validate() error {
	switch m.Severity {
	case SeverityWarning, SeverityError, SeverityRetracted:
	default:
		return fmt.Errorf("deprecation: unknown severity %q; want one of %q, %q, %q", m.Severity, SeverityWarning, SeverityError, SeverityRetracted)
	}
	if m.Reason == "" {
		return fmt.Errorf("deprecation: reason must not be empty")
	}
	if m.IssuedOn != "" {
		if _, err := time.Parse(time.RFC3339, m.IssuedOn); err != nil {
			return fmt.Errorf("deprecation: issuedOn %q is not a valid RFC-3339 timestamp: %w", m.IssuedOn, err)
		}
	}
	return nil
}

// IssuedOnTime parses the marker's issuedOn as an RFC-3339 timestamp. It reports
// the parsed time and whether a usable timestamp was present: a blank or
// unparseable issuedOn yields the zero time and false, so such a marker sorts
// oldest when the newest-issuedOn marker is selected. It is the single
// definition of the ordering key both the discovery pass and the query-time
// surfacing read.
func (m Marker) IssuedOnTime() (time.Time, bool) {
	if m.IssuedOn == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, m.IssuedOn)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// Marshal returns the canonical JSON encoding of the marker after validating
// it. The encoding is deterministic — fixed field order, no trailing newline,
// HTML escaping disabled so the message is stored verbatim — so the same marker
// produces byte-identical bytes across runs, and the deprecation referrer's
// manifest digest (which a signature commits to) is stable.
func Marshal(m Marker) ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		return nil, fmt.Errorf("deprecation: marshal marker: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// Unmarshal decodes canonical marker bytes and validates the result, so a
// consumer never acts on a marker with an unknown severity or an empty
// message. It is the inverse of Marshal.
func Unmarshal(data []byte) (Marker, error) {
	var m Marker
	if err := json.Unmarshal(data, &m); err != nil {
		return Marker{}, fmt.Errorf("deprecation: unmarshal marker: %w", err)
	}
	if err := m.Validate(); err != nil {
		return Marker{}, err
	}
	return m, nil
}

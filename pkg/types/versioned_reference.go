// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package types

import (
	"fmt"
	"strings"

	"github.com/Masterminds/semver/v3"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// SplitVersionedURI splits an authored reference such as
// "core/api-server:1.0.0" into its base (before the last colon) and version
// (after the last colon) segments. The last colon is the separator because a
// version segment does not contain colons while a base segment may carry
// registry-style separators ("hub.aggrega.ai/component/core/api-server:1.0.0").
//
// It is the single home for this split, so every reader of a versioned
// reference applies the same separator rule. The readers sit in packages that
// cannot import one another — the catalog's resolution ladder, the renderer's
// provider dispatch, and the load-time shape rules — so the rule lives here,
// in the package all three already depend on, rather than being restated
// beside each of them. A second copy would agree on the day it was written and
// would disagree silently on the first reference neither author had in mind.
//
// The separator rule is deliberately not the one an OCI reference follows. An
// OCI tag is the last colon after the last slash, which is what stops a
// registry host's port being read as a tag; this split takes the last colon
// anywhere, because its base may carry the registry-style separators above. On
// "localhost:5000/core/api-server" this rule yields base "localhost" and
// version "5000/core/api-server", while the OCI rule confines its search past
// the last slash and reports no version. Both are right for their own input
// shapes and neither is right for the other's. The two take the same argument
// and return the same shape, so substituting one for the other still compiles
// and changes the answer on exactly the registry-qualified references a real
// workspace is written in. They stay separate rules for separate inputs.
//
// Returns the bare reference as base, "" as version, and false as hasVersion
// when the reference carries no colon. hasVersion is what separates that case
// from a trailing colon with an empty version: "core/x" yields ("core/x", "",
// false) while "core/x:" yields ("core/x", "", true). The two agree on base and
// on version, so only hasVersion tells them apart.
func SplitVersionedURI(uri string) (base, version string, hasVersion bool) {
	idx := strings.LastIndex(uri, ":")
	if idx < 0 {
		return uri, "", false
	}
	return uri[:idx], uri[idx+1:], true
}

// ErrMalformedVersionPin fires when a reference carries a version pin that is
// none of the forms a pin takes: a caret pin whose range does not parse, or an
// exact pin that is not a strict semantic version — the form every entity's
// metadata.version takes.
//
// It is distinct from a pin no available version satisfies. A malformed pin
// names no version at all, so no catalog could ever satisfy it, and reporting it
// as an unsatisfiable or absent reference would send the author looking for a
// version or an entity when the fault is the text they typed.
//
// It is raised where a reference's lookup has already failed, never ahead of
// one, so it changes nothing about which references resolve: it replaces the
// explanation a failed lookup gives, on the reference whose pin is at fault.
func ErrMalformedVersionPin() error { return errMalformedVersionPin }

var errMalformedVersionPin = cerrors.Register("malformed-version-pin", cerrors.Meta{
	ShortCode: "E0311",
	Message:   "reference carries a malformed version pin",
	RulePlace: cerrors.RuleOverAuthoredInput(),
})

// VersionPinForms is the advice a malformed pin carries: the three forms a
// reference is written in. It is the one statement of them on a diagnostic, so a
// surface that reports the fault in a shape of its own takes this text rather
// than restating the forms.
const VersionPinForms = "write the reference as <name>, as <name>:<version> with a semantic version " +
	"such as 1.0.0, or as <name>:^<version> for the highest version compatible with it"

// VersionPinError is the failure [ParseVersionPin] reports for a malformed pin.
// It names the condition [ErrMalformedVersionPin] returns, so errors.Is matches
// it, and it carries the reference and the pin where a surface composing a
// diagnostic of its own shape can read them rather than parse the message back.
type VersionPinError struct {
	// Reference is the reference as it was authored.
	Reference string
	// Pin is the text after the reference's version separator.
	Pin string
	// Where names where the reference was authored, in the words of the surface
	// reporting it. Empty when the reporting surface has nothing to add, which is
	// the value the parser itself leaves; a surface that knows sets it through
	// [VersionPinError.At].
	Where string
}

// At returns a copy of the failure that names where the reference was authored,
// so a reporting surface adds its own coordinate without restating the rest.
func (e *VersionPinError) At(where string) *VersionPinError {
	c := *e
	c.Where = where
	return &c
}

// Error renders the condition's sentence, the occurrence's values, and the
// three forms as the advice.
func (e *VersionPinError) Error() string {
	return cerrors.ComposeMessage(errMalformedVersionPin, e.Values(), VersionPinForms)
}

// Unwrap returns the condition's identity, so errors.Is and the code readers
// reach it through any wrapping.
func (e *VersionPinError) Unwrap() error { return errMalformedVersionPin }

// Values returns the occurrence's values in the order a diagnostic lists them:
// the reference, the pin, which of the two pin forms it failed to be, and where
// the reference was authored when the reporting surface said.
func (e *VersionPinError) Values() []string {
	values := []string{
		fmt.Sprintf("reference %q", e.Reference),
		fmt.Sprintf("pin %q", e.Pin),
		e.why(),
	}
	if e.Where != "" {
		values = append(values, e.Where)
	}
	return values
}

// why says which form the pin was read as and what it lacks. The form is chosen
// by the leading caret alone, which is the rule every reader of a pin applies.
func (e *VersionPinError) why() string {
	if strings.HasPrefix(e.Pin, "^") {
		return "the text after ^ is not a version range"
	}
	return "an exact pin must be a semantic version"
}

// VersionPin is an authored reference split into its base and its version pin,
// with the pin's grammar judged. [ParseVersionPin] is the only way to build one.
type VersionPin struct {
	base   string
	pin    string
	hasPin bool
	rng    *semver.Constraints
	exact  *semver.Version
	fault  *VersionPinError
}

// ParseVersionPin splits reference with [SplitVersionedURI] and judges the pin
// it carries against the three forms a reference is written in:
//
//   - unversioned ("core/x") carries no pin, and there is nothing to judge;
//   - exact ("core/x:1.2.0") must be a strict semantic version;
//   - caret ("core/x:^1.2.0") must be a caret followed by a version range that
//     parses.
//
// It is the one home of that grammar. Every reader of a pin — the entity-version
// select, provider selection for a Recipe's capability, implements matching, and
// a pipeline step's plugin reference — takes its split and its parsed pin from
// here, and every surface that reports a reference it could not resolve asks it
// whether the pin was at fault, so a malformed pin is reported the same way
// wherever it is written.
//
// A segment carrying a "/" is not judged, because a pin never carries one: a
// registry host's port ("localhost:5000/core/api-server") is the one reference
// shape whose last colon precedes a slash, and the split reads the rest of the
// reference as its version. Declining to judge such a segment is what keeps a
// port from being reported as a malformed pin, and it leaves the split exactly as
// [SplitVersionedURI] states it.
//
// The judgement is about the grammar alone. Whether a well-formed pin names a
// version that exists stays the version select's question.
func ParseVersionPin(reference string) VersionPin {
	base, pin, hasPin := SplitVersionedURI(reference)
	p := VersionPin{base: base, pin: pin, hasPin: hasPin}
	if !hasPin || strings.Contains(pin, "/") {
		return p
	}
	if strings.HasPrefix(pin, "^") {
		rng, err := semver.NewConstraint(pin)
		if err != nil {
			p.fault = &VersionPinError{Reference: reference, Pin: pin}
			return p
		}
		p.rng = rng
		return p
	}
	exact, err := semver.StrictNewVersion(pin)
	if err != nil {
		p.fault = &VersionPinError{Reference: reference, Pin: pin}
		return p
	}
	p.exact = exact
	return p
}

// Base returns the reference's base: everything before its version separator,
// or the whole reference when it carries none.
func (p VersionPin) Base() string { return p.base }

// Pin returns the text after the reference's version separator, and whether the
// reference carries a separator at all.
func (p VersionPin) Pin() (string, bool) { return p.pin, p.hasPin }

// IsCaret reports whether the pin is written in the caret form, whether or not
// its range parses.
func (p VersionPin) IsCaret() bool { return p.hasPin && strings.HasPrefix(p.pin, "^") }

// Range returns the parsed range of a well-formed caret pin, and nil for any
// other reference.
func (p VersionPin) Range() *semver.Constraints { return p.rng }

// Version returns the parsed version of a well-formed exact pin, and nil for any
// other reference.
func (p VersionPin) Version() *semver.Version { return p.exact }

// Fault returns the failure naming [ErrMalformedVersionPin] when the pin is
// malformed, and nil when the reference carries no pin, a well-formed one, or a
// segment the parser does not judge.
func (p VersionPin) Fault() error {
	if p.fault == nil {
		return nil
	}
	return p.fault
}

// Malformed returns the failure as its concrete type and true when the pin is
// malformed, for a surface that composes its own diagnostic from the failure's
// values; it returns nil and false otherwise.
func (p VersionPin) Malformed() (*VersionPinError, bool) { return p.fault, p.fault != nil }

// BareCapability returns the capability a `capability:` binding names, with any
// version pin removed.
//
// A binding may be written unversioned ("helm"), with an exact pin
// ("helm:1.0.0"), or with a major-compatible range ("helm:^1.0.0"). The bare
// name selects the provider kind; the pin selects which version of the plugin
// serving that capability renders the document, and it is not part of the
// capability's name. So every reading that asks *which capability is this* —
// the render dispatch, the output grouping, and the load-time rules that carve
// one capability out of a shape requirement — asks it of this projection rather
// than of the authored string, and a pinned binding answers exactly as the
// unpinned one does.
//
// A longer capability whose name merely begins with a shorter one is a
// different capability and is returned unchanged: the projection removes a pin
// and never a prefix.
func BareCapability(binding string) string {
	base, _, _ := SplitVersionedURI(binding)
	return base
}

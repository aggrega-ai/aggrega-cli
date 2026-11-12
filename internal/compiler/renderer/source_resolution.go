// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"errors"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// sourceOrigin is what a refusal raised while resolving a source needs in order to
// report it without a value an author declared sensitive: the source as its author
// wrote it, and the set its templates were evaluated against. The resolution is
// handed the source a template already PRODUCED, and a coordinate in it carries no
// link back to what the template read; the authored text is what still carries it.
//
// The zero value declares nothing, which is what a caller resolving a source no
// declaration governs passes.
type sourceOrigin struct {
	authored any
	withheld redact.Keys
}

// partsReadingWithheld returns the parts of the authored source whose templates
// read a datum the set names.
func (o sourceOrigin) partsReadingWithheld() map[string]bool {
	return sourcePartsReadingWithheld(o.authored, o.withheld)
}

// sourcePartsReadingWithheld returns the parts of a source whose templates read a
// datum keys names, keyed as mapRecipeSourceTemplates names a part: a structured
// field by its own name, a source written as one string by redact.WholeSourceKey.
// It is the one reading of that question, over the one walk that classifies a
// source's shape, so a display withholding a source part and a refusal withholding
// a coordinate that part produced cannot disagree about which parts those are.
//
// Nothing is replaced: the walk's rebuilt value is discarded and each template's
// text is returned unchanged. A set naming nothing marks nothing.
func sourcePartsReadingWithheld(source any, keys redact.Keys) map[string]bool {
	parts := map[string]bool{}
	if !keys.AnyWithheld() {
		return parts
	}
	_, _ = mapRecipeSourceTemplates(source, func(field, tmplText string) (string, error) {
		if templateReadsWithheld(tmplText, keys) {
			parts[field] = true
		}
		return tmplText, nil
	})
	return parts
}

// sourceIdentityParts are the parts of a structured source that select which
// artifact it names: the authored keys a remote source's identity is evaluated from,
// each of which a refusal raised while looking the artifact up can quote. A source
// written as one string is its own identity, and redact.WholeSourceKey names it.
var sourceIdentityParts = []string{redact.WholeSourceKey, "kind", "url", "version", "chart", "ref", "module_subdir"}

// sourceRefusalWithheld is the sentence a refusal raised while resolving a remote
// source carries in place of its own text, when that text is composed from a
// coordinate a template produced by reading a value an author declared sensitive.
const sourceRefusalWithheld = "the reason is withheld: the template producing this source reads a value " +
	"declared sensitive, and the reason names what the template produced"

// withholdRefusal decides what a refusal raised while resolving a remote source may
// say. quoted names the parts of the source whose coordinates the refusal's text is
// composed from. Where none of them was produced by a template reading a datum the
// set names, the refusal is returned exactly as it was raised, so its text is
// byte-identical to what it would be with nothing declared.
//
// Where one was, the refusal is reported by what the source is called and a fixed
// sentence: the source named by its url and the version or reference it was asked
// at, each standing as the placeholder where the part producing it read a withheld
// datum, and nothing of the refusal's own text — every line of which names the
// coordinates it was looking up. It unwraps to the registered condition the refusal
// carried and to nothing else, so the condition's codes, its category and the advice
// given for it reach every surface while no coordinate a declared value produced
// does.
//
// url and at are the coordinates the evaluation produced, empty where the refusal
// was raised before there were any.
func (o sourceOrigin) withholdRefusal(err error, url, at, atPart string, quoted ...string) error {
	parts := o.partsReadingWithheld()
	if !anyPart(parts, quoted) {
		return err
	}
	name := ""
	if url != "" {
		if anyPart(parts, []string{redact.WholeSourceKey, "url"}) {
			url = redact.Placeholder()
		}
		if at != "" && parts[atPart] {
			at = redact.Placeholder()
		}
		name = "source-artifact " + url
		if at != "" {
			name += "@" + at
		}
	}
	return newWithheldFailure(name, sourceRefusalWithheld, err)
}

// anyPart reports whether parts marks any of names, a source written as one string
// standing for every part of it.
func anyPart(parts map[string]bool, names []string) bool {
	if parts[redact.WholeSourceKey] {
		return true
	}
	for _, name := range names {
		if parts[name] {
			return true
		}
	}
	return false
}

// frSourceURI is the FeatureRecipe analogue of the recipe URI used for source-spec
// evaluation: metadata.name, optionally suffixed with :version.
func frSourceURI(fr *types.FeatureRecipe) string {
	uri := fr.Metadata.Name
	if fr.Metadata.Version != "" {
		uri = uri + ":" + fr.Metadata.Version
	}
	return uri
}

// wrapSourceResolutionError categorises a source-resolution error: fetch sentinels
// route to FETCH_ERROR with an appended remediation pointer (unless the chain already
// carries one); everything else routes to TRANSLATION_ERROR. Shared by the component
// node loop and the FeatureRecipe render sites so both surface source-fetch failures
// identically. label is the leading "node %q ... source resolution" message.
func wrapSourceResolutionError(err error, nodeID, label string) error {
	category := cerrors.TranslationError
	message := label
	if sentinel, ok := matchFetchSentinel(err); ok {
		category = cerrors.FetchError
		if !strings.Contains(err.Error(), "remediation:") {
			if rem := oci.RemediationFor(sentinel, hostFromSourceErr(err)); rem != "" {
				message = message + "; " + rem
			}
		}
	}
	return cerrors.Wrap(err, category, cerrors.SourceLocation{Instance: nodeID}, message)
}

// matchFetchSentinel walks the error chain via errors.Is against each
// fetch-family sentinel: the four remote-acquisition faults declared at
// pkg/oci/errors.go plus the offline-egress refusal from pkg/egress. Returns
// the matched sentinel + true on first hit; returns (nil, false)
// when the chain carries no fetch sentinel.
//
// The orchestrator uses the matched sentinel to (1) route the
// CompilerError through cerrors.FetchError instead of
// cerrors.TranslationError and (2) call oci.RemediationFor to append
// the operator-actionable remediation when the underlying chain
// doesn't already carry one (e.g., build-time errors from
// pkg/sourcecache that didn't pass through sourceresolver.Wrap).
func matchFetchSentinel(err error) (error, bool) {
	switch {
	case errors.Is(err, oci.ErrFetchNotFound()):
		return oci.ErrFetchNotFound(), true
	case errors.Is(err, oci.ErrFetchUnauthorized()):
		return oci.ErrFetchUnauthorized(), true
	case errors.Is(err, oci.ErrFetchNetworkUnreachable()):
		return oci.ErrFetchNetworkUnreachable(), true
	case errors.Is(err, oci.ErrFetchIntegrity()):
		return oci.ErrFetchIntegrity(), true
	case errors.Is(err, egress.ErrOfflineEgress()):
		// An offline-egress refusal is a fetch-family fault: from the user's
		// perspective it is an inability to fetch. It carries its own remediation,
		// so the classifier appends none.
		return egress.ErrOfflineEgress(), true
	default:
		return nil, false
	}
}

// hostFromSourceErr is a best-effort extractor for the registry
// host substring inside a source-resolution error chain. The
// orchestrator passes it to oci.RemediationFor for build-time
// errors that didn't pass through sourceresolver.Wrap (which would
// otherwise have done the extraction at the lock-time side).
//
// The current implementation scans the error message for a
// "url=<URL>" segment (the format sourceresolver.Wrap emits) and
// returns the host portion of the matched URL. When no URL segment
// is found — typical of pure sourcecache errors — returns "" so
// RemediationFor uses the placeholder.
func hostFromSourceErr(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	idx := strings.Index(msg, "url=")
	if idx < 0 {
		return ""
	}
	rest := msg[idx+len("url="):]
	end := strings.IndexAny(rest, " \t")
	if end > 0 {
		rest = rest[:end]
	}
	for _, scheme := range []string{"oci://", "https://", "http://", "git+ssh://", "git://", "ssh://"} {
		if strings.HasPrefix(rest, scheme) {
			body := strings.TrimPrefix(rest, scheme)
			if slash := strings.Index(body, "/"); slash > 0 {
				return body[:slash]
			}
			return body
		}
	}
	return ""
}

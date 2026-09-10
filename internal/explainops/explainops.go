// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package explainops

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/aggrega-ai/aggrega-cli/internal/docsbundle"
	"github.com/aggrega-ai/aggrega-cli/internal/haltref"
	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// Pages are the two published pages an explanation is read from: the one that
// resolves every declared condition, and the one that documents the authoring fix
// for the conditions it covers.
//
// They travel together because they come from one bundle and one read: fetching
// them separately would let a reply resolve a code against one version of the
// documentation and take its prose from another.
type Pages struct {
	// Reference is the bytes of the page that resolves every condition the
	// compiler declares, including the ones declared inside a provider guest.
	Reference []byte

	// Halts is the bytes of the page that documents the authoring fix for the
	// conditions it covers.
	Halts []byte
}

// Explanation is one composed answer: the condition a code names, the published
// entries that teach the fix, and the reason those entries are missing where they
// are.
//
// Note carries a reason rather than a failure. The condition is the answer, and a
// missing walk-through leaves it standing — so a surface renders the condition and
// reports the note beside it, never in place of it. It is nil when nothing is
// missing.
//
// Entries and Note are never both populated: a read that could not produce the
// entries is what supplies the note. A surface may therefore render them in
// whichever order suits its channels without having to choose between them.
type Explanation struct {
	// Condition is what the binary or the published reference declares about the
	// code: its two identifiers and the sentence its diagnostics open with.
	Condition cerrors.Condition

	// Entries are the published entries documenting the condition, in the order
	// the page's own correspondence names them.
	Entries []haltref.Entry

	// Note is the reason the walk-through is absent, or nil when it is not.
	Note error
}

// Compose returns the explanation for code, reading the published pages through
// loadPages.
//
// Two conditions have to be met before anything is composed, and they are reached
// in the order that costs the least. The compiler's own declarations answer first
// and answer offline; the published reference answers for the rest, which are the
// conditions a provider guest declares and the host that loads it therefore never
// registers. Only once neither resolves is the code refused.
//
// loadPages is called at most once and is not called at all for a code answered
// from the retirement record, which is what keeps a spent number from costing a
// documentation acquisition.
//
// Three shapes report failure, and each is a different fact about the code. A code
// neither side resolves is refused as a code that names nothing, which the reply
// may say outright because both sides were searched. A code this binary does not
// declare, where the reference could not be searched at all, is refused as
// undetermined instead — the refusal states what it measured and leaves both
// repairs standing. And a number that named a condition once is refused with what
// it named, because what a reader needs is that the number is spent.
//
// A returned error may arrive alongside a populated Condition, and that pairing is
// deliberate: a page that resolved the code and then could not be read as the page
// it is a defect rather than an answer, and the condition it did resolve is still
// worth reporting to whoever asked. A refusal that resolved nothing returns the
// zero Explanation, which [Write] renders as nothing.
func Compose(code string, loadPages func() (Pages, error)) (Explanation, error) {
	condition, declaredErr := haltref.Resolve(code)

	// A retired number is answered without reading anything. It is absent from
	// the published reference by construction — the reference refuses to carry a
	// number that is both retired and registered — so consulting it could only
	// spend a round trip to learn what the retirement record already said.
	var retired *haltref.RetiredCodeError
	if errors.As(declaredErr, &retired) {
		return Explanation{}, declaredErr
	}

	pages, pageErr := loadPages()

	if declaredErr != nil {
		if pageErr != nil {
			// Nothing here declares the code and the published reference could not
			// be consulted, so this cannot tell a code that names nothing from one
			// a guest declares. Both halves are reported, because the repair
			// differs: one is a correction to the code, the other is getting the
			// documentation.
			return Explanation{}, fmt.Errorf("%w\n\n%v", withoutTheReference(declaredErr), pageErr)
		}
		published, perr := haltref.ResolveIn(pages.Reference, code)
		if perr != nil {
			var unknown *haltref.UnknownCodeError
			if errors.As(perr, &unknown) {
				// The reference was read and carries no such code. Every place a
				// condition is declared has now been searched, so the refusal is
				// entitled to the definitive negative it states.
				return Explanation{}, declaredErr
			}
			// The reference arrived and could not be read as the page it is, which
			// leaves the code as undetermined as an unreachable bundle does. Both
			// halves are reported for the reason the branch above reports both.
			return Explanation{}, fmt.Errorf("%w\n\n%v", withoutTheReference(declaredErr), perr)
		}
		condition = published
	}

	if pageErr != nil {
		return Explanation{Condition: condition, Note: pageErr}, nil
	}

	entries, err := haltref.Entries(pages.Halts, condition.ShortCode)
	if err != nil {
		// A condition the page does not document is an ordinary fact about the
		// published reference and leaves the condition standing. Anything else from
		// this read means the page is not the one it resolves against, which is a
		// defect rather than an answer — and the condition is still reported, since
		// it did resolve and does not depend on the page.
		var undocumented *haltref.UndocumentedConditionError
		if errors.As(err, &undocumented) {
			return Explanation{Condition: condition, Note: err}, nil
		}
		return Explanation{Condition: condition}, err
	}
	return Explanation{Condition: condition, Entries: entries}, nil
}

// withoutTheReference narrows a refusal to what was actually measured, for the
// paths where the published reference could not be consulted.
//
// The narrowing is the whole point. A code missing from this binary's own
// declarations is not thereby a code that names nothing: a provider's conditions
// are declared inside the guest that raises them, so the host registers none of
// them, and only the reference tells a mistyped code from one of those. Carrying
// the definitive negative into this path would assert something no read
// established, and it would assert it hardest against a provider author: a
// provider's conditions are precisely the ones the host cannot see for itself.
//
// A refusal of any other kind passes through unchanged, because it already
// reports what it measured.
func withoutTheReference(declaredErr error) error {
	var unknown *haltref.UnknownCodeError
	if errors.As(declaredErr, &unknown) {
		return &haltref.UnresolvedCodeError{Code: unknown.Code}
	}
	return declaredErr
}

// Write renders e the way a terminal reads it: the condition on out, the
// published entries after it, and whatever could not be expanded on notes.
//
// A zero Explanation renders nothing, which is what a refusal returns: a surface
// may therefore write whatever [Compose] produced and then report its error,
// without having to decide whether there was anything to write.
func Write(out, notes io.Writer, e Explanation) error {
	if e.Condition.ShortCode == "" && e.Condition.Code == "" {
		return nil
	}
	if err := writeCondition(out, e.Condition); err != nil {
		return err
	}
	for _, entry := range e.Entries {
		if _, err := fmt.Fprintf(out, "\n%s\n", entry.Body); err != nil {
			return err
		}
	}
	if e.Note != nil {
		return note(notes, e.Note)
	}
	return nil
}

// writeCondition writes the part of the answer the binary declares: the two codes
// the condition carries and the sentence a diagnostic naming it opens with.
func writeCondition(out io.Writer, c cerrors.Condition) error {
	_, err := fmt.Fprintf(out, "%s  %s\n\nA diagnostic naming this condition opens with:\n  %s\n",
		c.ShortCode, c.Code, c.Message)
	return err
}

// note writes a reason the walk-through is missing, and reports success: the
// reply above it is the answer, and the reason is a note about what could not be
// added to it.
func note(notes io.Writer, reason error) error {
	_, err := fmt.Fprintf(notes, "\n%s\n", reason)
	return err
}

// LoadPages returns the published pages for a workspace, acquiring the
// documentation bundle when the workspace has no copy.
//
// Acquisition happens here rather than at process start because explaining a code
// is the only thing that needs the bundle: a fetch wired earlier would put a
// registry round-trip in front of every other operation. The reads that follow it
// are offline by construction — they take no target — so a warm cache reaches for
// nothing.
//
// It is for a caller whose acquisition of the bundle is this call: a surface that
// acquires on its own schedule reads through [ReadPages] instead, which is the
// same read without the fetch.
//
// Every failure it reports names the artifact and the version it looked for, so a
// reader who cannot get the documentation is told what was missing rather than
// that something was not found. Warnings raised while acquiring go to warn, which
// is the caller's own channel for them.
func LoadPages(
	ctx context.Context,
	cfg workspaceconfig.Config,
	root, version string,
	warn func(string),
) (Pages, error) {
	cacheDir := cfg.ResolvedArtifactCacheDir()
	ref := docsbundle.Ref(cfg.DefaultRegistry)

	if _, aerr := docsbundle.EnsureCached(ctx, cfg, root, cacheDir, ref, version, nil, warn); aerr != nil {
		return Pages{}, uncachedBundle(ref, version, aerr)
	}
	return ReadPages(root, cacheDir, ref, version)
}

// ReadPages returns the published pages a workspace has cached, reaching for
// nothing.
//
// It takes the bundle's coordinates already resolved, because which bundle a
// caller serves is the caller's own decision — a session pinned to one reads that
// one — while WHICH two pages an explanation is read from, and how their absence
// is worded, belong here. A surface that read the pages itself would word a
// missing bundle its own way, and a reader who could not get the documentation
// would then be told a different thing depending on where they asked.
//
// It takes no target and cannot reach a registry. Acquiring the bundle is the
// caller's act, performed before this or not at all, so a surface that only reads
// never links the acquisition path — which is what lets a caller whose fetch
// happens on its own schedule share this read rather than reimplement it.
func ReadPages(root, cacheDir, ref, version string) (Pages, error) {
	var pages Pages
	for _, p := range []struct {
		path string
		into *[]byte
	}{
		{haltref.ReferencePagePath, &pages.Reference},
		{haltref.PagePath, &pages.Halts},
	} {
		data, _, rerr := docscache.ReadPage(root, cacheDir, ref, version, p.path)
		if rerr != nil {
			// A bundle this workspace has no copy of is a different fact from a
			// bundle that is here and does not carry the page, and a reader repairs
			// them differently: one is documentation to obtain, the other a bundle
			// that is not the one this reads.
			if errors.Is(rerr, docscache.ErrFetchNotFound()) {
				return Pages{}, uncachedBundle(ref, version, rerr)
			}
			return Pages{}, fmt.Errorf("the published documentation is not available: the "+
				"documentation bundle %s@%s carries no readable %s (%v)", ref, version, p.path, rerr)
		}
		*p.into = data
	}
	return pages, nil
}

// uncachedBundle renders the reason a workspace with no cached documentation
// cannot be given the walk-through.
//
// It has one home because two callers state it — the acquiring read and the
// read-only one — and it is the sentence a reader sees most often, since a fresh
// workspace and an offline one both arrive here. A second copy would agree on the
// day it was written and diverge the first time either was edited, leaving two
// answers to what "the documentation is not available" means.
//
// It names the artifact and the version rather than reporting that something was
// not found, and it carries both published addresses, so the answer stands on its
// own for a reader who has to go and read the pages instead.
func uncachedBundle(ref, version string, cause error) error {
	return fmt.Errorf("the published documentation is not available: the documentation "+
		"bundle %s@%s is not cached for this workspace and could not be acquired (%v). The conditions "+
		"this binary declares are still answered; every condition, including the ones a provider "+
		"declares, is published at %s, and the walk-through of each authoring fix at %s",
		ref, version, cause,
		haltref.PublishedPath(haltref.ReferencePagePath), haltref.PublishedPath(haltref.PagePath))
}

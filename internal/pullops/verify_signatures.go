// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pullops

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginstrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// ErrRecordedSignatureOutOfDate reports that a verifying pull found a signature
// fact, recorded in the lockfile or in the record of plugin identities beside it,
// that a plain pull run now would write differently: a fact it would record where
// none is recorded, one it would replace, or one it would clear.
//
// One condition covers both files because it is one fact in two places — the
// signature a pull verified for an artifact, which a lockfile row carries for a
// pinned artifact and a record entry carries for a plugin attached at a verdict
// role — so a script or a suppression matching it meets both, for the reason the
// build's refusal of a missing required fact is one condition over both stores.
//
// It is reached through this accessor and carries both of a halting condition's
// identifiers, so every surface that reports a verify's failure can name it: the
// command line quotes the short code, and a structured answer carries the
// descriptive code as a field of its own.
func ErrRecordedSignatureOutOfDate() error { return errRecordedSignatureOutOfDate }

var errRecordedSignatureOutOfDate = cerrors.Register("recorded-signature-out-of-date", cerrors.Meta{
	ShortCode: "E0333",
	Message:   "signing: a recorded signature is not the one a pull would record",
	RulePlace: cerrors.NotARule("A verifying pull found a signature fact the lockfile, or the record of plugin identities " +
		"beside it, records differently from what a plain pull would record now. The fact is a pull's own answer, " +
		"from a registry and the content cache, so it reports a recorded verification fact that is out of date " +
		"rather than a rule over authored content."),
})

// verifyRecordedSignatures is a verifying pull's comparison of the recorded
// signature facts: it reports every signature fact a plain pull run now would
// write differently, in the lockfile and in the record of plugin identities beside
// it, and writes neither file. It runs in the verify-only branch of Run, after the
// pins have been held to the resolution, so a fact is compared only over a
// lockfile whose pins are the ones a pull would write.
//
// The lockfile side reads which rows a pull asks a registry about, and why, from
// the pass's own decision (signatureQuestions), so whatever the pass comes to ask
// about, a verify reports with no change of its own:
//
//   - A row the pass asks about because a signature is required for it and the row
//     records none, or because its recorded signature was made for another artifact,
//     is one a pull rewrites whatever the registry answers — it records the
//     artifact's own signature, or halts where none can be established. Such a row
//     is reported without asking.
//   - Every other row the pass asks about — a row whose bytes this verify fetched,
//     and a row whose recorded signature a build refuses for another reason, its
//     cached bundle removed, unreadable or altered — is rewritten only where the
//     registry's answer differs from what it records. It is asked through the pass's
//     own call over the lockfile the pass would compose, never the file, and
//     reported where the answer records a signature, replaces one or clears one.
//     Under the offline posture it is not asked: one diagnostic line names such rows
//     and says their signatures were not checked, as the comparison of recorded
//     source references does for its own offline narrowing.
//
// The lockfile compared against is composed as the pass composes it: rows from the
// resolution, with the loaded file's facts carried forward by the pass's own
// carry-forward (carryForwardVerifiedFacts) — not a copy of the loaded file. The two
// differ on a row whose recorded manifest digest no longer matches the one the
// content cache read for it: a pull carries no fact across that, so it asks or
// clears, and a copy of the file would hold the old fact instead. Every comparison
// is against the file as loaded.
//
// The record side reads what the record would become from the one computation of
// it (verdictTrustAfterPull), which the caller has reached through the registry for
// every verdict plugin it attaches: a held entry whose signature that would record,
// replace or clear is reported, and so is a plugin the record holds no entry for
// where a build refuses it for recording no signature — the build's own answer, its
// check of the record (compilerops.VerdictPluginSignatureRefusals) read over view,
// the catalog view a build of the Release reads — since a pull writes an entry
// carrying its fact and the build refuses the plugin until then. Any other first
// sighting is not reported: the identity a pull would record for it is not a fact a
// verify holds the record to. An entry whose recorded signature was made for another
// artifact needs no rule of its own here: the walk verified the plugin's own
// signature, which is never that one, so the computation replaces it.
//
// Under the offline posture the caller's walk asked no registry about a verdict
// plugin (verdictPluginLookupFor), so for such a plugin that computation has no
// signature to compare and is not read. The plugin is held instead to the build's
// own check of the record. Where the build refuses it for recording no signature —
// its entry records none, or the record holds no entry for it — or because the
// signature its entry records was made for another artifact, a pull rewrites the
// entry whatever its registry answers, so it is reported without asking, as a
// lockfile row of either kind is. Every other entry of such a plugin — one the build
// accepts, and one whose cached bundle it refuses as removed, unreadable or altered,
// which only the registry's answer settles — is named on one diagnostic line saying
// its signature was not checked, and nothing is reported about it.
//
// Deprecation markers are compared in neither file. A marker newly attached to an
// artifact is registry state that moves after the pull, and failing a verify on one
// would fail a check over files nothing in the workspace changed — the reasoning
// that has a recorded marker advise a build rather than halt it.
//
// Every difference is collected and returned together, one occurrence each, under
// ErrRecordedSignatureOutOfDate. A halt the pass would meet — a row that binds to
// no registry, a registry answer the trust configuration refuses — is returned as
// that halt, beside whatever differences were already found. The content cache may
// be filled: a bundle a verify's question verified is cached, as a verify already
// fills the cache.
func verifyRecordedSignatures(ctx context.Context, loaded *lockfile.Lockfile, cl closure.Closure, releasePath, lockfilePath string,
	wsCfg workspaceconfig.Config, workspaceRoot, cacheDir string, view *catalog.LazyLookup, verdictResolutions []verdictPluginResolution,
	mode catalog.PinReuseMode, diagOut io.Writer, targetFor pullverify.TargetFor) error {
	differences, err := lockfileSignatureDifferences(ctx, loaded, cl, releasePath, lockfilePath, wsCfg, workspaceRoot,
		cacheDir, diagOut, targetFor)
	if err != nil {
		return err
	}
	recordDifferences, err := trustRecordSignatureDifferences(releasePath, workspaceRoot, wsCfg, view, verdictResolutions, mode, diagOut)
	if err != nil {
		return err
	}
	differences = append(differences, recordDifferences...)
	switch len(differences) {
	case 0:
		return nil
	case 1:
		return differences[0]
	default:
		return errors.Join(differences...)
	}
}

// lockfileSignatureDifferences is verifyRecordedSignatures over the lockfile.
func lockfileSignatureDifferences(ctx context.Context, loaded *lockfile.Lockfile, cl closure.Closure, releasePath, lockfilePath string,
	wsCfg workspaceconfig.Config, workspaceRoot, cacheDir string, diagOut io.Writer, targetFor pullverify.TargetFor) ([]error, error) {
	// The rows a pull composes before its signature pass, with no bindings,
	// attachments or entity overlay: none of those reaches a fact-bearing row, and
	// the pins have just been held to the resolution, so each row's reference,
	// version and content digest are the loaded file's own.
	would := buildLockfile(releasePath, cl, nil, nil, nil, nil, nil)
	carryForwardVerifiedFacts(would, loaded)
	questions, err := signatureQuestions(would, cl, wsCfg, workspaceRoot, cacheDir)
	if err != nil {
		return nil, err
	}
	rewritten := make(map[lockfile.RowKey]signatureQuestion, len(questions))
	var asking []signatureQuestion
	for _, q := range questions {
		if rewrittenWhateverTheRegistryAnswers(q) {
			rewritten[q.row] = q
			continue
		}
		asking = append(asking, q)
	}
	notAsked := make(map[lockfile.RowKey]bool, len(asking))
	var askErr error
	if len(asking) > 0 {
		// The offline decision is read from the egress gate, as the comparison of
		// recorded source references reads it.
		if _, err := egress.RoundTripper(wsCfg); errors.Is(err, egress.ErrOfflineEgress()) {
			for _, q := range asking {
				notAsked[q.row] = true
			}
		} else if _, err := recordSignatureAnswers(ctx, would, askedOnce(asking), wsCfg, workspaceRoot, cacheDir,
			diagOut, targetFor); err != nil {
			askErr = err
		}
	}

	recorded := loaded.RecordedSignatures()
	var differences []error
	var skipped []string
	for _, row := range would.VerifiedFactRows() {
		key := row.Key()
		was := recorded[key]
		if q, ok := rewritten[key]; ok {
			// A question carrying the build's refusal is, among the rows rewritten
			// whatever the registry answers, one refused as made for another artifact.
			differences = append(differences, rewrittenWithoutAsking(q.row.Quoted(), lockfilePath, "records "+describeSignature(was),
				q.refused != nil, releasePath))
			continue
		}
		if notAsked[key] {
			skipped = append(skipped, key.Quoted())
			continue
		}
		if askErr != nil {
			continue
		}
		if sameSignature(was, row.Signature()) {
			continue
		}
		differences = append(differences, recordedSignatureOutOfDate(key.Quoted(), lockfilePath,
			"records "+describeSignature(was), "a pull would record "+describeSignature(row.Signature()), releasePath))
	}
	if len(skipped) > 0 {
		fmt.Fprintf(diagOut, "  verify: offline, so the signature the lockfile records for %s was not checked at its "+
			"registry, which alone can say whether a pull would record it again; this verify does not cover it\n",
			strings.Join(skipped, ", "))
	}
	if askErr != nil {
		return nil, errors.Join(append(differences, askErr)...)
	}
	return differences, nil
}

// rewrittenWhateverTheRegistryAnswers reports whether a pull rewrites the row q
// asks about whatever its registry answers. A cache-served row recording no fact is
// asked about only because a missing signature halts for it, so a pull records a
// signature on it or halts. A row whose recorded signature was made for another
// artifact receives the registry's answer for its own artifact, which cannot be
// that signature. Every other question — a fetched row, and a row whose cached
// bundle a build refuses — is one only the registry's answer settles: the answer
// may be the signature already recorded.
func rewrittenWhateverTheRegistryAnswers(q signatureQuestion) bool {
	if q.fetched {
		return false
	}
	return q.refused == nil || errors.Is(q.refused, oci.ErrSignatureForAnotherArtifact())
}

// askedForARequiredSignature is what an occurrence says a pull would record for an
// entry, in either file, that records no signature where a build refuses it for
// recording none, when the verify did not ask the registry what it would answer.
const askedForARequiredSignature = "a pull would ask its registry for the signature a requirement asks for and record it"

// askedForTheArtifactsOwnSignature is what an occurrence says a pull would record
// for an entry, in either file, whose recorded signature a build refuses as made
// for another artifact, when the verify did not ask the registry what it would
// answer.
const askedForTheArtifactsOwnSignature = "a pull would ask its registry for this artifact's own signature and record it"

// rewrittenWithoutAsking is the occurrence for an entry, in either file, a pull
// rewrites whatever its registry answers: subject (rendered, as
// recordedSignatureOutOfDate takes it) and file name it, recorded is what
// the file records for it, and madeForAnother says the signature recorded was made
// for another artifact — which the pull replaces with the artifact's own — rather
// than missing where a requirement asks for one, which the pull records. The two
// files share it, so an entry of either kind reads alike in both.
func rewrittenWithoutAsking(subject, file, recorded string, madeForAnother bool, releasePath string) error {
	wouldRecord := askedForARequiredSignature
	if madeForAnother {
		recorded += ", made for another artifact"
		wouldRecord = askedForTheArtifactsOwnSignature
	}
	return recordedSignatureOutOfDate(subject, file, recorded, wouldRecord, releasePath)
}

// trustRecordSignatureDifferences is verifyRecordedSignatures over the record of
// plugin identities beside the lockfile.
//
// What a build refuses in the record is the build's own answer, never the walk's:
// compilerops.VerdictPluginSignatureRefusals is the build's check of the record —
// the attachments read as a build reads them, over view, each plugin's entry held as
// the build holds it — run over the record as loaded, with workspaceRoot locating
// the cached bundles it reads. The walk's resolutions cannot answer it, because the
// walk reads each plugin from its cache slot directly while a build reads the
// catalog view first: a plugin a catalog directory serves, or one the lockfile pins,
// whose bytes are in the cache too has a resolution and is not one a build holds to
// a requirement or to the manifest its bundle was made for. A refusal is told apart
// by its condition, as the lockfile side tells its own apart.
//
// A resolution the walk did not ask its registry about (SignatureNotAsked) carries
// no answer, so the computation's signature change for it — a cleared signature,
// over a held entry that records one — is not a change a pull would make, and is
// left out; which plugins those are is the computation's own NotAsked, read off the
// one reduction it shares with the hold. Such a plugin is held to the build's check
// alone: reported where the build refuses it for recording no signature, or because
// the signature its entry records was made for another artifact, and otherwise,
// where the record holds an entry for it, named on the line diagOut receives.
func trustRecordSignatureDifferences(releasePath, workspaceRoot string, wsCfg workspaceconfig.Config, view *catalog.LazyLookup,
	resolutions []verdictPluginResolution, mode catalog.PinReuseMode, diagOut io.Writer) ([]error, error) {
	outcome, err := verdictTrustAfterPull(releasePath, wsCfg.VerdictAttachments(), resolutions, mode)
	if err != nil {
		return nil, err
	}
	if !outcome.Changed() && len(outcome.NotAsked) == 0 {
		return nil, nil
	}
	notAsked := make(map[string]bool, len(outcome.NotAsked))
	for _, coordinate := range outcome.NotAsked {
		notAsked[coordinate] = true
	}
	recordPath := pluginstrust.PathFor(releasePath)
	held, _, err := pluginstrust.LoadOrStart(recordPath)
	if err != nil {
		return nil, err
	}
	refused := make(map[string]error, len(resolutions))
	for _, r := range compilerops.VerdictPluginSignatureRefusals(view, wsCfg, workspaceRoot, releasePath, held) {
		refused[r.Plugin] = r.Refusal
	}
	var differences []error
	for _, c := range outcome.Changes {
		if notAsked[c.Plugin] {
			continue
		}
		after, _ := outcome.Trust.Find(c.Plugin)
		switch c.Kind {
		case verdictTrustSignatureRecorded, verdictTrustSignatureCleared:
			before, _ := held.Find(c.Plugin)
			differences = append(differences, recordedSignatureOutOfDate(fmt.Sprintf("%q", c.Plugin), recordPath,
				"records "+describeSignature(before.Signature), "a pull would record "+describeSignature(after.Signature),
				releasePath))
		case verdictTrustRecorded:
			if !errors.Is(refused[c.Plugin], lockfile.ErrSignatureNotRecorded()) {
				continue
			}
			differences = append(differences, recordedSignatureOutOfDate(fmt.Sprintf("%q", c.Plugin), recordPath,
				unrecordedEntry, "a pull would record "+describeSignature(after.Signature), releasePath))
		}
	}
	var unchecked []string
	for _, coordinate := range outcome.NotAsked {
		entry, isHeld := held.Find(coordinate)
		switch refusal := refused[coordinate]; {
		case errors.Is(refusal, lockfile.ErrSignatureNotRecorded()):
			recorded := unrecordedEntry
			if isHeld {
				recorded = "records " + describeSignature(nil)
			}
			differences = append(differences, rewrittenWithoutAsking(fmt.Sprintf("%q", coordinate), recordPath, recorded, false, releasePath))
		case errors.Is(refusal, oci.ErrSignatureForAnotherArtifact()):
			differences = append(differences, rewrittenWithoutAsking(fmt.Sprintf("%q", coordinate), recordPath,
				"records "+describeSignature(entry.Signature), true, releasePath))
		case isHeld:
			unchecked = append(unchecked, fmt.Sprintf("%q", coordinate))
		}
	}
	if len(unchecked) > 0 {
		fmt.Fprintf(diagOut, "  verify: offline, so the signature of %s was not checked at its registry, which alone can say "+
			"whether a pull would record the same signature on its entry in the plugin trust record; this verify does not "+
			"cover it\n", strings.Join(unchecked, ", "))
	}
	return differences, nil
}

// unrecordedEntry is what an occurrence says the record holds for a plugin it has
// no entry for, where a build refuses that plugin for recording no signature.
const unrecordedEntry = "records no entry for it and a signature is required for it"

// sameSignature reports whether two recorded signature facts are the same: both
// absent, or both present and equal. It asks the record's own comparison, over a
// record carrying that fact alone, so "has this fact moved" keeps one answer.
func sameSignature(a, b *lockfile.VerifiedSignature) bool {
	return pluginstrust.TrustedPlugin{Signature: a}.Equal(pluginstrust.TrustedPlugin{Signature: b})
}

// describeSignature names a recorded signature fact the way an occurrence of
// ErrRecordedSignatureOutOfDate states it: its mode, its signer, the issuer of a
// keyless one, and the digest of the bundle it names — or that there is none.
func describeSignature(sig *lockfile.VerifiedSignature) string {
	if sig == nil {
		return "no signature"
	}
	var b strings.Builder
	b.WriteString("a ")
	if sig.Mode != "" {
		b.WriteString(sig.Mode + " ")
	}
	b.WriteString("signature")
	if sig.Identity != "" {
		b.WriteString(" by " + sig.Identity)
	}
	if sig.Issuer != "" {
		b.WriteString(" issued by " + sig.Issuer)
	}
	b.WriteString(" with bundle digest " + sig.BundleDigest)
	return b.String()
}

// recordedSignatureOutOfDate composes one occurrence of
// ErrRecordedSignatureOutOfDate: the reference or plugin it is about, the file and
// what that file records, what a pull would record instead, and the pull that
// brings the file up to date. release is the Release as the reader named it.
//
// subject is already rendered: a lockfile row as closure.EntityKey.Quoted names
// it — its reference, and its kind where the row records one, as the drift
// comparison names a row — and a plugin by its quoted coordinate.
func recordedSignatureOutOfDate(subject, file, recorded, wouldRecord, release string) error {
	values := []string{subject, file + " " + recorded, wouldRecord}
	advice := fmt.Sprintf("remediation: run `aggrega pull --release %s` to bring %s up to date", release, file)
	return &recordedSignatureOutOfDateError{message: cerrors.ComposeMessage(errRecordedSignatureOutOfDate, values, advice)}
}

// recordedSignatureOutOfDateError renders the composed occurrence and unwraps to
// the condition.
type recordedSignatureOutOfDateError struct {
	message string
}

func (e *recordedSignatureOutOfDateError) Error() string { return e.message }

func (e *recordedSignatureOutOfDateError) Unwrap() error { return errRecordedSignatureOutOfDate }

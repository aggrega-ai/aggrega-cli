// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package pullverify runs signature verification at pull/lock time. It is
// the lock-time half of the signing model: pull discovers each pinned
// entity's signature referrers, verifies them against the registry's trust
// policy, records a verified-signature fact for the earliest that verifies,
// and caches its bundle as a content-addressed blob for air-gapped
// re-verification. Build never imports this package — verification is a
// recorded fact build reads from the lockfile, not a step build re-runs (the
// AOT boundary: build is offline and lockfile-only).
//
// The verification LOGIC lives in pkg/signing (keyed-bundle and
// keyless-bundle verify); this package orchestrates it across a closure's
// entities, resolving each entity's trust policy and turning the verify
// outcome into a durable fact. The package is deliberately decoupled from
// the lockfile and closure types: callers supply the entities and a
// per-entity target factory, and receive neutral Result values to fold
// into the lockfile.
package pullverify

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/sigstore/sigstore-go/pkg/root"
	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/pkg/egress/livetrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// Entity is the minimal pin a verification needs: the canonical URI, the
// OCI manifest digest the signature commits to, and the registry host the
// entity resolved from.
type Entity struct {
	URI string
	// ManifestDigest is the OCI manifest digest the artifact's signature
	// commits to — the cryptographic verify subject. It is the manifest
	// coordinate, not the portable content digest the caller uses for the
	// cache pin and integrity check.
	ManifestDigest string
	// Host is the registry host the entity resolved from, already
	// normalised via registryhost.Of. Empty for a non-registry (local /
	// catalog) entity, which is never verified.
	Host string
	// Location is the full OCI repository reference (host + path) the entity
	// resolved from, when the reference cannot be recovered from URI alone. A
	// catalog entity's URI is a host-less bare identity — the registry host and
	// path are bound at pull time from the workspace registry and the entity
	// kind — so its referrer-discovery target is resolved from Location, not
	// URI. Empty for a host-carrying artifact (a content-pinned .wasm plugin),
	// whose URI already carries the reference; the target factory then falls
	// back to the URI.
	Location string
	// Kind is the entity's canonical catalog kind (PascalCase, e.g. "Component"
	// or "Plugin"), used to consult the workspace signing policy's kind scope.
	// The collection site derives it from the closure kind hint through the
	// single kind-normalization primitive. Empty when the kind could not be
	// classified, in which case an active org signing policy still requires a
	// signature (the fail-safe below refuses to silently exempt an
	// unclassifiable artifact).
	Kind string
}

// Result is the verified-signature fact for one entity, plus the cached
// bundle bytes the caller writes to the closure as a content-addressed
// blob. Mode is "keyless" or "keyed"; Issuer is set for keyless, and Identity
// for both (for keyed, the pinned key's fingerprint).
//
// LoggedAt is the time the transparency log recorded a keyless signature, as
// the verified log entry attests it, in UTC. It is the zero time for a keyed
// signature, which no log records, and for a keyless one whose verified
// entries carry no time. It is a verified fact like the others, so a caller
// may use it as a trustworthy time where it is set.
type Result struct {
	URI          string
	Mode         string
	Identity     string
	Issuer       string
	BundleDigest string
	Bundle       []byte
	LoggedAt     time.Time
}

// TargetFor returns the read-only OCI target and subject reference to
// discover an entity's signature referrers against. The subjectRef is the
// reference the target resolves to the entity's manifest descriptor
// (typically the content digest or version tag). A nil target with a nil
// error means the entity has no reachable registry (local/catalog) and is
// skipped.
type TargetFor func(e Entity) (target oras.ReadOnlyTarget, subjectRef string, err error)

// Options configures a Verify run.
type Options struct {
	// Config is the resolved workspace config carrying the per-registry
	// trust policy and the global skip.
	Config workspaceconfig.Config
	// Target resolves a per-entity target for referrer discovery.
	Target TargetFor
	// Warn reports a non-fatal verification notice (advisory downgrade,
	// global skip). Never nil in production; a nil Warn drops notices.
	Warn func(string)
}

// Verify runs the verification pass over entities and returns the verified
// facts. Where an entity carries several signatures that verify, its fact is
// the earliest of them by EarlierSignature, never the first the registry
// happens to list. A fatal verification failure (a required signature that is
// missing, untrusted, or invalid for a non-advisory registry) returns a
// non-nil error and halts the pull. A required signature whose lookup could
// not be completed halts it too: a check that did not run has not passed.
// Where no signature is required, neither an absent signature nor an
// unanswerable lookup halts, because neither leaves anything to verify.
// Advisory registries downgrade failures to warnings; the global skip and
// per-registry skip disable verification loudly.
//
// What the configuration asks of each entity's signature is read through
// workspaceconfig.Config.SignatureCheckFor, and whether a missing signature
// halts is that check's HaltsWithoutSignature — the same decision a build makes
// over a lockfile row that records no signature, so a setting that admits an
// artifact here admits it there and one that requires a signature requires it at
// both.
//
// The registry is asked about up to oci.RegistryRequestsAtOnce entities at once,
// each through Target. What each answer means is judged afterwards, one entity
// after another in the order given, so the results, every notice handed to Warn
// and the refusal returned are those of a run asking about one entity at a time:
// the refusal is the first entity's in that order that halts, whichever answer
// arrived first. Every live trust root the verifications need is the one fetch a
// shared root holds (livetrust.EnsureSharedRoot) — the caller's, where ctx carries
// one. Target is called from several goroutines at once.
func Verify(ctx context.Context, entities []Entity, opts Options) ([]Result, error) {
	warn := opts.Warn
	if warn == nil {
		warn = func(string) {}
	}

	if opts.Config.SignatureVerificationDisabled() {
		warn(globalSkipNotice)
		return nil, nil
	}

	checks := make([]workspaceconfig.SignatureCheck, len(entities))
	for i, e := range entities {
		checks[i] = opts.Config.SignatureCheckFor(hostOf(e), e.Kind)
	}
	type answer struct {
		res *Result
		err error
	}
	answers := make([]answer, len(entities))
	ctx = livetrust.EnsureSharedRoot(ctx)
	oci.EachAtOnce(ctx, len(entities), oci.RegistryRequestsAtOnce, func(ctx context.Context, i int) {
		if checks[i].Skip == workspaceconfig.SignatureSkipRegistry {
			return
		}
		res, verr := verifyEntity(ctx, opts.Config, entities[i], checks[i].Policy, checks[i].Configured, opts.Target)
		answers[i] = answer{res: res, err: verr}
	})

	var results []Result
	for i, e := range entities {
		check := checks[i]
		if check.Skip == workspaceconfig.SignatureSkipRegistry {
			warn(registrySkipNotice(check.Host))
			continue
		}
		res, err := judge(check, e, answers[i].res, answers[i].err, warn)
		if err != nil {
			return nil, err
		}
		if res != nil {
			results = append(results, *res)
		}
	}
	return results, nil
}

// VerifyKindlessArtifact runs [Verify] for one artifact that publishes under no
// catalog kind — a published catalog snapshot, say — at reference, whose manifest
// digest is manifestDigest, read from target, and returns its verified fact, or nil
// where it is admitted without one.
//
// It is the one place such an artifact's entity is built: its identity is the
// reference with the digest, its host the one the reference names, and its kind
// empty, so [workspaceconfig.Config.SignatureCheckFor] counts it as required under
// any active requirement whatever kinds that requirement is scoped to — an
// artifact nothing classifies is never exempted for that reason. Every other step
// is Verify's own.
func VerifyKindlessArtifact(ctx context.Context, cfg workspaceconfig.Config, reference, manifestDigest string,
	target oras.ReadOnlyTarget, warn func(string)) (*Result, error) {
	results, err := Verify(ctx, []Entity{{
		URI:            reference + "@" + manifestDigest,
		ManifestDigest: manifestDigest,
		Host:           registryhost.Of(reference),
	}}, Options{
		Config: cfg,
		Target: func(Entity) (oras.ReadOnlyTarget, string, error) { return target, manifestDigest, nil },
		Warn:   warn,
	})
	if err != nil || len(results) == 0 {
		return nil, err
	}
	return &results[0], nil
}

// VerifyStored decides whether an artifact is admitted on the strength of the
// signature bundles a pull fetched for it earlier and kept, under the
// configuration's own check for the artifact, without asking the registry
// anything.
//
// It is the decision [Verify] makes, taken over kept bundles rather than over the
// registry's referrer listing, and the two share every step of it: the global and
// per-registry skips admit and say so; the earliest kept bundle that verifies
// through [VerifyBundle] is the artifact's verified fact; no kept bundle at all is
// a missing signature, admitted exactly when the check does not halt without one;
// and a failure on an advisory host is reported and admitted. So a setting that
// admits an artifact at a pull admits the same kept bundles here, and one that
// refuses them there refuses them here.
//
// The registry is never asked, so the question "could the registry be asked" does
// not arise. Whether the verification itself reaches anything is [VerifyBundle]'s
// own answer for the policy: a pinned key and a pinned trust root verify from what
// is held locally, and a keyless policy with neither obtains the live trust root —
// unless held names a trust root to verify such a policy against instead
// ([HeldRoot]). A nil held is the live root, as at a pull.
//
// It returns the verified fact, nil where the artifact is admitted without one, and
// the refusal Verify would have halted the pull with otherwise.
func VerifyStored(cfg workspaceconfig.Config, e Entity, bundles [][]byte, held HeldRoot, warn func(string)) (*Result, error) {
	if warn == nil {
		warn = func(string) {}
	}
	if cfg.SignatureVerificationDisabled() {
		warn(globalSkipNotice)
		return nil, nil
	}
	check := cfg.SignatureCheckFor(hostOf(e), e.Kind)
	if check.Skip == workspaceconfig.SignatureSkipRegistry {
		warn(registrySkipNotice(check.Host))
		return nil, nil
	}
	candidates := make([]candidate, 0, len(bundles))
	for _, b := range bundles {
		candidates = append(candidates, candidate{bundle: b})
	}
	res, verr := earliestVerified(cfg, e, candidates, check.Policy, held.anchor())
	return judge(check, e, res, verr, warn)
}

// HeldRoot names a trust-root file a keyless policy that pins no trust root of its
// own is verified against in place of the live public root, for a read of kept
// signature bundles ([VerifyStored]).
//
// It answers "" where the live root is to be obtained as usual, and an error where
// neither can be used — which then refuses such a policy's verification, and only
// that: it is asked only when a keyless signature is verified under a policy
// pinning no root, so a skip, a pinned key, a pinned trust root and an artifact
// keeping no signature decide exactly as they would without it.
//
// The file it names is used the way a pinned trust_root is: through the same
// loader, and keyed by its bytes in [StoredVerdictKey].
type HeldRoot func() (path string, err error)

// anchor returns the keyless trust anchor a read of kept bundles builds under held:
// keylessAnchor's own, with the held file standing in for the live root where held
// names one.
func (held HeldRoot) anchor() anchorFor {
	if held == nil {
		return keylessAnchor
	}
	return func(cfg workspaceconfig.Config, policy workspaceconfig.RegistryConfig) (root.TrustedMaterial, error) {
		if policy.TrustedRoot == "" {
			path, err := held()
			if err != nil {
				return nil, err
			}
			policy.TrustedRoot = path
		}
		return keylessAnchor(cfg, policy)
	}
}

// heldTrustedRoot returns the trust-root file the keyless anchor held builds reads
// for policy — the pinned one, or else the one held names — and "" where it reads
// none and would obtain the live root. Its error is held's.
func (held HeldRoot) heldTrustedRoot(policy workspaceconfig.RegistryConfig) (string, error) {
	if policy.PublicKey != "" || policy.TrustedRoot != "" || held == nil {
		return policy.TrustedRoot, nil
	}
	return held()
}

// StoredVerdictKey returns a key naming everything [VerifyStored]'s verdict for e
// over bundles is decided by, so that a caller may remember a verdict under it: two
// calls returning the same key put VerifyStored the same question about the same
// bundles.
//
// What it names is what the decision reads. The check the configuration asks of
// e's signature, which carries both skips, the trusted identities, a pinned key,
// the pinned trust root's path and the advisory and requirement stance. The offline
// posture, which decides whether a keyless check with no pinned trust root can
// obtain the live one at all. The bytes of the trust-root file a keyless check
// verifies against — a pinned one, or the one held names for a check pinning none —
// which are read when the verification runs rather than when the configuration is,
// so an edit to that file changes the key. The entity. And each bundle, by content
// digest, in the order given. A read added to [VerifyBundle], or to the keyless
// trust anchor it builds, belongs here too.
//
// held is the one [VerifyStored] is given for the same read, and it is asked only
// where the check is keyless and pins no trust root.
//
// ok is false where the key cannot be taken — a trust-root file that cannot be
// read, or a held root that cannot be named — and a verdict is then not to be
// remembered; the verification itself reports why that root is unusable.
//
// The key is compared only within one process and never written anywhere, so its
// encoding owes nothing to any other version of the binary. It may tell apart two
// questions that would be decided alike — two identity lists in different orders —
// which costs a caller one more verification and never a different verdict.
func StoredVerdictKey(cfg workspaceconfig.Config, e Entity, bundles [][]byte, held HeldRoot) (key string, ok bool) {
	inputs := struct {
		Check       workspaceconfig.SignatureCheck
		Offline     bool
		TrustedRoot string
		Entity      Entity
		Bundles     []string
	}{
		Check:   cfg.SignatureCheckFor(hostOf(e), e.Kind),
		Offline: cfg.Offline,
		Entity:  e,
	}
	path, err := held.heldTrustedRoot(inputs.Check.Policy)
	if err != nil {
		return "", false
	}
	if path != "" {
		material, err := os.ReadFile(path)
		if err != nil {
			return "", false
		}
		inputs.TrustedRoot = blobDigest(material)
	}
	for _, b := range bundles {
		inputs.Bundles = append(inputs.Bundles, blobDigest(b))
	}
	encoded, err := json.Marshal(inputs)
	if err != nil {
		return "", false
	}
	return string(encoded), true
}

// globalSkipNotice is what the top-level skip_signature_verification switch is
// announced with, wherever it turns a check off.
const globalSkipNotice = "signature verification is GLOBALLY DISABLED (skip_signature_verification: true) — " +
	"no artifact's signature is being checked, including the hub baseline"

// registrySkipNotice is what a registry's own skip_signature_verification is
// announced with, wherever it turns a check off.
func registrySkipNotice(host string) string {
	return fmt.Sprintf("signature verification skipped for registry %q (skip_signature_verification: true)", host)
}

// hostOf returns the registry host an entity's signature is checked for: the
// host it resolved from, or the one its reference names.
func hostOf(e Entity) string {
	if e.Host != "" {
		return e.Host
	}
	return registryhost.Of(e.URI)
}

// judge applies check to the outcome of looking for e's signature — the verified
// fact, or the reason there is none — and returns the fact to record, nil where the
// artifact is admitted without one, or the refusal that halts.
//
// It is the one halt decision both a pull and a read of kept bundles take, so the
// two cannot come to disagree about which artifacts a setting admits.
func judge(check workspaceconfig.SignatureCheck, e Entity, res *Result, verr error, warn func(string)) (*Result, error) {
	if verr == nil {
		return res, nil
	}
	// Two outcomes leave no signature to verify — the registry answered and
	// nothing is attached, and the registry could not be asked — and whether
	// either stops the pull is the check's one halt decision.
	absent := errors.Is(verr, signing.ErrNoSignatureFound()) ||
		errors.Is(verr, oci.ErrReferrerDiscoveryUnavailable())
	if absent && !check.HaltsWithoutSignature() {
		switch {
		case check.Required():
			// Required, and the host is advisory: the failure is reported and the
			// pull goes on.
			warn(fmt.Sprintf("signature verification failed for %s (advisory; continuing): %v", e.URI, verr))
		case errors.Is(verr, oci.ErrReferrerDiscoveryUnavailable()):
			// The registry could not be asked, so nothing was established either
			// way. Treating that as a failed verification would refuse an artifact
			// on the strength of a question nobody managed to put — but it is said
			// out loud, because a check that did not run is not a check that
			// passed, and silence would read as a clean unsigned pull. Where a
			// signature IS required this case is not admitted: a required check
			// that could not run has not passed.
			warn(fmt.Sprintf("signature verification could not run for %s "+
				"(the registry could not answer the signature lookup and no signature is required; continuing): %v", e.URI, verr))
		}
		// The registry answered and nothing is attached, and nothing requires a
		// signature: verify-if-present means nothing to verify.
		return nil, nil
	}
	if check.Advisory {
		warn(fmt.Sprintf("signature verification failed for %s (advisory; continuing): %v", e.URI, verr))
		return nil, nil
	}
	return nil, fmt.Errorf("verifying the signature of %s: %w", e.URI, verr)
}

// verifyEntity fetches and verifies one entity's signatures and returns the
// earliest that verifies, by EarlierSignature. Returns a nil Result with no
// error when the entity has no reachable registry (skipped). Returns
// ErrNoSignatureFound when the registry reports no signature referrer, and an
// error wrapping oci.ErrReferrerDiscoveryUnavailable when the referrer lookup
// could not be completed at all — the caller distinguishes the two because only
// the first is an answer.
//
// The entity is trusted when ANY of its signatures verifies, and a signature
// that could not be fetched or does not verify beside one that does changes
// nothing. Where none verifies, the error is the last failure met in the order
// the registry lists the signatures. Every signature is read and judged rather
// than the walk stopping at the first that verifies, so an entity carrying one
// more signature costs one more fetch and one more verification: the listing
// order is an order of referrer digests and says nothing about when each
// signature was attached, so stopping early would let a signature attached
// later stand in for the entity's own.
func verifyEntity(ctx context.Context, cfg workspaceconfig.Config, e Entity, policy workspaceconfig.RegistryConfig, configured bool, targetFor TargetFor) (*Result, error) {
	if targetFor == nil {
		return nil, nil
	}
	target, subjectRef, err := targetFor(e)
	if err != nil {
		return nil, fmt.Errorf("resolve target: %w", err)
	}
	if target == nil {
		// Local / catalog entity — never carries a registry signature.
		return nil, nil
	}

	refs, err := oci.ListReferrers(ctx, target, subjectRef, signing.BundleMediaType)
	if err != nil {
		return nil, fmt.Errorf("list signature referrers: %w", err)
	}

	candidates := make([]candidate, 0, len(refs))
	for _, ref := range refs {
		bundleBytes, ferr := oci.FetchReferrerPayload(ctx, target, ref)
		candidates = append(candidates, candidate{bundle: bundleBytes, err: ferr})
	}
	return earliestVerified(cfg, e, candidates, policy, keylessAnchorContext(ctx))
}

// candidate is one signature an artifact carries, as it was obtained: its bundle
// bytes, or the reason they could not be read.
type candidate struct {
	bundle []byte
	err    error
}

// earliestVerified verifies each candidate signature of e through VerifyBundle,
// in the order given, and returns the earliest that verifies by EarlierSignature.
// No candidate at all is ErrNoSignatureFound. Where none verifies, the error is
// the last failure met in the order given — a candidate that could not be read
// counting as a failure — and ErrSignatureVerificationFailed where no failure was
// recorded.
//
// It is the one walk over an artifact's signatures, whether they were just fetched
// from the registry or kept from an earlier fetch; anchor is the keyless trust
// anchor each verification builds.
func earliestVerified(cfg workspaceconfig.Config, e Entity, candidates []candidate, policy workspaceconfig.RegistryConfig, anchor anchorFor) (*Result, error) {
	if len(candidates) == 0 {
		return nil, signing.ErrNoSignatureFound()
	}

	// Track the last error so a wholly-unverifiable set surfaces a meaningful
	// failure.
	var (
		earliest *Result
		lastErr  error
	)
	for _, c := range candidates {
		if c.err != nil {
			lastErr = c.err
			continue
		}
		res, verr := verifyBundle(cfg, c.bundle, e.ManifestDigest, policy, anchor)
		if verr != nil {
			lastErr = verr
			continue
		}
		if earliest == nil || EarlierSignature(res, earliest) {
			earliest = res
		}
	}
	if earliest != nil {
		earliest.URI = e.URI
		return earliest, nil
	}
	if lastErr == nil {
		lastErr = signing.ErrSignatureVerificationFailed()
	}
	return nil, lastErr
}

// VerifyBundle verifies one signature bundle against the registry trust
// policy and returns the verified fact (Result without URI) on success.
// Keyed mode is chosen when the policy pins a public key; otherwise keyless
// against the policy's trusted identities and trust anchor. artifactDigest is
// the OCI manifest digest the signature commits to — the cryptographic
// subject.
//
// The recorded Identity/Issuer is the bundle's actual matched signer: for
// keyless mode the certificate's verified identity reported by the verify
// pass; for keyed mode the pinned-key fingerprint (a keyed bundle carries
// no certificate, so the pinned key IS the signer). The recorded identity
// is what the closure-aware same-signer check compares across a plugin's
// entity and .wasm.
//
// It is the single bundle-verification primitive: the pull-time signature
// pass verifies each entity's signature through it, a read of kept signature
// bundles ([VerifyStored]) verifies them through it, and the retroactive
// deprecation-marker pass verifies a marker's own signature through it too
// (passing a policy whose trusted set is the deprecation-signer set), so every
// such surface shares one crypto path and none can drift.
func VerifyBundle(cfg workspaceconfig.Config, bundleBytes []byte, artifactDigest string, policy workspaceconfig.RegistryConfig) (*Result, error) {
	return verifyBundle(cfg, bundleBytes, artifactDigest, policy, keylessAnchor)
}

// VerifyBundleWithin is [VerifyBundle] for a verification made under ctx: where a
// keyless check pinning no trust root of its own verifies against the live public
// trust root, it takes that root through livetrust.TrustedRootWithin, so every
// verification under a context carrying a shared root (livetrust.WithSharedRoot)
// verifies against one fetch of it. The checks, the signers honored and the
// refusals are VerifyBundle's own; only where the live root comes from differs,
// and under a context carrying no shared root, not even that.
func VerifyBundleWithin(ctx context.Context, cfg workspaceconfig.Config, bundleBytes []byte, artifactDigest string, policy workspaceconfig.RegistryConfig) (*Result, error) {
	return verifyBundle(cfg, bundleBytes, artifactDigest, policy, keylessAnchorContext(ctx))
}

// verifyBundle is [VerifyBundle] building its keyless trust anchor with anchor: a
// read of kept bundles passes the one its [HeldRoot] builds, a verification made
// under a context passes the one keylessAnchorContext builds for it, and every
// other verification passes keylessAnchor.
func verifyBundle(cfg workspaceconfig.Config, bundleBytes []byte, artifactDigest string, policy workspaceconfig.RegistryConfig, anchor anchorFor) (*Result, error) {
	digest := blobDigest(bundleBytes)
	if policy.PublicKey != "" {
		if err := signing.VerifyKeyedBundle(bundleBytes, []byte(policy.PublicKey), artifactDigest); err != nil {
			return nil, err
		}
		return &Result{
			Mode:         "keyed",
			Identity:     keyedSignerIdentity(policy.PublicKey),
			BundleDigest: digest,
			Bundle:       bundleBytes,
		}, nil
	}

	ids := make([]signing.TrustedIdentity, 0, len(policy.TrustedIdentities))
	for _, id := range policy.TrustedIdentities {
		ids = append(ids, signing.TrustedIdentity{
			Identity:       id.Identity,
			IdentityRegexp: id.IdentityRegexp,
			Issuer:         id.Issuer,
		})
	}

	material, err := anchor(cfg, policy)
	if err != nil {
		return nil, err
	}
	matched, err := signing.VerifyKeylessBundle(bundleBytes, material, ids, artifactDigest)
	if err != nil {
		return nil, err
	}
	return &Result{
		Mode:         "keyless",
		Identity:     matched.Identity,
		Issuer:       matched.Issuer,
		BundleDigest: digest,
		Bundle:       bundleBytes,
		LoggedAt:     matched.LoggedAt,
	}, nil
}

// EarlierSignature reports whether verified signature a ranks before b in the
// order that decides which of an artifact's signatures stands for its
// publication where more than one verifies. It is the one ordering: the
// pull-time pass ranks by it when it records an artifact's verified signature,
// and so does the registry index when it dates a version and names its signer,
// so a later signature, however it was attached, never displaces the earliest
// for either of them, and the two rank by one rule rather than by two copies of
// it free to drift apart.
//
// A signature carrying a transparency-log time ranks before one carrying none,
// and of two that carry one the earlier time ranks first. The log's time is the
// publication date wherever a verified signature carries one, so a signature
// without one is never preferred over a signature that attests a time. A keyed
// signature carries no log time, but a keyed and a keyless signature never both
// verify under one policy — VerifyBundle verifies keyed against a pinned key and
// keyless only where none is pinned — so in practice the rule orders keyless
// signatures by their log time, and among keyed ones, all of them the pinned
// key's, decides nothing but which bundle is recorded.
//
// A tie — two equal log times, which the log records to the second, or two
// signatures carrying none — breaks on the lower bundle digest, an order the
// registry's referrer listing does not affect.
func EarlierSignature(a, b *Result) bool {
	aLogged, bLogged := !a.LoggedAt.IsZero(), !b.LoggedAt.IsZero()
	if aLogged != bLogged {
		return aLogged
	}
	if aLogged && !a.LoggedAt.Equal(b.LoggedAt) {
		return a.LoggedAt.Before(b.LoggedAt)
	}
	return a.BundleDigest < b.BundleDigest
}

// keyedSignerIdentity is the same-signer identity for a keyed verification:
// a keyed bundle carries no certificate, so the pinned public key itself is
// the signer. A stable fingerprint of the pinned key (the sha256 of its PEM)
// names that signer so two keyed bundles signed under the same pinned key
// compare equal, and two under different keys compare unequal.
func keyedSignerIdentity(publicKeyPEM string) string {
	sum := sha256.Sum256([]byte(publicKeyPEM))
	return fmt.Sprintf("keyed:sha256:%x", sum)
}

// anchorFor builds the keyless trust anchor a verification under policy checks a
// signature against.
type anchorFor func(cfg workspaceconfig.Config, policy workspaceconfig.RegistryConfig) (root.TrustedMaterial, error)

// keylessAnchor builds the keyless trust anchor for the policy: a pinned
// trust root (air-gapped, offline) when policy.TrustedRoot is set,
// otherwise the live public-good root fetched via TUF.
//
// LIVE-ONLY for the no-pinned-root branch: with no pinned root, the keyless
// anchor is the real public-good Sigstore trust root fetched and cached via
// TUF — the one network-touching branch of the verify pass — obtained through
// the network-egress gate, so an offline workspace refuses the fetch loudly
// before any dial. An air-gapped deployment pins a trusted_root and never
// reaches the live branch; the pinned-root branch and the verification logic
// that consumes either anchor are fully exercised offline.
//
// It is keylessAnchorContext for a verification whose caller holds no context.
func keylessAnchor(cfg workspaceconfig.Config, policy workspaceconfig.RegistryConfig) (root.TrustedMaterial, error) {
	return keylessAnchorContext(context.Background())(cfg, policy)
}

// keylessAnchorContext is keylessAnchor for a verification made under ctx: the
// pinned trust root is read exactly as keylessAnchor reads it, and the live one is
// taken through livetrust.TrustedRootWithin, which shares one fetch among the
// verifications under a context carrying a shared root and otherwise fetches as
// livetrust.TrustedRoot does — so under a context carrying none, it is
// keylessAnchor. Only the shared root is read from ctx: cancelling ctx neither
// ends a fetch under way nor refuses one about to start.
func keylessAnchorContext(ctx context.Context) anchorFor {
	return func(cfg workspaceconfig.Config, policy workspaceconfig.RegistryConfig) (root.TrustedMaterial, error) {
		if policy.TrustedRoot != "" {
			return signing.TrustedMaterialFromRootPath(policy.TrustedRoot)
		}
		return livetrust.TrustedRootWithin(ctx, cfg)
	}
}

// blobDigest returns the sha256:... digest of b — the content address of
// the cached bundle blob.
func blobDigest(b []byte) string {
	sum := sha256.Sum256(b)
	return fmt.Sprintf("sha256:%x", sum)
}

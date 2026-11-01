// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package docsbundle names the documentation bundle, acquires it into the
// machine-wide cache every surface on the machine reads it from, and holds each
// read of it to the reading workspace's own signature check.
//
// It is the one home for what every surface that serves documentation needs and
// none of them should decide for itself: where the bundle is published, where it
// is kept, how it is fetched and verified, which check a read of it answers to,
// and how long one attempt may take. A surface that resolved its own reference
// would be a second spelling of the artifact's address, and two surfaces reading
// two addresses would disagree about what "the documentation" is on the same
// machine; a surface that kept its own copy would download what another already
// holds.
//
// # Where the bundle is kept, and why every reader checks it again
//
// The bundle's bytes depend only on the registry it is published to and the
// version it is published at — never on a workspace — so it is kept once per
// machine, under the root every machine-wide cache derives from, keyed by
// reference and version. Which bundle a workspace reads is still that workspace's
// decision: its default registry names the reference, and its egress settings
// decide whether a fetch may happen at all.
//
// Workspaces on one machine need not trust the same signers, so a copy one
// workspace fetched is not thereby admitted for another. The slot keeps the
// signature bundles its fetches verified, and no page is served until they have
// been verified under the reading workspace's own check for the bundle's registry
// ([Admission]) — once per process for each copy and each check, the admission
// remembered for the reads after it. A copy that check refuses is answered as no
// bundle, and the next acquisition fetches it again under that check.
//
// That verification reads only what is on this machine, with one exception: online,
// a keyless check that pins no trust root obtains the public Sigstore trust root
// over the network. Offline it verifies instead against the trust root an earlier
// online fetch left on this machine (livetrust.LastFetchedRootFile), so the
// documentation stays readable offline under the check the workspace asks for; a
// machine that never fetched one has nothing to verify against, and the read is
// refused.
//
// The read half is deliberately not here. Reading a page out of the cache is
// pkg/docscache's job and takes no target, so a surface that only reads never
// links the acquisition path at all.
package docsbundle

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress/livetrust"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

const (
	// Namespace is the reserved registry namespace standalone data artifacts
	// publish under.
	Namespace = "docs"

	// Name is the artifact name of the documentation bundle within that
	// namespace.
	Name = "aggrega-docs"
)

// AcquireTimeout bounds one acquisition attempt.
//
// The egress transport carries no deadline of its own, so without a bound an
// unreachable registry would hold the handshake open for as long as the operating
// system's connect timeout allows. It is generous enough for a cold pull over a
// slow link and short enough that a dead registry costs a pause rather than a
// session.
const AcquireTimeout = 30 * time.Second

// Ref returns the documentation bundle's reference within the given registry
// location prefix: a registry host, followed by the repository path the default
// registry carries, if any — where a data-artifact publication through the same
// default places it.
func Ref(prefix string) string {
	return prefix + "/" + Namespace + "/" + Name
}

// cacheRootKey is the private context key under which a caller states the cache
// root the documentation bundle is kept under, in place of the process's own.
type cacheRootKey struct{}

// declaredRoot is a stated cache root; an empty one states that none resolves.
type declaredRoot struct{ root string }

// WithCacheRoot returns ctx carrying root as the cache root the documentation
// bundle is kept under, in place of the one this process keeps every
// machine-wide cache under. An empty root states that no cache root resolves.
//
// No production path sets it: every surface then keeps the bundle under the
// process's own root. A test sets it on the context it drives a surface with, so
// that tests serving different fixture bundles under one reference and version
// never share a slot — the process's own root is shared by every test process on
// the machine — and so that a test of a machine with no cache root can state one.
func WithCacheRoot(ctx context.Context, root string) context.Context {
	return context.WithValue(ctx, cacheRootKey{}, declaredRoot{root: root})
}

// CacheRoot returns the cache root the documentation bundle is kept under: the
// one ctx states ([WithCacheRoot]), or else the root every machine-wide cache this
// process keeps derives from. It is never derived a second way, so the bundle,
// the compiled-module cache and the plugin-describe cache always sit beside each
// other.
//
// When no root resolves the error says what was missing, in words a reader can
// act on, and nothing is acquired or read: there is nowhere to keep the bundle.
func CacheRoot(ctx context.Context) (string, error) {
	if d, ok := ctx.Value(cacheRootKey{}).(declaredRoot); ok {
		if d.root == "" {
			return "", noCacheRoot(errors.New("none was stated for this run"))
		}
		return d.root, nil
	}
	if root, ok := wasm.AggregaCacheRoot(); ok {
		return root, nil
	}
	return "", noCacheRoot(wasm.AggregaCacheRootError())
}

// noCacheRoot states why the documentation bundle can be neither acquired nor
// read: it is kept in the per-user cache directory, and none resolves.
func noCacheRoot(reason error) error {
	return fmt.Errorf("the documentation bundle is kept in the per-user cache directory shared by every "+
		"project on this machine, and none resolves here (%v)", reason)
}

// TargetFor resolves the OCI target the documentation bundle is pulled from and
// the pull-time signature check to run against it.
//
// It is deliberately the narrowest seam that removes the registry: everything
// after it — the cache-presence probe, the pull, the media-type check, the
// verification, the cache write and the read — is the production path, so a test
// substituting a target still exercises all of it. A seam one level higher, over
// the whole acquisition, would take the cache write out of the tested path and
// leave the trigger proven against nothing. A test that wants the production
// verification over its own target builds the check with [Verifier].
type TargetFor func(cfg workspaceconfig.Config, ref, version string, warn func(string)) (oras.ReadOnlyTarget, docscache.VerifyAtPull, error)

// targetKey is the private context key under which a caller states the target an
// acquisition reaches, in place of the registry.
type targetKey struct{}

// WithTarget returns ctx carrying target as the resolution an acquisition that
// names none uses, in place of [RegistryTarget].
//
// No production path sets it. A test sets it on the context it drives a surface
// with, so a surface whose acquisition names no target of its own — the command
// line, the language server — reaches a registry the test controls and counts.
func WithTarget(ctx context.Context, target TargetFor) context.Context {
	return context.WithValue(ctx, targetKey{}, target)
}

// trustRootCacheKey is the private context key under which a caller states the
// directory the TUF client's cache is read from, in place of the one this
// machine's client keeps.
type trustRootCacheKey struct{}

// WithTrustRootCache returns ctx carrying dir as the TUF client cache directory a
// read of the documentation bundle takes the last fetched public trust root from
// while offline (livetrust.LastFetchedRootFile), in place of the one this
// machine's client keeps. An empty dir states nothing.
//
// No production path sets it: every surface then reads the client's own cache. A
// test sets it on the context it drives a surface with, so it can stand in for an
// earlier online fetch — or for a machine that never made one — without reading or
// writing the developer's own cache.
func WithTrustRootCache(ctx context.Context, dir string) context.Context {
	return context.WithValue(ctx, trustRootCacheKey{}, dir)
}

// trustRootCache returns the TUF client cache directory ctx states, or "" for the
// one this machine's client keeps.
func trustRootCache(ctx context.Context) string {
	dir, _ := ctx.Value(trustRootCacheKey{}).(string)
	return dir
}

// Declarations is what a context states about the documentation bundle — a
// cache root ([WithCacheRoot]), a target ([WithTarget]) and a trust-root cache
// ([WithTrustRootCache]) — taken off one context so it can be stated on another.
//
// It exists for a surface whose per-request contexts do not descend from the
// context it was started with — the language server derives each request's
// context from one of its own, so that withdrawing the launch drains rather
// than aborts — and which must still answer under what its launch stated. The
// zero value states nothing, which is every production launch.
type Declarations struct {
	root           *declaredRoot
	target         TargetFor
	trustRootCache string
}

// DeclarationsOf returns what ctx states about the documentation bundle.
func DeclarationsOf(ctx context.Context) Declarations {
	var d Declarations
	if root, ok := ctx.Value(cacheRootKey{}).(declaredRoot); ok {
		d.root = &root
	}
	d.target, _ = ctx.Value(targetKey{}).(TargetFor)
	d.trustRootCache = trustRootCache(ctx)
	return d
}

// Onto returns ctx stating what d states, and ctx itself where d states nothing.
func (d Declarations) Onto(ctx context.Context) context.Context {
	if d.root != nil {
		ctx = context.WithValue(ctx, cacheRootKey{}, *d.root)
	}
	if d.target != nil {
		ctx = context.WithValue(ctx, targetKey{}, d.target)
	}
	if d.trustRootCache != "" {
		ctx = WithTrustRootCache(ctx, d.trustRootCache)
	}
	return ctx
}

// RegistryTarget is the production resolution. The workspace's egress gate
// supplies the transport and refuses outright when the workspace is offline; the
// shared OCI constructor supplies the authenticated repository, so credentials
// and the loopback plain-HTTP decision are read from the one place that owns
// them.
//
// The verifier it returns is the same pull-time signature check a catalog entity
// or a plugin gets ([Verifier]). Returning none would have been simpler and would
// have made a documentation bundle the one artifact class that bypasses that
// policy.
func RegistryTarget(cfg workspaceconfig.Config, ref, version string, warn func(string)) (oras.ReadOnlyTarget, docscache.VerifyAtPull, error) {
	httpClient, err := egress.HTTPClient(cfg)
	if err != nil {
		return nil, nil, err
	}
	repo, err := oci.NewRemoteRepository(ref, httpClient)
	if err != nil {
		return nil, nil, err
	}
	return repo, Verifier(cfg, ref, version, repo, warn), nil
}

// bundleEntity is the documentation bundle as a signature check sees it: its
// reference, the manifest digest a signature commits to, and the registry host
// its trust policy is looked up under. It carries no catalog kind — the bundle
// publishes under none — and the fetch and every read build it here, so the two
// ask the configuration the same question.
func bundleEntity(ref, manifestDigest string) pullverify.Entity {
	return pullverify.Entity{
		URI:            ref,
		ManifestDigest: manifestDigest,
		Host:           registryhost.Of(ref),
	}
}

// Verifier returns the pull-time signature check for the documentation bundle at
// (ref, version) on repo, run against cfg's own per-registry trust policy through
// pullverify — the check a catalog entity or a plugin gets — and handing back the
// signature bundle it verified, which the cache slot keeps for later readers.
func Verifier(cfg workspaceconfig.Config, ref, version string, repo oras.ReadOnlyTarget, warn func(string)) docscache.VerifyAtPull {
	return func(ctx context.Context, manifestDigest string) ([][]byte, error) {
		results, err := pullverify.Verify(ctx, []pullverify.Entity{bundleEntity(ref, manifestDigest)}, pullverify.Options{
			Config: cfg,
			Target: func(pullverify.Entity) (oras.ReadOnlyTarget, string, error) {
				return repo, version, nil
			},
			Warn: warn,
		})
		if err != nil {
			return nil, err
		}
		var verified [][]byte
		for _, r := range results {
			verified = append(verified, r.Bundle)
		}
		return verified, nil
	}
}

// Admission returns the check a read of the documentation bundle at ref is held
// to for a workspace configured as cfg: that workspace's own signature check for
// the bundle's registry, run over the signature bundles the cache slot keeps and
// against the digest of the manifest it stores, through pullverify.VerifyStored.
//
// One thing about it differs from the fetch, and only offline: a keyless check
// that pins no trust root verifies against the public trust root this machine
// last fetched, where the fetch would obtain the live one and cannot offline
// (livetrust.LastFetchedRootFile, from the TUF client cache ctx states with
// [WithTrustRootCache], or else the machine's own). A machine that never fetched
// one is refused, and says the check could not run offline. Online the check is
// the fetch's in every respect.
//
// It asks the configuration exactly what the fetch asks it ([Verifier]), so a
// setting that admits the bundle at a fetch admits the kept copy at a read and one
// that refuses it there refuses it here: a skip admits; a kept signature that
// verifies admits; a copy keeping none is admitted exactly when the check does not
// halt without a signature; and an advisory registry's failure is reported on warn
// and admitted. A copy the check refuses is answered as no bundle.
//
// The check runs once per process for each copy and each check: an admission is
// remembered under everything the verdict is decided by
// (pullverify.StoredVerdictKey) — the copy, by its manifest digest and the exact
// set of signatures it keeps, and the check, together with the offline posture and
// the bytes of the trust-root file a keyless check verifies against — so a later
// read of the same copy under the same check is admitted without verifying again,
// and reports what the check reported when it ran. A copy fetched since, a
// different check, the same check read offline, or an offline read finding a
// different trust root held is verified afresh. A refusal is never remembered: a
// fetch can write back the very copy a read refused — the same manifest, the same
// signatures — when the refusal came of a trust root that could not be obtained at
// that moment, and a remembered refusal would go on refusing what that fetch
// admitted.
func Admission(ctx context.Context, cfg workspaceconfig.Config, ref string, warn func(string)) docscache.AdmitAtRead {
	if warn == nil {
		warn = func(string) {}
	}
	cache := trustRootCache(ctx)
	held := func() (string, error) { return livetrust.LastFetchedRootFile(cfg, cache) }
	return func(manifestDigest string, signatures [][]byte) error {
		e := bundleEntity(ref, manifestDigest)
		verify := func(warn func(string)) error {
			admissionRuns.Add(1)
			_, err := pullverify.VerifyStored(cfg, e, signatures, held, warn)
			return err
		}
		key, ok := pullverify.StoredVerdictKey(cfg, e, signatures, held)
		if !ok {
			return verify(warn)
		}
		return admissions.admit(key, verify, warn)
	}
}

// admissionRuns counts the read-time checks Admission actually ran, rather than
// answered from what it remembered. It is test instrumentation and nothing reads it
// in production: it lets a test assert how many verifications a sequence of reads
// cost without timing anything.
var admissionRuns atomic.Int64

// admissions is what this process remembers of the read-time checks that admitted
// a copy of the documentation bundle. One holds for every surface the process
// serves, because every surface reads the bundle through Admission.
var admissions = &admissionMemo{verdicts: map[string]*admissionVerdict{}}

// admissionMemo remembers admissions by the key of everything they were decided by.
// It is safe for concurrent use: the assistant server answers requests on
// goroutines of their own.
type admissionMemo struct {
	mu       sync.Mutex
	verdicts map[string]*admissionVerdict
}

// admissionVerdict is one check's outcome, shared by every read that arrives while
// it runs and kept afterwards when it admitted. done is closed once err and
// notices are final.
type admissionVerdict struct {
	done    chan struct{}
	err     error
	notices []string
}

// errVerdictNotReached is the refusal a read meets when the check it waited on
// stopped without deciding.
var errVerdictNotReached = errors.New("the signature check stopped before it decided")

// admit answers a read under key: from the verdict remembered or being reached for
// it, or else by running verify once and sharing its outcome with every read that
// arrived meanwhile. The notices the check reported reach warn on every read, the
// remembered ones included. A refusal is answered and then forgotten, so the next
// read under key runs the check again.
func (m *admissionMemo) admit(key string, verify func(warn func(string)) error, warn func(string)) error {
	m.mu.Lock()
	v, reached := m.verdicts[key]
	if !reached {
		v = &admissionVerdict{done: make(chan struct{})}
		m.verdicts[key] = v
	}
	m.mu.Unlock()

	if reached {
		<-v.done
	} else {
		m.decide(key, v, verify)
	}
	for _, notice := range v.notices {
		warn(notice)
	}
	return v.err
}

// decide runs verify for v, and forgets v unless it admitted — including when
// verify stops without returning, so a check that never decided is never taken
// for one that admitted.
func (m *admissionMemo) decide(key string, v *admissionVerdict, verify func(warn func(string)) error) {
	v.err = errVerdictNotReached
	defer func() {
		if v.err != nil {
			m.mu.Lock()
			delete(m.verdicts, key)
			m.mu.Unlock()
		}
		close(v.done)
	}()
	v.err = verify(func(notice string) { v.notices = append(v.notices, notice) })
}

// EnsureCached acquires the documentation bundle for (ref, version) into the
// cache under cacheRoot unless that cache already holds a copy the workspace
// configured as cfg admits, and reports whether it acquired one.
//
// A copy already cached that the workspace's check admits is left exactly as it
// is and reported as not acquired, so a caller can distinguish a warm cache from a
// fetch without probing again; the check runs before the target is resolved, so a
// warm cache reaches for no registry at all. A copy the check refuses is fetched
// again under that same check, and the fetched copy replaces it.
//
// A nil target selects the one ctx states ([WithTarget]), or else
// [RegistryTarget]. The attempt is bounded by [AcquireTimeout], and a failure
// leaves the cache exactly as it was — the pull verifies content and signature
// before anything is written, so a bundle that cannot be verified never populates
// a slot. Where the failed fetch was a second attempt at a copy the check refused,
// the error says both: why the copy here was refused, and why fetching it again
// failed.
//
// The error is returned rather than swallowed, and what to do with it is the
// caller's decision: a session that serves documentation as one capability among
// many logs it and carries on, while a command whose whole answer is a page has
// to say so.
func EnsureCached(ctx context.Context, cfg workspaceconfig.Config, cacheRoot, ref, version string,
	target TargetFor, warn func(string)) (bool, error) {
	if warn == nil {
		warn = func(string) {}
	}
	held := docscache.Admitted(cacheRoot, ref, version, Admission(ctx, cfg, ref, warn))
	if held == nil {
		return false, nil
	}
	var refused *docscache.NotAdmittedError
	if !errors.As(held, &refused) && !errors.Is(held, docscache.ErrFetchNotFound()) {
		return false, held
	}
	repo, verify, err := targetOf(ctx, target)(cfg, ref, version, warn)
	if err != nil {
		return false, alsoRefused(refused, err)
	}
	ctx, cancel := context.WithTimeout(ctx, AcquireTimeout)
	defer cancel()
	if _, err := docscache.ResolveAndCache(ctx, repo, ref, version, cacheRoot, verify); err != nil {
		return false, alsoRefused(refused, fmt.Errorf("acquire documentation bundle %s@%s: %w", ref, version, err))
	}
	return true, nil
}

// targetOf returns the target an acquisition reaches: target where it names one,
// else the one ctx states ([WithTarget]), else [RegistryTarget].
func targetOf(ctx context.Context, target TargetFor) TargetFor {
	if target != nil {
		return target
	}
	if stated, _ := ctx.Value(targetKey{}).(TargetFor); stated != nil {
		return stated
	}
	return RegistryTarget
}

// EnsureCachedAtOrBelow acquires the documentation bundle for (ref, version) as
// [EnsureCached] does and, where none is published at that version, the newest
// one published at or below it, and returns the version it holds.
//
// The version asked for is tried first, exactly as EnsureCached tries it, so a
// copy cached for it is used without reaching a registry and a reader whose
// version is published reads exactly that one. Only when the registry answers
// that it publishes no bundle at that version are the versions it does publish
// listed, through the same target and the same egress, and the newest at or below
// the version asked for is acquired in turn — under the same signature check, from
// the cache where a copy of it is held. Offline, where no registry can be asked,
// the newest version at or below it that the cache holds a copy of that the
// workspace's check admits is used instead; with none, the offline refusal is
// returned unchanged.
//
// "At or below" reads the version the way a release is numbered. A release
// version X.Y.Z bounds the choice inclusively. A version a build describes
// itself by as some commits after a release — X.Y.Z-N-gHASH, with or without a
// trailing -dirty — is taken as built after X.Y.Z, so X.Y.Z bounds it, rather
// than as a pre-release of X.Y.Z, which would sort before it. Any other
// pre-release version bounds the choice strictly, and a pre-release is never
// chosen. A version that is not a release number at all — a development build
// stamped with none — has nothing to be at or below, and the failure to acquire
// the exact version is returned as it is.
//
// The acquired reports whether a fetch happened, as EnsureCached reports it.
func EnsureCachedAtOrBelow(ctx context.Context, cfg workspaceconfig.Config, cacheRoot, ref, version string,
	target TargetFor, warn func(string)) (held string, acquired bool, err error) {
	acquired, err = EnsureCached(ctx, cfg, cacheRoot, ref, version, target, warn)
	if err == nil {
		return version, acquired, nil
	}
	bound, ok := versionBound(version)
	if !ok {
		return "", false, err
	}
	switch {
	case errors.Is(err, egress.ErrOfflineEgress()):
		if cached, ok := newestCachedAtOrBelow(ctx, cfg, cacheRoot, ref, bound, warn); ok {
			return cached, false, nil
		}
		return "", false, err
	case errors.Is(err, docscache.ErrFetchNotFound()):
		published, lerr := publishedVersions(ctx, cfg, ref, version, target, warn)
		if lerr != nil {
			return "", false, fmt.Errorf("%w; listing the versions the registry publishes failed: %w", err, lerr)
		}
		chosen := atOrBelow(published, bound)
		if chosen == "" {
			return "", false, fmt.Errorf("%w; the registry publishes no version of it at or below %s", err, bound)
		}
		acquired, err = EnsureCached(ctx, cfg, cacheRoot, ref, chosen, target, warn)
		if err != nil {
			return "", false, err
		}
		return chosen, acquired, nil
	}
	return "", false, err
}

// describedBuild matches a version a build describes itself by as some commits
// after a release: the release, the count of commits since it, and the commit
// the build was made from, marked when the tree it was built from held changes.
var describedBuild = regexp.MustCompile(`^(\d+\.\d+\.\d+)-\d+-g[0-9a-f]+(-dirty)?$`)

// versionBound returns the release number version is at or below — version
// itself, or the release a described build was made after — and whether version
// is a release number at all.
func versionBound(version string) (string, bool) {
	if m := describedBuild.FindStringSubmatch(version); m != nil {
		return m[1], true
	}
	if oci.HasAnySemverTag([]string{version}) {
		return version, true
	}
	return "", false
}

// atOrBelow returns the version among versions equal to bound, else the newest
// release strictly below it, else "".
func atOrBelow(versions []string, bound string) string {
	for _, v := range versions {
		if v == bound {
			return v
		}
	}
	return oci.SelectHighestBelow(versions, bound)
}

// publishedVersions lists the versions the registry publishes the bundle at,
// through the target an acquisition reaches, within one acquisition's bound.
func publishedVersions(ctx context.Context, cfg workspaceconfig.Config, ref, version string,
	target TargetFor, warn func(string)) ([]string, error) {
	repo, _, err := targetOf(ctx, target)(cfg, ref, version, warn)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, AcquireTimeout)
	defer cancel()
	return oci.ListTags(ctx, repo)
}

// newestCachedAtOrBelow returns the newest version at or below bound that the
// cache under cacheRoot holds a copy of for ref that the workspace configured as
// cfg admits, and whether there is one.
func newestCachedAtOrBelow(ctx context.Context, cfg workspaceconfig.Config, cacheRoot, ref, bound string,
	warn func(string)) (string, bool) {
	versions, err := docscache.CachedVersions(cacheRoot, ref)
	if err != nil {
		return "", false
	}
	admit := Admission(ctx, cfg, ref, warn)
	for len(versions) > 0 {
		v := atOrBelow(versions, bound)
		if v == "" {
			return "", false
		}
		if docscache.Admitted(cacheRoot, ref, v, admit) == nil {
			return v, true
		}
		versions = slices.DeleteFunc(versions, func(s string) bool { return s == v })
	}
	return "", false
}

// alsoRefused reports a failed fetch, together with the refusal of the cached copy
// that sent the acquisition to fetch, where there was one. Both stay matchable:
// the refusal answers as no bundle, and the fetch's own failure keeps its
// identity.
func alsoRefused(refused *docscache.NotAdmittedError, fetchErr error) error {
	if refused == nil {
		return fetchErr
	}
	return fmt.Errorf("%w; fetching it again under this workspace's own signature check failed: %w", refused, fetchErr)
}

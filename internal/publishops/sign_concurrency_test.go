// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package publishops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing/signingtest"
)

// signLiveness is a LIVENESS CEILING, not a performance budget: it separates a
// batch that returns, or a signature that starts, from one that never does, so a
// defect that deadlocks fails the test rather than hanging the run. Nothing here
// measures how long anything took.
const signLiveness = 2 * time.Minute

// heldSigner is a test-only signing.Signer that is safe for concurrent use and
// lets the test decide when each call returns.
//
// A call whose digest has a gate blocks until the test closes that gate, after
// announcing itself on started; that is how the test holds several signatures in
// flight at once and chooses the order they finish in. A call whose digest has no
// gate returns at once. fail names the digests whose signature fails.
//
// inFlight and peak are test-only instrumentation: peak is the most calls ever
// inside Sign at the same moment, read through peakOf rather than through any
// production interface.
type heldSigner struct {
	mu       sync.Mutex
	gates    map[string]chan struct{}
	fail     map[string]error
	started  chan string
	inFlight int
	peak     int
	signed   []string
	returned int
}

func (h *heldSigner) Sign(_ context.Context, digest string) ([]byte, string, error) {
	h.mu.Lock()
	h.inFlight++
	if h.inFlight > h.peak {
		h.peak = h.inFlight
	}
	h.signed = append(h.signed, digest)
	gate := h.gates[digest]
	failure := h.fail[digest]
	h.mu.Unlock()

	if gate != nil {
		h.started <- digest
		<-gate
	}

	h.mu.Lock()
	h.inFlight--
	h.returned++
	h.mu.Unlock()
	if failure != nil {
		return nil, "", failure
	}
	return []byte(fmt.Sprintf(`{"held-signature-over":%q}`, digest)), signing.BundleMediaType, nil
}

var _ signing.Signer = (*heldSigner)(nil)

// peakOf reads the most signatures the signer ever had in flight at once.
func peakOf(h *heldSigner) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.peak
}

// signedOf reads the digests the signer was asked to sign, in the order asked.
func signedOf(h *heldSigner) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.signed...)
}

// returnedOf reads how many calls have returned.
func returnedOf(h *heldSigner) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.returned
}

// batchFixture is n published versions in one in-memory registry, each its own
// artifact, with the references a batch signs them under, in order.
type batchFixture struct {
	store    *memory.Store
	refs     []string
	subjects map[string]string
	digests  []string
}

func newBatchFixture(t *testing.T, n int) batchFixture {
	t.Helper()
	f := batchFixture{store: memory.New(), subjects: map[string]string{}}
	for i := range n {
		tag := fmt.Sprintf("1.%d.0", i)
		digest, err := oci.Push(context.Background(), f.store, tag, oci.MediaTypeComponent,
			[]byte(fmt.Sprintf("# published version %d\n", i)), nil)
		if err != nil {
			t.Fatalf("Push %s: %v", tag, err)
		}
		ref := fmt.Sprintf("%s/component/core/c%d:%s", datedRegistry, i, tag)
		f.refs = append(f.refs, ref)
		f.subjects[ref] = tag
		f.digests = append(f.digests, digest)
	}
	return f
}

// heldOver gates every reference in the fixture.
func (f batchFixture) heldOver() *heldSigner {
	h := &heldSigner{gates: map[string]chan struct{}{}, started: make(chan string)}
	for _, d := range f.digests {
		h.gates[d] = make(chan struct{})
	}
	return h
}

// config is the batch over the whole fixture, signed by signer at the bound jobs,
// in a workspace pinning no trust anchor, so no reference is judged already signed.
func (f batchFixture) config(t *testing.T, signer signing.Signer, jobs int, out io.Writer) SignConfig {
	t.Helper()
	return SignConfig{
		Workspace: resolveIn(t, "workspace: {}\n", nil), References: f.refs, SubjectRefs: f.subjects,
		Target: f.store, Signer: signer, IDToken: "supplied-token", EnvLookup: func(string) string { return "" },
		Out: out, Jobs: jobs,
	}
}

// attachedLines is the account a batch signing every reference in order writes.
func (f batchFixture) attachedLines() string {
	var b strings.Builder
	for i, ref := range f.refs {
		fmt.Fprintf(&b, "Signature attached: %s (%s)\n", ref, f.digests[i])
	}
	return b.String()
}

// batchOutcome is what one run of Sign came to.
type batchOutcome struct {
	res SignResult
	err error
}

// driveHeldBatch runs Sign over a fixture whose every signature is held, and
// finishes the signatures in the reverse of the order they started whenever more
// than one is in flight: it waits until the batch holds as many signatures as its
// bound allows — min(bound, references not yet finished) — then finishes the one
// that started last. So at a bound above one the references finish out of the
// order they were given, and at every step the batch has been given the chance to
// fill its bound.
func driveHeldBatch(t *testing.T, cfg SignConfig, h *heldSigner, bound int) batchOutcome {
	t.Helper()
	done := make(chan batchOutcome, 1)
	go func() {
		res, err := Sign(context.Background(), cfg)
		done <- batchOutcome{res, err}
	}()
	deadline := time.After(signLiveness)
	n := len(cfg.References)
	var pending []string
	for finished := 0; finished < n; finished++ {
		for len(pending) < min(bound, n-finished) {
			select {
			case d := <-h.started:
				pending = append(pending, d)
			case o := <-done:
				t.Fatalf("the batch returned with %d of %d signatures finished (err %v)", finished, n, o.err)
			case <-deadline:
				t.Fatalf("liveness ceiling: %d signature(s) in flight, waiting for %d; the batch stopped starting references",
					len(pending), min(bound, n-finished))
			}
		}
		last := pending[len(pending)-1]
		pending = pending[:len(pending)-1]
		close(h.gates[last])
	}
	select {
	case o := <-done:
		return o
	case <-deadline:
		t.Fatal("liveness ceiling: every signature finished and the batch never returned")
	}
	return batchOutcome{}
}

// TestASigningBatchSignsNoMoreReferencesAtOnceThanItsBound holds every signature
// until the test finishes it, and reads the most signatures the batch ever had in
// flight at once. It never exceeds the bound, and it reaches the bound wherever the
// batch has that many references — or every reference, where it has fewer. A zero
// bound is one.
func TestASigningBatchSignsNoMoreReferencesAtOnceThanItsBound(t *testing.T) {
	t.Parallel()
	const references = 6
	for _, row := range []struct {
		name     string
		jobs     int
		wantPeak int
		defect   string
	}{
		{"a bound of one signs one after another", 1, 1,
			"the default signs several references at once, reaching the public signing services harder than asked"},
		{"a zero bound is one", 0, 1,
			"an absent bound signs without limit, or signs nothing"},
		{"a bound of four holds four in flight", 4, 4,
			"the batch signs more at once than its bound, or never reaches it"},
		{"a bound above the reference count signs every reference at once", references + 4, references,
			"a bound above the batch's size is not reached by every reference, or more calls run than there are references"},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			f := newBatchFixture(t, references)
			h := f.heldOver()
			var out bytes.Buffer
			o := driveHeldBatch(t, f.config(t, h, row.jobs, &out), h, row.wantPeak)
			if o.err != nil {
				t.Fatalf("Sign: %v (defect: %s)", o.err, row.defect)
			}
			if got := peakOf(h); got != row.wantPeak {
				t.Errorf("the batch had at most %d signature(s) in flight at once; want %d (defect: %s)", got, row.wantPeak, row.defect)
			}
			if got := len(signedOf(h)); got != references {
				t.Errorf("the signer was asked %d times; want once per reference, %d (defect: %s)", got, references, row.defect)
			}
		})
	}
}

// TestASigningBatchAnswersInTheOrderItsReferencesWereGiven finishes the
// signatures out of order — the last started first, whenever more than one is in
// flight — and requires the account, the signed list and every attached signature
// to come back as if the references had been signed one after another, in the
// order given, with no reference's line split by another's.
func TestASigningBatchAnswersInTheOrderItsReferencesWereGiven(t *testing.T) {
	t.Parallel()
	for _, jobs := range []int{1, 4} {
		t.Run(fmt.Sprintf("a bound of %d", jobs), func(t *testing.T) {
			t.Parallel()
			f := newBatchFixture(t, 6)
			h := f.heldOver()
			var out bytes.Buffer
			o := driveHeldBatch(t, f.config(t, h, jobs, &out), h, jobs)
			if o.err != nil {
				t.Fatalf("Sign: %v", o.err)
			}
			// The control for the bound above one: the drive finishes signatures out of
			// order only while two or more are in flight, so a row that never had two
			// in flight would pass without proving anything.
			if got := peakOf(h); jobs > 1 && got < 2 {
				t.Fatalf("only %d signature in flight at once, so nothing finished out of order and this row proves nothing", got)
			}
			if got, want := out.String(), f.attachedLines(); got != want {
				t.Errorf("the account is not in the order the references were given:\ngot:\n%s\nwant:\n%s", got, want)
			}
			if len(o.res.Signed) != len(f.refs) {
				t.Fatalf("the batch reported %d signed; want %d: %+v", len(o.res.Signed), len(f.refs), o.res.Signed)
			}
			for i, ref := range f.refs {
				want := SignedArtifact{Reference: ref, Digest: f.digests[i], Attached: true}
				if o.res.Signed[i] != want {
					t.Errorf("signed[%d] = %+v; want %+v", i, o.res.Signed[i], want)
				}
				if n := countReferrers(t, f.store, f.subjects[ref], signing.BundleMediaType); n != 1 {
					t.Errorf("%s carries %d signatures; want 1", ref, n)
				}
			}
		})
	}
}

// TestASigningBatchAtABoundAboveOneStillContinuesAndReports signs, four at once,
// a batch holding one reference already carrying a signature its registry's policy
// verifies, one whose signature fails and two that sign. Every outcome is
// reported: the failure is named and nothing is attached to it, the already-signed
// reference is reported as such with nothing attached and its signer never asked,
// and the two others are each signed once — in an account ordered as the
// references were given.
func TestASigningBatchAtABoundAboveOneStillContinuesAndReports(t *testing.T) {
	t.Parallel()
	k := newCompletionKeys(t)
	f := newBatchFixture(t, 4)
	const alreadySigned, failing = 1, 2

	// The reference already signed carries a keyed signature its registry's policy
	// pins the key for.
	pemBytes, err := os.ReadFile(k.keyPath)
	if err != nil {
		t.Fatalf("read key: %v", err)
	}
	keyed, err := signing.LoadKeyedSigner(pemBytes, nil)
	if err != nil {
		t.Fatalf("LoadKeyedSigner: %v", err)
	}
	bundle, artifactType, err := keyed.Sign(context.Background(), f.digests[alreadySigned])
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if _, err := oci.AttachReferrer(context.Background(), f.store, f.subjects[f.refs[alreadySigned]], artifactType, bundle); err != nil {
		t.Fatalf("attach the existing signature: %v", err)
	}

	h := &heldSigner{fail: map[string]error{f.digests[failing]: errors.New("the test's signature fails here")}}
	var out bytes.Buffer
	cfg := f.config(t, h, 4, &out)
	cfg.Workspace = resolveIn(t, keyedWS(k.pubPEM), nil)
	_, err = Sign(context.Background(), cfg)

	if err == nil {
		t.Fatal("a batch with a failing reference returned no error")
	}
	if !strings.Contains(err.Error(), "1 of 4 references failed:") || !strings.Contains(err.Error(), f.refs[failing]) ||
		!strings.Contains(err.Error(), "the test's signature fails here") {
		t.Errorf("the error does not name the one failure with its cause: %v", err)
	}
	for i, ref := range f.refs {
		want := 1
		if i == failing {
			want = 0
		}
		if n := countReferrers(t, f.store, f.subjects[ref], signing.BundleMediaType); n != want {
			t.Errorf("%s carries %d signatures; want %d", ref, n, want)
		}
	}
	asked := signedOf(h)
	if len(asked) != 3 {
		t.Errorf("the signer was asked %d times; want 3 — every reference but the one already signed", len(asked))
	}
	for _, d := range asked {
		if d == f.digests[alreadySigned] {
			t.Errorf("the signer was asked to sign the reference already carrying a signature that verifies")
		}
	}

	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	wantPrefixes := []string{
		"Signature attached: " + f.refs[0] + " ",
		"Already signed: " + f.refs[alreadySigned] + " (" + f.digests[alreadySigned] + ") by ",
		"Signature attached: " + f.refs[3] + " ",
	}
	if len(lines) != len(wantPrefixes) {
		t.Fatalf("the account carries %d line(s); want %d:\n%s", len(lines), len(wantPrefixes), out.String())
	}
	for i, prefix := range wantPrefixes {
		if !strings.HasPrefix(lines[i], prefix) {
			t.Errorf("line %d = %q; want it to start %q (the account must follow the order the references were given)", i, lines[i], prefix)
		}
	}
	if !strings.HasSuffix(lines[1], "; nothing attached") {
		t.Errorf("the already-signed line does not say nothing was attached: %q", lines[1])
	}
}

// TestAWithdrawnBatchStartsNoFurtherReference withdraws a batch, signing two at
// once, while its first two signatures are in flight. No further reference is
// started; the two in flight run to their own end and the batch waits for them
// before it returns; and it reports the stop, with only references among those two
// as signed. The control runs the same batch to its end.
func TestAWithdrawnBatchStartsNoFurtherReference(t *testing.T) {
	t.Parallel()
	const references, jobs = 5, 2

	t.Run("control: the batch left alone signs every reference", func(t *testing.T) {
		t.Parallel()
		f := newBatchFixture(t, references)
		h := f.heldOver()
		o := driveHeldBatch(t, f.config(t, h, jobs, io.Discard), h, jobs)
		if o.err != nil || len(o.res.Signed) != references {
			t.Fatalf("the control signed %d of %d (err %v), so it establishes no baseline", len(o.res.Signed), references, o.err)
		}
	})

	t.Run("a batch withdrawn with two in flight starts no third", func(t *testing.T) {
		t.Parallel()
		f := newBatchFixture(t, references)
		h := f.heldOver()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan batchOutcome, 1)
		go func() {
			res, err := Sign(ctx, f.config(t, h, jobs, io.Discard))
			done <- batchOutcome{res, err}
		}()

		deadline := time.After(signLiveness)
		var inFlight []string
		for len(inFlight) < jobs {
			select {
			case d := <-h.started:
				inFlight = append(inFlight, d)
			case <-deadline:
				t.Fatalf("liveness ceiling: only %d of %d signatures started", len(inFlight), jobs)
			}
		}
		cancel()
		for _, d := range inFlight {
			close(h.gates[d])
		}

		var o batchOutcome
		select {
		case o = <-done:
		case <-deadline:
			t.Fatal("liveness ceiling: the withdrawn batch never returned")
		}

		if !errors.Is(o.err, context.Canceled) {
			t.Errorf("the withdrawn batch returned %v; want an error carrying context.Canceled", o.err)
		}
		if got := signedOf(h); len(got) != jobs {
			t.Errorf("the signer was asked %d times; want only the %d in flight when the batch was withdrawn: %v", len(got), jobs, got)
		}
		if got := returnedOf(h); got != jobs {
			t.Errorf("the batch returned with %d of the %d signatures in flight finished; it must wait for each", got, jobs)
		}
		for i := jobs; i < references; i++ {
			if n := countReferrers(t, f.store, f.subjects[f.refs[i]], signing.BundleMediaType); n != 0 {
				t.Errorf("%s, never started, carries %d signatures", f.refs[i], n)
			}
		}
		// Only the references in flight when the batch was withdrawn may be reported,
		// each one at most once and in the order given; each one reported carries the
		// signature attached for it.
		next := 0
		for _, s := range o.res.Signed {
			for next < jobs && f.refs[next] != s.Reference {
				next++
			}
			if next == jobs {
				t.Errorf("the withdrawn batch reported %+v; only %v, in that order, may be reported", o.res.Signed, f.refs[:jobs])
				break
			}
			next++
			if n := countReferrers(t, f.store, f.subjects[s.Reference], signing.BundleMediaType); n != 1 {
				t.Errorf("%s is reported signed and carries %d signatures", s.Reference, n)
			}
		}
	})
}

// authoritySigner is a test-only signing.Signer that makes a keyless signature
// through the in-memory certificate authority and transparency log for every call,
// as the admitted publisher identity. The authority is not relied on from two
// goroutines at once, so calls are taken through it one at a time; the batch's own
// work around each signature still runs at once.
type authoritySigner struct {
	mu        sync.Mutex
	authority *signingtest.KeylessAuthority
}

func (a *authoritySigner) Sign(_ context.Context, digest string) ([]byte, string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	sig, err := a.authority.Sign(completionIdentity, completionIssuer, digest, time.Now().Add(2*time.Minute))
	if err != nil {
		return nil, "", err
	}
	return sig.Bundle, signing.BundleMediaType, nil
}

// TestEachSignatureOfABatchSignedAtOnceVerifiesOnItsOwn signs a batch four at a
// time and then reads every reference's attached signature back through the
// signature walk a verifying reader runs, under that registry's trust policy, as
// the signature of that reference's own digest: each verifies on its own, and no
// two references share a bundle.
//
// The keyed row signs through the batch's own signer construction from a held
// key, one signer per reference. The keyless row signs through the in-memory
// certificate authority and transparency log, and also reads each bundle's signing
// certificate: no two references share one, nor the key it certifies. That row's
// signer stands in for the production keyless signer, which reaches a live
// certificate authority, so what it proves is that the batch asks for a signature
// per reference and attaches each reference its own, not how the production signer
// mints its key.
func TestEachSignatureOfABatchSignedAtOnceVerifiesOnItsOwn(t *testing.T) {
	t.Parallel()
	const references, jobs = 5, 4
	for _, row := range []struct {
		name    string
		keyless bool
	}{
		{"keyed, from a held key", false},
		{"keyless, through an in-memory certificate authority", true},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			k := newCompletionKeys(t)
			f := newBatchFixture(t, references)
			cfg := SignConfig{
				References: f.refs, SubjectRefs: f.subjects, Target: f.store,
				EnvLookup: func(string) string { return "" }, Out: io.Discard, Jobs: jobs,
			}
			if row.keyless {
				cfg.Workspace = resolveIn(t, k.keylessWS(), nil)
				cfg.Signer = &authoritySigner{authority: k.authority}
				cfg.IDToken = "supplied-token"
			} else {
				cfg.Workspace = resolveIn(t, keyedWS(k.pubPEM), nil)
				cfg.KeyPath = k.keyPath
			}
			res, err := Sign(context.Background(), cfg)
			if err != nil {
				t.Fatalf("Sign: %v", err)
			}
			if len(res.Signed) != references {
				t.Fatalf("the batch reported %d signed; want %d", len(res.Signed), references)
			}

			wsCfg := cfg.Workspace.Config()
			policy, _ := wsCfg.RegistryTrustPolicy(datedRegistry)
			bundles := map[string]string{}
			certificates := map[string]string{}
			keys := map[string]string{}
			for i, ref := range f.refs {
				tag := f.subjects[ref]
				found, judged, verr := verifyingSignature(context.Background(), wsCfg, f.store, tag, f.digests[i], policy)
				if verr != nil || !judged || found == nil {
					t.Errorf("%s: its signature does not verify under its registry's policy (judged %v, err %v)", ref, judged, verr)
				}
				if row.keyless && found != nil && found.Identity != completionIdentity {
					t.Errorf("%s: verified as %q; want %q", ref, found.Identity, completionIdentity)
				}

				descs, lerr := oci.ListReferrers(context.Background(), f.store, tag, signing.BundleMediaType)
				if lerr != nil || len(descs) != 1 {
					t.Fatalf("%s: %d signature(s) attached (err %v); want 1", ref, len(descs), lerr)
				}
				payload, ferr := oci.FetchReferrerPayload(context.Background(), f.store, descs[0])
				if ferr != nil {
					t.Fatalf("%s: read its signature: %v", ref, ferr)
				}
				if other, seen := bundles[string(payload)]; seen {
					t.Errorf("%s and %s carry the same signature bundle", other, ref)
				}
				bundles[string(payload)] = ref
				if !row.keyless {
					continue
				}

				b, berr := signing.LoadBundle(payload)
				if berr != nil {
					t.Fatalf("%s: parse its bundle: %v", ref, berr)
				}
				vc, verr := b.VerificationContent()
				if verr != nil || vc.Certificate() == nil {
					t.Fatalf("%s: its bundle carries no signing certificate (err %v)", ref, verr)
				}
				cert := vc.Certificate()
				if other, seen := certificates[string(cert.Raw)]; seen {
					t.Errorf("%s and %s share one signing certificate", other, ref)
				}
				certificates[string(cert.Raw)] = ref
				if other, seen := keys[string(cert.RawSubjectPublicKeyInfo)]; seen {
					t.Errorf("%s and %s were signed with one key", other, ref)
				}
				keys[string(cert.RawSubjectPublicKeyInfo)] = ref
			}
		})
	}
}

// TestASigningBatchRefusesANegativeBoundBeforeReadingAnything gives the batch a
// bound below zero and requires the refusal to come before any registry is read
// and before any identity is asked for. The control is the same batch with the
// bound at zero, which reads the registry.
func TestASigningBatchRefusesANegativeBoundBeforeReadingAnything(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name      string
		jobs      int
		wantRead  bool
		wantError string
	}{
		{"control: a zero bound signs, reading the registry", 0, true, ""},
		{"a negative bound is refused", -1, false, "the number of references signed at once must be at least 1, got -1"},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			f := newBatchFixture(t, 2)
			target := &countsReads{Store: f.store}
			cfg := SignConfig{
				Workspace: resolveIn(t, "workspace: {}\n", nil), References: f.refs, SubjectRefs: f.subjects,
				Target: target, EnvLookup: func(string) string { return "" }, Out: io.Discard, Jobs: row.jobs,
				Signer: &heldSigner{}, IDToken: "supplied-token",
				IsTTY: func() bool { return true },
				TokenGetter: func() (string, error) {
					t.Error("an identity was asked for")
					return "", errors.New("no identity here")
				},
			}
			_, err := Sign(context.Background(), cfg)
			if row.wantError == "" {
				if err != nil {
					t.Fatalf("Sign: %v", err)
				}
			} else if err == nil || err.Error() != row.wantError {
				t.Fatalf("Sign returned %v; want %q", err, row.wantError)
			}
			if read := target.reads() > 0; read != row.wantRead {
				t.Errorf("the registry was read: %v; want %v", read, row.wantRead)
			}
		})
	}
}

// countsReads is an in-memory registry that counts the reads a batch makes of it.
type countsReads struct {
	*memory.Store
	mu sync.Mutex
	n  int
}

func (c *countsReads) Resolve(ctx context.Context, reference string) (ocispec.Descriptor, error) {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
	return c.Store.Resolve(ctx, reference)
}

func (c *countsReads) Fetch(ctx context.Context, target ocispec.Descriptor) (io.ReadCloser, error) {
	c.mu.Lock()
	c.n++
	c.mu.Unlock()
	return c.Store.Fetch(ctx, target)
}

func (c *countsReads) reads() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

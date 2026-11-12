// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package oci_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"syscall"
	"testing"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/errcode"

	"github.com/aggrega-ai/aggrega-cli/internal/retry"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// registryWrite is one write a publication makes to its target: a push of a
// descriptor's bytes, or a tag pointing a reference at a descriptor.
type registryWrite struct {
	tag  bool
	desc ocispec.Descriptor
	ref  string
}

// faultingTarget is an in-memory store that fails chosen writes the way a
// registry fails them. It embeds the store, so its existence check, its tags and
// its referrer graph are the store's own, and it intercepts only Push and Tag.
//
// A write the matcher selects counts as a call on the faulted unit. The first
// `times` of those calls fail with the error fault returns — every one of them
// when times is negative — and the rest go through. With `after` set, a failing
// call first lets the write land and then fails, which is a registry whose answer
// was lost after it stored the bytes.
type faultingTarget struct {
	*memory.Store
	match func(registryWrite) bool
	fault func() error
	times int
	after bool

	calls  int
	failed int
}

func (f *faultingTarget) intercept(w registryWrite, write func() error) error {
	if f.match == nil || !f.match(w) {
		return write()
	}
	f.calls++
	if f.times >= 0 && f.failed >= f.times {
		return write()
	}
	f.failed++
	if f.after {
		if err := write(); err != nil {
			return err
		}
	}
	return f.fault()
}

func (f *faultingTarget) Push(ctx context.Context, desc ocispec.Descriptor, r io.Reader) error {
	return f.intercept(registryWrite{desc: desc}, func() error { return f.Store.Push(ctx, desc, r) })
}

func (f *faultingTarget) Tag(ctx context.Context, desc ocispec.Descriptor, ref string) error {
	return f.intercept(registryWrite{tag: true, desc: desc, ref: ref}, func() error { return f.Store.Tag(ctx, desc, ref) })
}

// The units a publication writes, each matched by what identifies it on the wire.
func blobOf(mediaType string) func(registryWrite) bool {
	return func(w registryWrite) bool { return !w.tag && w.desc.MediaType == mediaType }
}

func manifestOf(artifactType string) func(registryWrite) bool {
	return func(w registryWrite) bool {
		return !w.tag && w.desc.MediaType == ocispec.MediaTypeImageManifest && w.desc.ArtifactType == artifactType
	}
}

func tagOf(ref string) func(registryWrite) bool {
	return func(w registryWrite) bool { return w.tag && w.ref == ref }
}

func status(code int) func() error {
	return func() error { return &errcode.ErrorResponse{StatusCode: code} }
}

// retryTestSignatureType is the artifact type the signature rows attach under.
// It stands in for a signature bundle's media type: the attach path is the same
// for any payload, and what is asserted is the write, not the signature.
const retryTestSignatureType = "application/vnd.dev.sigstore.bundle.v0.3+json"

// The four publication entry points the rows drive, each returning the digest it
// landed. A date or signature entry attaches to a subject that is put in place
// directly in the store, so the only writes the faulting target sees are the
// attach's own.
const (
	entryPush      = "push"
	entryPlugin    = "plugin"
	entryDate      = "published-at"
	entrySignature = "signature"
)

var retryTestAttachedAt = time.Date(2026, 10, 4, 7, 29, 28, 0, time.UTC)

func publishThrough(ctx context.Context, t *testing.T, entry string, store *memory.Store, target *faultingTarget) (string, error) {
	t.Helper()
	switch entry {
	case entryPush:
		return oci.Push(ctx, target, "1.0.0", oci.MediaTypeComponent, []byte("kind: Component\nmetadata:\n  name: retried\n"), nil)
	case entryPlugin:
		return oci.PushPluginManifest(ctx, target, "1.0.0", []byte("kind: Plugin\nmetadata:\n  name: retried\n"), []byte("\x00asm retried wasm"), nil)
	case entryDate, entrySignature:
		if _, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeComponent, []byte("kind: Component\nmetadata:\n  name: subject\n"), nil); err != nil {
			t.Fatalf("put the subject in place: %v", err)
		}
		var desc ocispec.Descriptor
		var err error
		if entry == entryDate {
			desc, err = oci.AttachPublishedAt(ctx, target, "1.0.0", retryTestAttachedAt)
		} else {
			desc, err = oci.AttachReferrer(ctx, target, "1.0.0", retryTestSignatureType, []byte(`{"signature":"retried"}`))
		}
		return desc.Digest.String(), err
	}
	t.Fatalf("unknown entry %q", entry)
	return "", nil
}

// TestEveryRegistryWriteRetriesATransientFailure drives each unit a publication
// writes through the public entry point that writes it, against a target that
// fails that unit once the way a registry fails, and holds the result to what a
// fault-free publication lands.
//
// The control rows run first and fault nothing, so each later row is compared
// with a digest that was produced, not with a value written into the test. Every
// faulted row then asserts the publication succeeded, the faulted unit was
// written exactly twice, and the digest equals the control's — and for the
// attachments, that exactly one referrer of its type exists, so a retried
// attach never leaves a second one.
func TestEveryRegistryWriteRetriesATransientFailure(t *testing.T) {
	// Not parallel: mutates the process-global retry.BaseDelay seam.
	prev := retry.BaseDelay
	retry.BaseDelay = time.Millisecond
	t.Cleanup(func() { retry.BaseDelay = prev })

	ctx := context.Background()
	control := map[string]string{}
	for _, entry := range []string{entryPush, entryPlugin, entryDate, entrySignature} {
		store := memory.New()
		digest, err := publishThrough(ctx, t, entry, store, &faultingTarget{Store: store})
		if err != nil {
			t.Fatalf("control %s: %v", entry, err)
		}
		control[entry] = digest
	}

	rows := []struct {
		name  string
		entry string
		match func(registryWrite) bool
		fault func() error
		after bool
	}{
		{name: "the content blob", entry: entryPush, match: blobOf(oci.MediaTypeComponent), fault: status(500)},
		{name: "the entity manifest", entry: entryPush, match: manifestOf(oci.MediaTypeComponent), fault: status(500)},
		{name: "the tag", entry: entryPush, match: tagOf("1.0.0"), fault: status(500)},
		{name: "the Plugin config blob", entry: entryPlugin, match: blobOf(oci.MediaTypePlugin), fault: status(500)},
		{name: "the Plugin .wasm layer", entry: entryPlugin, match: blobOf(oci.MediaTypeWasm), fault: status(500)},
		{name: "the Plugin tag", entry: entryPlugin, match: tagOf("1.0.0"), fault: status(500)},
		{name: "the published-at payload", entry: entryDate, match: blobOf(oci.MediaTypePublishedAt), fault: status(500)},
		{name: "the published-at referrer manifest", entry: entryDate, match: manifestOf(oci.MediaTypePublishedAt), fault: status(500)},
		{name: "a signature payload", entry: entrySignature, match: blobOf(retryTestSignatureType), fault: status(500)},
		{name: "a signature referrer manifest", entry: entrySignature, match: manifestOf(retryTestSignatureType), fault: status(500)},

		{name: "the published-at payload, answer lost after it landed", entry: entryDate, match: blobOf(oci.MediaTypePublishedAt), fault: status(500), after: true},
		{name: "the published-at referrer manifest, answer lost after it landed", entry: entryDate, match: manifestOf(oci.MediaTypePublishedAt), fault: status(500), after: true},
		{name: "a signature referrer manifest, answer lost after it landed", entry: entrySignature, match: manifestOf(retryTestSignatureType), fault: status(500), after: true},

		{name: "the content blob, 408", entry: entryPush, match: blobOf(oci.MediaTypeComponent), fault: status(408)},
		{name: "the content blob, connection reset", entry: entryPush, match: blobOf(oci.MediaTypeComponent), fault: func() error {
			return &url.Error{Op: "Put", URL: "http://registry.invalid/v2/upload", Err: &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}}
		}},
		{name: "the content blob, unexpected EOF", entry: entryPush, match: blobOf(oci.MediaTypeComponent), fault: func() error {
			return &url.Error{Op: "Put", URL: "http://registry.invalid/v2/upload", Err: io.ErrUnexpectedEOF}
		}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			store := memory.New()
			target := &faultingTarget{Store: store, match: row.match, fault: row.fault, times: 1, after: row.after}
			digest, err := publishThrough(ctx, t, row.entry, store, target)
			if err != nil {
				t.Fatalf("the publication failed on a fault one retry clears: %v", err)
			}
			if target.calls != 2 {
				t.Errorf("the faulted unit was written %d times; want 2 (the failed attempt and one retry)", target.calls)
			}
			if digest != control[row.entry] {
				t.Errorf("landed %s; the fault-free control landed %s — a retry changed what was published", digest, control[row.entry])
			}
			switch row.entry {
			case entryDate:
				refs, lerr := oci.ListReferrers(ctx, store, "1.0.0", oci.MediaTypePublishedAt)
				if lerr != nil {
					t.Fatalf("ListReferrers: %v", lerr)
				}
				if len(refs) != 1 {
					t.Errorf("published-at referrers = %d; want 1", len(refs))
				}
				at, found, rerr := oci.ReadPublishedAt(ctx, store, "1.0.0")
				if rerr != nil || !found {
					t.Fatalf("ReadPublishedAt: found=%v err=%v", found, rerr)
				}
				if !at.Equal(retryTestAttachedAt) {
					t.Errorf("stated date %v; want the attached %v", at, retryTestAttachedAt)
				}
			case entrySignature:
				refs, lerr := oci.ListReferrers(ctx, store, "1.0.0", retryTestSignatureType)
				if lerr != nil {
					t.Fatalf("ListReferrers: %v", lerr)
				}
				if len(refs) != 1 {
					t.Errorf("signature referrers = %d; want 1", len(refs))
				}
			}
		})
	}
}

// TestARegistryWriteThatKeepsFailingOrIsRefusedEndsAsTheClassifierSays holds the
// three ways a retried write ends without landing: a fault that persists through
// every attempt, a refusal the classifier reads as terminal, and a caller that
// withdraws. Each asserts how many attempts reached the target and that the
// returned error still carries the cause.
func TestARegistryWriteThatKeepsFailingOrIsRefusedEndsAsTheClassifierSays(t *testing.T) {
	// Not parallel: mutates the process-global retry.BaseDelay seam.
	prev := retry.BaseDelay
	retry.BaseDelay = time.Millisecond
	t.Cleanup(func() { retry.BaseDelay = prev })

	t.Run("a permanent 500 fails after every attempt", func(t *testing.T) {
		store := memory.New()
		target := &faultingTarget{Store: store, match: blobOf(oci.MediaTypeComponent), fault: status(500), times: -1}
		_, err := publishThrough(context.Background(), t, entryPush, store, target)
		var resp *errcode.ErrorResponse
		if !errors.As(err, &resp) || resp.StatusCode != 500 {
			t.Fatalf("error = %v; want it to wrap the registry's 500", err)
		}
		if target.calls != retry.MaxAttempts {
			t.Errorf("the faulted unit was written %d times; want %d", target.calls, retry.MaxAttempts)
		}
	})

	t.Run("a 403 is terminal", func(t *testing.T) {
		store := memory.New()
		target := &faultingTarget{Store: store, match: blobOf(oci.MediaTypeComponent), fault: status(403), times: -1}
		_, err := publishThrough(context.Background(), t, entryPush, store, target)
		var resp *errcode.ErrorResponse
		if !errors.As(err, &resp) || resp.StatusCode != 403 {
			t.Fatalf("error = %v; want it to wrap the registry's 403", err)
		}
		if target.calls != 1 {
			t.Errorf("the refused unit was written %d times; want 1 (a refusal is not retried)", target.calls)
		}
	})

	// The backoff is set to an hour for this case, so a retry that slept it out
	// would never return; the ceiling below is a liveness bound separating
	// "returns" from "hangs", not a performance budget.
	t.Run("a caller cancellation is not retried and sleeps out no backoff", func(t *testing.T) {
		shrunk := retry.BaseDelay
		retry.BaseDelay = time.Hour
		t.Cleanup(func() { retry.BaseDelay = shrunk })
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		store := memory.New()
		target := &faultingTarget{Store: store, match: blobOf(oci.MediaTypeComponent), times: -1, fault: func() error {
			cancel()
			return &url.Error{Op: "Put", URL: "http://registry.invalid/v2/upload", Err: context.Canceled}
		}}
		type outcome struct {
			err error
		}
		done := make(chan outcome, 1)
		go func() {
			_, err := oci.Push(ctx, target, "1.0.0", oci.MediaTypeComponent, []byte("kind: Component\n"), nil)
			done <- outcome{err: err}
		}()
		select {
		case got := <-done:
			if !errors.Is(got.err, context.Canceled) {
				t.Fatalf("error = %v; want it to carry the cancellation", got.err)
			}
			if target.calls != 1 {
				t.Errorf("the unit was written %d times; want 1 (no attempt after the caller withdrew)", target.calls)
			}
		case <-time.After(30 * time.Second):
			t.Fatal("the push did not return after its caller withdrew; it slept out the backoff")
		}
	})
}

// TestIsTransientRegistryError holds the classifier to the sets its
// documentation names, one value per row, each wrapped the way a transport or a
// caller wraps it, so a match that only worked on a bare value would fail.
func TestIsTransientRegistryError(t *testing.T) {
	t.Parallel()
	wrap := func(err error) error { return fmt.Errorf("oci: push content: %w", err) }
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"an immutable-tag conflict", wrap(oci.ErrImmutableTagConflict()), false},
		{"the unauthorized condition", wrap(oci.ErrFetchUnauthorized()), false},
		{"a 401", wrap(&errcode.ErrorResponse{StatusCode: 401}), false},
		{"a 403", wrap(&errcode.ErrorResponse{StatusCode: 403}), false},
		{"no stored credential", wrap(auth.ErrBasicCredentialNotFound), false},
		{"a caller cancellation", wrap(&url.Error{Op: "Put", URL: "u", Err: context.Canceled}), false},
		{"a 404", wrap(&errcode.ErrorResponse{StatusCode: 404}), false},
		{"a 416", wrap(&errcode.ErrorResponse{StatusCode: 416}), false},
		{"an unrecognised error", wrap(errors.New("something else")), false},
		{"a 500", wrap(&errcode.ErrorResponse{StatusCode: 500}), true},
		{"a 503", wrap(&errcode.ErrorResponse{StatusCode: 503}), true},
		{"a 408", wrap(&errcode.ErrorResponse{StatusCode: 408}), true},
		// A registry asking to slow down is answered by the registry client before
		// any classifier sees it, so neither its answer nor its refusal spends the
		// retry budget. The refusal that ended on the request's own deadline is the
		// row that fails if the refusal is not checked first: its deadline alone
		// would read as transient.
		{"a 429", wrap(&errcode.ErrorResponse{StatusCode: 429}), false},
		{"a registry that kept asking to slow down", wrap(oci.ErrRegistryTooBusy()), false},
		{"a registry that kept asking to slow down until the deadline", wrap(fmt.Errorf("%w: %w", oci.ErrRegistryTooBusy(), context.DeadlineExceeded)), false},
		{"a network timeout", wrap(&url.Error{Op: "Put", URL: "u", Err: &net.OpError{Op: "dial", Net: "tcp", Err: timeoutNetError{}}}), true},
		{"a connection reset", wrap(&url.Error{Op: "Put", URL: "u", Err: &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}}), true},
		{"an unexpected EOF", wrap(&url.Error{Op: "Put", URL: "u", Err: io.ErrUnexpectedEOF}), true},
		{"a request deadline", wrap(&url.Error{Op: "Put", URL: "u", Err: context.DeadlineExceeded}), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := oci.IsTransientRegistryError(tc.err); got != tc.want {
				t.Errorf("IsTransientRegistryError(%v) = %v; want %v", tc.err, got, tc.want)
			}
		})
	}
}

// timeoutNetError is a net.Error reporting a timeout, as a dial or read
// deadline does.
type timeoutNetError struct{}

func (timeoutNetError) Error() string   { return "i/o timeout" }
func (timeoutNetError) Timeout() bool   { return true }
func (timeoutNetError) Temporary() bool { return true }

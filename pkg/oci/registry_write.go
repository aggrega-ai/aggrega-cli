// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package oci

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/registry/remote/errcode"

	"github.com/aggrega-ai/aggrega-cli/internal/retry"
)

// Every write this package makes to a registry goes through one of the three
// functions below — a blob upload, a manifest pack, a tag — and each of them
// retries a transient failure with the same budget and the same classifier. The
// functions that publish (Push, PushNamed, PushPluginManifest,
// AttachReferrerToDescriptor, and the pack steps they share) call these and
// never the underlying oras write directly, so a write added later reaches the
// registry through the retry or not at all.
//
// A retry is a new call to the oras primitive with the same bytes. Against a
// remote registry each blob upload opens its own upload session, so a retry
// never resends the finalizing request of a session the registry has already
// consumed: a registry that rewrites its upload state as it receives the body
// refuses such a resend, and only a new session recovers. That is also why the
// retry sits here and not in the HTTP transport, which can only resend the
// request it was given.
//
// A registry asking to slow down is not one of those failures. How a request
// reacts to it is decided in one place (registry_busy.go): the wait, the
// shared allowance and the bound all live there, and a request with a body that
// was refused that way is handed back here, where each write runs again as a new
// attempt (resendWhileBusy) — its first request taking the wait on its way out —
// for as long as that place lets it.

// IsTransientRegistryError reports whether a failed registry write is worth
// another attempt.
//
// Terminal (false): a nil error; an immutable-tag conflict
// (ErrImmutableTagConflict); an authentication refusal — the registry answered
// 401 or 403, or demands a credential the credential store holds none for
// (ErrFetchUnauthorized); a caller cancellation (context.Canceled); a registry
// that kept asking to slow down (ErrRegistryTooBusy); and any error not
// recognised below, a 429 Too Many Requests among them.
//
// Transient (true): a registry response with any 5xx status or 408 Request
// Timeout; a network error that reports a timeout; a connection the peer reset;
// an unexpected end of a response body (io.ErrUnexpectedEOF); and a deadline that
// expired on the request (context.DeadlineExceeded). A deadline that belongs to
// the caller's own context ends the retry anyway, because the retry stops waiting
// the moment the caller's context is done.
//
// A registry asking to slow down — a 429, or a 503 carrying Retry-After — is not
// a failure worth another attempt under this budget: a registry client reacts to
// it before it reaches any classifier (RegistryClient), with no fixed attempt
// ceiling and a bound of its own, and a write it hands back is run again there
// (resendWhileBusy). Counting it here too would spend this budget on a wait the
// registry asked for.
//
// It reads error values, never their wording, so it answers the same for a
// remote registry, an in-memory store, or any other target that returns these
// values.
func IsTransientRegistryError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrImmutableTagConflict()) || errors.Is(err, ErrFetchUnauthorized()) || isUnauthorized(err) {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, ErrRegistryTooBusy()) {
		return false
	}
	if status, answered := registryStatus(err); answered {
		switch {
		case status >= 500,
			status == http.StatusRequestTimeout:
			return true
		}
	}
	return timedOut(err) || connectionReset(err) || cutShort(err)
}

// ReadFailureKind names, in a reader's words, what kind of failure ended a
// registry read: a registry that kept asking to slow down, the status the
// registry answered with, a read that timed out, a connection that was reset, or
// an answer whose body was cut short. It returns "" for any other failure — one
// with no registry answer and no connection failure behind it — and for a nil
// error.
//
// It reads the same error values IsTransientRegistryError reads, never their
// wording, so a failure is named the same way whether it was worth another
// attempt or not. Where an error carries more than one of these, a registry that
// kept asking to slow down is named first, then the status, then a reset, then an
// answer cut short, then a timeout: a body that ended because the connection was
// reset is named as the reset.
func ReadFailureKind(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrRegistryTooBusy()) {
		return "the registry kept asking to slow down"
	}
	if status, answered := registryStatus(err); answered {
		return "the registry answered " + statusWords(status)
	}
	switch {
	case connectionReset(err):
		return "the connection was reset"
	case cutShort(err):
		return "the answer was cut short"
	case timedOut(err):
		return "the read timed out"
	}
	return ""
}

// registryStatus returns the status a registry answered a request with, where
// err carries that answer.
func registryStatus(err error) (int, bool) {
	var resp *errcode.ErrorResponse
	if errors.As(err, &resp) {
		return resp.StatusCode, true
	}
	return 0, false
}

// timedOut reports whether err is a network error that reports a timeout, or a
// deadline that expired on the request.
func timedOut(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return errors.Is(err, context.DeadlineExceeded)
}

// cutShort reports whether err is a response body that ended before it was
// complete.
func cutShort(err error) bool {
	return errors.Is(err, io.ErrUnexpectedEOF)
}

// connectionReset reports whether err carries the value this platform reports a
// connection the peer reset as.
func connectionReset(err error) bool {
	for _, reset := range connectionResetErrors {
		if errors.Is(err, reset) {
			return true
		}
	}
	return false
}

// pushBlob uploads data as a blob of mediaType to target and returns its
// descriptor, retrying a transient failure (IsTransientRegistryError) up to the
// shared attempt budget, and running again as a new attempt each time the
// registry asks it to slow down (resendWhileBusy).
//
// Each attempt is a new oras.PushBytes call, which reads the bytes afresh and,
// against a remote registry, opens a new upload session. A target that answers
// that the blob already exists has it: a blob is addressed by its digest, so what
// exists is what was asked for, and the push counts as landed. That is what makes
// an attempt whose response was lost after the bytes landed safe to repeat.
func pushBlob(ctx context.Context, target oras.Target, mediaType string, data []byte) (ocispec.Descriptor, error) {
	return resendWhileBusy(ctx, func() (ocispec.Descriptor, error) {
		return retry.Transient(ctx, func() (ocispec.Descriptor, error) {
			desc, err := oras.PushBytes(ctx, target, mediaType, data)
			if errors.Is(err, errdef.ErrAlreadyExists) {
				return content.NewDescriptorFromBytes(mediaType, data), nil
			}
			return desc, err
		}, IsTransientRegistryError)
	})
}

// packManifest packs an OCI image manifest of artifactType from opts into target
// and returns its descriptor, retrying a transient failure
// (IsTransientRegistryError) up to the shared attempt budget, and running again as
// a new attempt each time the registry asks it to slow down (resendWhileBusy).
//
// It pins the manifest's creation annotation (see mergeCreatedAnnotations) on
// every attempt, so each attempt packs byte-identical manifest JSON: a retry
// lands the same digest, and an attempt whose response was lost after the
// manifest landed is repeated as the same manifest rather than as a second one.
// The pack itself treats a blob or manifest the target already holds as landed.
func packManifest(ctx context.Context, target oras.Target, artifactType string, opts oras.PackManifestOptions) (ocispec.Descriptor, error) {
	opts.ManifestAnnotations = mergeCreatedAnnotations(opts.ManifestAnnotations)
	return resendWhileBusy(ctx, func() (ocispec.Descriptor, error) {
		return retry.Transient(ctx, func() (ocispec.Descriptor, error) {
			return oras.PackManifest(ctx, target, oras.PackManifestVersion1_1, artifactType, opts)
		}, IsTransientRegistryError)
	})
}

// tagManifest points tag at desc on target, retrying a transient failure
// (IsTransientRegistryError) up to the shared attempt budget, and running again as
// a new attempt each time the registry asks it to slow down (resendWhileBusy).
// Tagging the same descriptor again is the same request, so a repeated attempt
// leaves the tag where one attempt would.
func tagManifest(ctx context.Context, target oras.Target, desc ocispec.Descriptor, tag string) error {
	_, err := resendWhileBusy(ctx, func() (struct{}, error) {
		return retry.Transient(ctx, func() (struct{}, error) {
			return struct{}{}, target.Tag(ctx, desc, tag)
		}, IsTransientRegistryError)
	})
	return err
}

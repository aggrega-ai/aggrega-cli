// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package registryindex

import (
	"context"
	"fmt"
	"io"

	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

// AttachSignedMarker validates the subject is published, attaches the marker as
// an OCI referrer of the subject, then signs the marker's own manifest digest
// and attaches that signature as a referrer of the marker. The subject artifact
// is never re-pushed or mutated; both attachments are sibling manifests.
//
// It sits beside the discovery and verification of the same marker rather than
// inside the command that writes one, because the two sides are one contract: a
// marker is attached as a referrer of its subject and signed by its own digest,
// and it is discovered and honored on exactly those grounds. Splitting the write
// from the read leaves the shape of the thing stated in two places, free to
// drift on the one detail that decides whether a withdrawal is ever seen.
//
// The signature is produced by calling the signer directly, which is what the
// shared sign step does — it is a pass-through to this same method — so no
// second computation of the bundle exists.
//
// Its diagnostics name what went wrong and not the verb a person would have run:
// a caller reaching this may never have typed a command, so each surface adds its
// own name at its own boundary.
//
// It returns the marker's own manifest digest, which is the marker's identity: the
// marker is an untagged referrer, so nothing else addresses it and a caller told
// only that the attachment succeeded cannot name what was attached. The digest is
// reported for the same reason it is printed — a surface that answers with facts
// rather than with a line of prose needs the fact itself.
func AttachSignedMarker(ctx context.Context, out io.Writer, target oras.Target, subjectRef, reference string, marker deprecation.Marker, signer signing.Signer) (markerDigest string, err error) {
	payload, err := deprecation.Marshal(marker)
	if err != nil {
		return "", err
	}

	// The subject must already be published: a withdrawal never publishes the
	// artifact it withdraws, it only amends it.
	if _, exists, rerr := oci.Resolve(ctx, target, subjectRef); rerr != nil {
		return "", fmt.Errorf("resolve %s: %w", reference, rerr)
	} else if !exists {
		return "", fmt.Errorf("%s is not published; nothing to deprecate", reference)
	}

	markerDesc, err := oci.AttachReferrer(ctx, target, subjectRef, deprecation.ArtifactType, payload)
	if err != nil {
		return "", fmt.Errorf("attach marker to %s: %w", reference, err)
	}

	// Sign the marker's own manifest digest and attach the signature as a
	// referrer of the marker. The marker is an untagged referrer, so its
	// signature attaches by descriptor — a tag-based resolve cannot recover
	// its digest.
	bundle, artifactType, err := signer.Sign(ctx, markerDesc.Digest.String())
	if err != nil {
		return "", fmt.Errorf("sign marker for %s: %w", reference, err)
	}
	if _, err := oci.AttachReferrerToDescriptor(ctx, target, markerDesc, artifactType, bundle); err != nil {
		return "", fmt.Errorf("attach signature to marker for %s: %w", reference, err)
	}

	fmt.Fprintf(out, "Deprecation attached: %s (%s, %s)\n", reference, marker.Severity, markerDesc.Digest)
	return markerDesc.Digest.String(), nil
}

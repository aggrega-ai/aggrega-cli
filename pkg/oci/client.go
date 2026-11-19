// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package oci

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"

	"github.com/Masterminds/semver/v3"
	"github.com/aggrega-ai/aggrega-cli/internal/credentialfailure"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/errdef"
	"oras.land/oras-go/v2/registry"
	"oras.land/oras-go/v2/registry/remote"
	"oras.land/oras-go/v2/registry/remote/auth"
	"oras.land/oras-go/v2/registry/remote/credentials"
	"oras.land/oras-go/v2/registry/remote/errcode"
)

// PullResult holds the bytes pulled from a target plus the OCI
// manifest digest and the mediatype the manifest declared.
//
// ManifestBytes carries the raw manifest JSON the pull read to derive the
// content descriptor. Digest is that manifest's digest, so
// digest.ContentDigest(ManifestBytes) equals Digest — the property a cached
// manifest is re-hashed against offline. ManifestBytes may be empty for a
// target that does not surface the manifest body.
type PullResult struct {
	Bytes         []byte
	Digest        string
	MediaType     string
	ManifestBytes []byte
}

// Pull fetches the artifact tagged `tag` from `target` and returns
// the bytes, the manifest digest, and the artifact's mediatype.
// The target may be a remote.Repository (production) or any in-
// memory CAS implementing oras.ReadOnlyTarget (tests).
//
// An optional expectedMediaType selects the content layer by media type
// rather than by position. A caller pulling content whose manifest may
// carry more than one layer — a signed chart, which bundles a detached
// provenance layer beside the chart content — passes the content media
// type it expects; the provenance and any other layer are then ignored,
// and a manifest missing (or ambiguous about) that content halts loudly.
// Callers whose manifests carry a single artifact layer omit the argument
// and the sole layer is used. The same expectation drives the content
// digest verification below, so verification checks the selected content
// layer, never a positionally-first layer.
//
// The manifest is read through fetchManifest: against a registry, one request
// for the manifest by the reference exactly as given, so a pull by tag fetches
// by the tag and a pull by digest fetches by the digest.
//
// Error mapping:
//   - target reports "not found" → wrapped ErrFetchNotFound
//   - authentication required, rejected, or no stored credential for
//     the registry → wrapped ErrFetchUnauthorized
//   - manifest bytes that do not match the digest stated for them, or
//     content digest mismatch between manifest and fetched bytes →
//     wrapped ErrFetchIntegrity
//
// Remaining raw transport errors (DNS/TCP failures → network
// unreachable) pass through unmapped; callers can still match the
// sentinels in errors.go via errors.Is once they wrap their own
// diagnostic context.
func Pull(ctx context.Context, target oras.ReadOnlyTarget, tag string, expectedMediaType ...string) (*PullResult, error) {
	manifestDesc, manifestBytes, err := fetchManifest(ctx, target, tag)
	if err != nil {
		return nil, err
	}
	return contentFromManifest(ctx, target, tag, manifestDesc, manifestBytes, expectedMediaType...)
}

// resolveReference resolves a tag or digest on target to the manifest it names,
// classifying a failure through referenceError.
func resolveReference(ctx context.Context, target oras.ReadOnlyTarget, reference string) (ocispec.Descriptor, error) {
	manifestDesc, err := target.Resolve(ctx, reference)
	if err != nil {
		return ocispec.Descriptor{}, referenceError(reference, err)
	}
	return manifestDesc, nil
}

// fetchManifest reads the manifest a tag or digest names on target and returns
// its descriptor and its bytes. Each read of a published artifact's manifest
// begins with it: a pull of an entity or a plugin, and a read of its facets.
//
// Against a registry the manifest is requested once, by the reference exactly as
// given: a tag is fetched by the tag and a digest by the digest, with no separate
// request to resolve the reference first. A target that cannot fetch by
// reference — an in-memory store — resolves the reference and then fetches the
// descriptor it resolved to.
//
// Either way the bytes are checked against the size and digest the target states
// for the manifest, so a body that does not match them is refused with
// ErrFetchIntegrity rather than read as the manifest. A reference that names
// nothing wraps ErrFetchNotFound, and a refused or absent credential wraps
// ErrFetchUnauthorized, through referenceError, so the two failures a reader acts
// on are classified identically whichever descriptor of the manifest it goes on to
// read. A manifest carrying a media type this compiler no longer reads is refused
// here with ErrRetiredMediaType (refuseRetiredMediaType).
func fetchManifest(ctx context.Context, target oras.ReadOnlyTarget, reference string) (ocispec.Descriptor, []byte, error) {
	var manifestDesc ocispec.Descriptor
	var manifestRC io.ReadCloser
	if fetcher, ok := target.(registry.ReferenceFetcher); ok {
		desc, rc, err := fetcher.FetchReference(ctx, reference)
		if err != nil {
			return ocispec.Descriptor{}, nil, referenceError(reference, err)
		}
		manifestDesc, manifestRC = desc, rc
	} else {
		desc, err := resolveReference(ctx, target, reference)
		if err != nil {
			return ocispec.Descriptor{}, nil, err
		}
		rc, err := target.Fetch(ctx, desc)
		if err != nil {
			return ocispec.Descriptor{}, nil, fmt.Errorf("fetch manifest %q: %w", reference, err)
		}
		manifestDesc, manifestRC = desc, rc
	}
	defer manifestRC.Close()

	manifestBytes, err := content.ReadAll(manifestRC, manifestDesc)
	if err != nil {
		if errors.Is(err, content.ErrMismatchedDigest) || errors.Is(err, content.ErrTrailingData) {
			return ocispec.Descriptor{}, nil, fmt.Errorf("manifest %q does not match its digest %s: %v: %w",
				reference, manifestDesc.Digest, err, ErrFetchIntegrity())
		}
		return ocispec.Descriptor{}, nil, fmt.Errorf("read manifest %q: %w", reference, err)
	}
	if err := refuseRetiredMediaType(reference, manifestBytes); err != nil {
		return ocispec.Descriptor{}, nil, err
	}
	return manifestDesc, manifestBytes, nil
}

// refuseRetiredMediaType refuses a manifest that carries an Aggrega media type of
// a version this compiler no longer reads, on its artifact type, its config or
// any layer, in that order, naming the first it meets. A manifest that does not
// parse is left to the reader that parses it, which reports it.
//
// It runs in fetchManifest, the read every read of a published artifact's
// manifest goes through — a pull, a plugin fetch, an index reading a manifest's
// facets — so an artifact published by an earlier release is refused by name before
// its content is taken for anything — an entity, an executable, a documentation
// bundle — rather than failing later as whatever the mismatch happens to break.
// The referrers attached beside an artifact (a signature, a publication date) are
// read by subject digest through their own path and carry media types this check
// does not name.
func refuseRetiredMediaType(reference string, manifestBytes []byte) error {
	view, err := parseManifest(manifestBytes)
	if err != nil {
		return nil
	}
	carried := append([]string{view.ArtifactType, view.Config.MediaType}, layerMediaTypes(view.Layers)...)
	for _, mt := range carried {
		if current, ok := retiredMediaType(mt); ok {
			return fmt.Errorf("%q carries media type %q, and this compiler reads %q; publish the artifact again with the current release: %w",
				reference, mt, current, ErrRetiredMediaType())
		}
	}
	return nil
}

// layerMediaTypes returns the media type of each layer, in manifest order.
func layerMediaTypes(layers []ocispec.Descriptor) []string {
	out := make([]string, 0, len(layers))
	for _, l := range layers {
		out = append(out, l.MediaType)
	}
	return out
}

// referenceError classifies a failure to resolve or fetch the manifest a
// reference names: a reference the registry holds nothing at wraps
// ErrFetchNotFound, and a refused or absent credential wraps ErrFetchUnauthorized.
// Any other failure is returned wrapped as it came.
func referenceError(reference string, err error) error {
	if isNotFoundResponse(err) {
		return fmt.Errorf("%q: %w", reference, ErrFetchNotFound())
	}
	if isUnauthorized(err) {
		return fmt.Errorf("resolve %q: %v: %w", reference, err, ErrFetchUnauthorized())
	}
	return fmt.Errorf("resolve %q: %w", reference, err)
}

// contentFromManifest reads the content layer a fetched manifest commits to,
// verifies it against that manifest, and returns it as a PullResult.
//
// It is the second half of Pull, separated so a reader that has already fetched
// the manifest — and decided from it that the content layer is what it wants —
// reads the layer through the same selection and the same verify rather than a
// copy of them.
func contentFromManifest(ctx context.Context, target oras.ReadOnlyTarget, tag string, manifestDesc ocispec.Descriptor, manifestBytes []byte, expectedMediaType ...string) (*PullResult, error) {
	contentDesc, mediaType, err := descriptorFromManifest(manifestBytes, expectedMediaType...)
	if err != nil {
		return nil, fmt.Errorf("oci: parse manifest %q: %w", tag, err)
	}

	contentRC, err := target.Fetch(ctx, contentDesc)
	if err != nil {
		if errors.Is(err, errdef.ErrNotFound) {
			return nil, fmt.Errorf("oci: content for %q: %w", tag, ErrFetchNotFound())
		}
		return nil, fmt.Errorf("oci: fetch content %q: %w", tag, err)
	}
	content, err := io.ReadAll(contentRC)
	_ = contentRC.Close()
	if err != nil {
		return nil, fmt.Errorf("oci: read content %q: %w", tag, err)
	}

	if err := VerifyContentAgainstManifest(manifestBytes, content, expectedMediaType...); err != nil {
		return nil, fmt.Errorf("oci: %q: %w", tag, err)
	}

	return &PullResult{
		Bytes:         content,
		Digest:        manifestDesc.Digest.String(),
		MediaType:     mediaType,
		ManifestBytes: manifestBytes,
	}, nil
}

// epochCreated is the fixed value Push pins into every manifest's
// org.opencontainers.image.created annotation. oras stamps that
// annotation with the current wall-clock time unless a value is
// supplied, which would make byte-identical content produce a
// different manifest digest on every push. Pinning it to the zero
// epoch (the reproducible-build convention) makes the manifest digest
// a pure function of the content — the precondition the publish-time
// immutability guard relies on to recognise a re-push of identical
// content. This extends the Deterministic Output discipline from the
// content blob (which callers pre-canonicalise) to the manifest.
const epochCreated = "1970-01-01T00:00:00Z"

// mergeCreatedAnnotations folds a caller's searchable annotations together with
// the pinned creation annotation into the one map a packed manifest carries.
// The pinned created value always wins, so a caller can never displace the
// determinism pin. The copy's iteration order is irrelevant: the result is a
// map oras serialises with sorted keys, so byte-identical inputs still produce a
// byte-identical manifest digest regardless of which searchable keys are
// present.
func mergeCreatedAnnotations(annotations map[string]string) map[string]string {
	merged := make(map[string]string, len(annotations)+1)
	for k, v := range annotations {
		merged[k] = v
	}
	merged[ocispec.AnnotationCreated] = epochCreated
	return merged
}

// packArtifact uploads `content` and packs a deterministic OCI manifest
// referencing it into `target`, returning the manifest descriptor. It
// does NOT tag. Push, PushNamed, ManifestDigest and ManifestDigestNamed share it
// so the digest a guard previews against an in-memory store is byte-identical to
// the digest a real push produces.
//
// `fileName` is stamped on the content layer as its title — the name a standard
// registry client writes the content to (see contentTitle). An empty name stamps
// no title.
//
// `annotations` carries the searchable catalog annotations to stamp onto the
// manifest (see SearchableMetadata); it is merged with the pinned creation
// annotation into one manifest-annotation map. A nil or empty map stamps only
// the creation pin. Because the digest preview and the real push route through
// this one helper, both stamp identical annotations and produce an identical
// digest.
//
// The content upload and the manifest pack each retry a transient registry
// failure (see IsTransientRegistryError) up to three attempts with a short
// backoff, each attempt a new upload of the same bytes; a step still failing
// after that returns its error, wrapped with the step it was.
func packArtifact(ctx context.Context, target oras.Target, mediaType, fileName string, content []byte, annotations map[string]string) (ocispec.Descriptor, error) {
	contentDesc, err := pushBlob(ctx, target, mediaType, content)
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("oci: push content: %w", err)
	}
	manifestDesc, err := packManifest(ctx, target, mediaType, oras.PackManifestOptions{
		Layers:              []ocispec.Descriptor{withTitle(contentDesc, fileName)},
		ManifestAnnotations: annotations,
	})
	if err != nil {
		return ocispec.Descriptor{}, fmt.Errorf("oci: pack manifest: %w", err)
	}
	return manifestDesc, nil
}

// Push uploads `content` with `mediaType` to `target`, builds an OCI
// manifest pointing at it, tags the manifest as `tag`, and returns
// the manifest digest. The target may be a remote.Repository
// (production) or any in-memory CAS implementing oras.Target
// (tests).
//
// Per Aggrega's deterministic-output discipline, callers SHOULD
// supply bundle bytes pre-canonicalised (sorted file entries in
// tar archives; zero timestamps; gzip default compression). Push
// adds no content canonicalisation; it trusts its input. It does pin
// the manifest's creation annotation, so byte-identical content always
// produces a byte-identical manifest digest.
//
// `annotations` carries the searchable catalog annotations to stamp onto the
// manifest (see SearchableMetadata); pass nil to stamp none. A caller that
// previews the digest with ManifestDigest before pushing MUST pass the same
// annotations map to both, or the previewed digest will not equal the pushed
// digest.
//
// The content layer is titled from `mediaType`: an entity kind's content is
// entity.yaml or entity.tar.gz according to its transport shape (see
// contentTitle). A media type this package derives no title for stamps none, so
// a caller publishing content of such a type supplies the name with PushNamed.
//
// Each write it makes — the content upload, the manifest, the tag — retries a
// transient registry failure as PushNamed describes.
func Push(ctx context.Context, target oras.Target, tag string, mediaType string, content []byte, annotations map[string]string) (string, error) {
	return PushNamed(ctx, target, tag, mediaType, contentTitle(mediaType), content, annotations)
}

// PushNamed is Push for content whose file name is supplied by the caller rather
// than derived from its media type — a standalone data artifact, which carries
// no entity and is named by its publisher. `fileName` is stamped on the content
// layer as the org.opencontainers.image.title annotation, the name a standard
// registry client writes the content to; an empty name stamps no title. The name
// is part of the manifest and therefore of its digest, so a caller that previews
// the digest with ManifestDigestNamed MUST pass the same name to both.
//
// Each write it makes to the target — the content upload, the manifest, the
// tag — retries a transient registry failure (see IsTransientRegistryError) up
// to three attempts with a short backoff. A retried upload is a new upload of
// the same bytes, and a retried manifest is the same manifest, so a retry lands
// the digest one attempt would. A write still failing after its last attempt
// fails the push, and the error names the step: the content, the manifest or
// the tag.
func PushNamed(ctx context.Context, target oras.Target, tag, mediaType, fileName string, content []byte, annotations map[string]string) (string, error) {
	manifestDesc, err := packArtifact(ctx, target, mediaType, fileName, content, annotations)
	if err != nil {
		return "", err
	}
	if err := tagManifest(ctx, target, manifestDesc, tag); err != nil {
		return "", fmt.Errorf("oci: tag %q: %w", tag, err)
	}
	return manifestDesc.Digest.String(), nil
}

// ManifestDigest computes, without contacting any remote, the manifest
// digest Push would produce for `content` of `mediaType`. It packs into
// an in-memory store and returns the descriptor digest. Because the
// manifest's creation annotation is pinned, the result is deterministic
// and equal to the digest a real Push of the same content yields — so a
// caller can preview the digest a push would land before deciding
// whether that push would overwrite an existing tag.
//
// `annotations` must equal the map the corresponding Push will stamp (see
// SearchableMetadata); the searchable annotations are part of the manifest and
// therefore part of the digest, so a preview computed with a different set will
// not match the pushed digest.
func ManifestDigest(ctx context.Context, mediaType string, content []byte, annotations map[string]string) (string, error) {
	return ManifestDigestNamed(ctx, mediaType, contentTitle(mediaType), content, annotations)
}

// ManifestDigestNamed computes, without contacting any remote, the manifest
// digest PushNamed would produce for `content` of `mediaType` published under
// `fileName`. It is ManifestDigest for content whose name the caller supplies,
// and the same equality holds: a preview computed with the name and annotations
// a PushNamed will stamp equals the digest that push lands.
func ManifestDigestNamed(ctx context.Context, mediaType, fileName string, content []byte, annotations map[string]string) (string, error) {
	manifestDesc, err := packArtifact(ctx, memory.New(), mediaType, fileName, content, annotations)
	if err != nil {
		return "", err
	}
	return manifestDesc.Digest.String(), nil
}

// Resolve reports the manifest digest the target currently has for
// `tag`. It is the read-only pre-push HEAD: exists is false (with a nil
// error and empty digest) when the tag is simply absent, so callers can
// distinguish "no such tag, safe to publish" from a transport or
// authentication failure (non-nil error). The target may be a
// remote.Repository or any oras.ReadOnlyTarget.
func Resolve(ctx context.Context, target oras.ReadOnlyTarget, tag string) (digest string, exists bool, err error) {
	desc, err := target.Resolve(ctx, tag)
	if err != nil {
		if errors.Is(err, errdef.ErrNotFound) {
			return "", false, nil
		}
		if isUnauthorized(err) {
			return "", false, fmt.Errorf("oci: resolve %q: %v: %w", tag, err, ErrFetchUnauthorized())
		}
		return "", false, fmt.Errorf("oci: resolve %q: %w", tag, err)
	}
	return desc.Digest.String(), true, nil
}

// NewRemoteRepository constructs an authenticated remote.Repository
// for the given full repository reference (e.g.
// "hub.aggrega.ai/component/core/api-server"). Authentication reads
// the Docker credential store — the Docker configuration file
// (DOCKER_CONFIG's config.json, else ~/.docker/config.json), with
// credential-helper delegation via its credsStore and credHelpers
// honoured — which is where `aggrega login` and `docker login` write.
// No other tool's credential store is read.
//
// The returned repository targets the full reference path; the
// `tag` argument to Pull / Push selects the specific version within
// that repository.
//
// A reference whose registry host is loopback (localhost, 127.0.0.0/8,
// ::1 — with or without a port) is contacted over plain HTTP, so a
// local development registry works without TLS provisioning. The
// detection is host-based and automatic: plaintext can only ever go to
// the local machine, and every non-loopback host keeps HTTPS exactly
// as before. There is no configuration surface for this.
// The HTTP client is supplied by the caller and obtained from the network-egress
// gate (pkg/egress): this package owns OCI semantics but never decides network
// policy, so the gate's offline refusal happens before this constructor is
// reached and a non-nil client here means egress permitted the connection.
func NewRemoteRepository(reference string, httpClient *http.Client) (*remote.Repository, error) {
	repo, err := remote.NewRepository(reference)
	if err != nil {
		return nil, fmt.Errorf("oci: parse reference %q: %w", reference, err)
	}
	plainHTTP, client, err := remoteClient(repo.Reference.Registry, httpClient)
	if err != nil {
		return nil, err
	}
	repo.PlainHTTP = plainHTTP
	repo.Client = client
	return repo, nil
}

// NewRemoteRegistry constructs an authenticated remote.Registry for the given
// registry host (e.g. "hub.aggrega.ai"). It is the registry sibling of
// NewRemoteRepository: a registry enumerates the repositories it serves and
// answers the /v2/ base ping, where a repository targets one reference path
// within it. Both apply the same loopback plain-HTTP handling and the same
// Docker-credential-backed authentication, so the two never wire the transport
// differently.
//
// A registry host that is loopback (localhost, 127.0.0.0/8, ::1 — with or
// without a port) is contacted over plain HTTP so a local development registry
// works without TLS; every non-loopback host keeps HTTPS. There is no
// configuration surface for this.
// The HTTP client is supplied by the caller and obtained from the network-egress
// gate (pkg/egress), mirroring NewRemoteRepository.
func NewRemoteRegistry(host string, httpClient *http.Client) (*remote.Registry, error) {
	reg, err := remote.NewRegistry(host)
	if err != nil {
		return nil, fmt.Errorf("oci: parse registry %q: %w", host, err)
	}
	plainHTTP, client, err := remoteClient(reg.Reference.Registry, httpClient)
	if err != nil {
		return nil, err
	}
	reg.PlainHTTP = plainHTTP
	reg.Client = client
	return reg, nil
}

// remoteClient builds the loopback plain-HTTP decision and the
// Docker-credential-backed auth client that both NewRemoteRepository and
// NewRemoteRegistry apply to their remote target. The registry host drives the
// loopback decision; authentication reads the Docker credential store — the
// Docker configuration file, with credential-helper delegation honoured — where
// `aggrega login` and `docker login` write, and no other tool's store. The wiring
// lives here once so the repository and registry clients cannot diverge.
//
// A failure of the credential store — opening it here, or reading an entry when a
// registry asks for a credential — is reported in a fixed sentence naming the
// registry and the credential file, never in the store's own words, which can
// quote what the file holds. See storeCredential.
//
// The client every target is given sends its requests through RegistryClient,
// the one place that decides how a request reacts to a registry asking it to slow
// down. A target built around the egress client directly would reach the registry
// with no wait, no shared allowance and no bound on its refusals.
//
// The auth client remembers what each registry's sign-in taught it (signInCache):
// once a registry has challenged a request, the requests after it, through any
// target this process builds, are sent with the credential the first time. What is
// remembered lives for the process, is never written anywhere, and is filed under
// the credential it was obtained with, so a request is only ever sent with a scheme
// or token learned under the credential it is made with. The first request this
// process sends a registry goes alone (signInClient), so many requests started at
// once draw one challenge rather than one each.
func remoteClient(registryHost string, httpClient *http.Client) (plainHTTP bool, client remote.Client, err error) {
	if httpClient == nil {
		return false, nil, fmt.Errorf("oci: nil egress client; obtain the HTTP client from pkg/egress before constructing a remote target")
	}
	store, err := credentials.NewStoreFromDocker(credentials.StoreOptions{})
	if err != nil {
		return false, nil, credentialfailure.Unreadable(registryHost, "", err)
	}
	return registryhost.IsLoopback(registryHost), signInClientFor(httpClient, storeCredential(store)), nil
}

// signInClientFor builds the client one remote target sends its requests through:
// an auth client over RegistryClient(httpClient), remembering what each registry's
// sign-in taught it (signInCache), behind the first-contact wait (signInClient).
//
// read is the credential store. It is read at most once per registry host for the
// whole life of the target (onceForTheTarget), and that one answer is what both the
// sign-in sends and the sign-in cache files the request under, so the two cannot
// differ. Once per target rather than once per request because a store that
// delegates to a credential helper starts a process for every read, and a
// challenged registry consults the credential on every request; once per target
// rather than once per process because a target serves one operation, so the next
// target — and with it a login made since — reads the store afresh.
func signInClientFor(httpClient *http.Client, read auth.CredentialFunc) *signInClient {
	credential := pinnedFirst(onceForTheTarget(read))
	return &signInClient{auth: &auth.Client{
		Client:     RegistryClient(httpClient),
		Credential: credential,
		Cache:      signInCache{credential: credential},
	}}
}

// VerifyCredential checks a username/password against a registry by pinging
// its /v2/ endpoint with that specific credential, before anything is stored.
// It is distinct from NewRemoteRepository, which reads whatever the credential
// store holds: VerifyCredential authenticates with the credential passed in, so
// `aggrega login` can fail loudly on a typo at login time rather than silently
// at the first push. A registry host that addresses the local machine is pinged
// over plain HTTP (mirroring NewRemoteRepository's loopback handling).
//
// A 401/403 maps to ErrFetchUnauthorized (wrong credential); other failures
// (DNS, TCP, TLS) pass through wrapped with the registry host for diagnosis.
func VerifyCredential(ctx context.Context, registryHost string, cred auth.Credential, httpClient *http.Client) error {
	reg, err := NewRemoteRegistry(registryHost, httpClient)
	if err != nil {
		return err
	}
	// Override the store-backed client the home wires with a static credential
	// for the resolved registry host: verify authenticates with the credential
	// passed in rather than whatever the store holds, so a typo fails loudly at
	// login time rather than silently at the first push. The home's loopback
	// plain-HTTP decision and egress-supplied transport are kept.
	//
	// Its cache is its own and ends with the call: it verifies one credential, so
	// it neither reads a token another request obtained nor leaves one behind for
	// any request after it.
	reg.Client = &auth.Client{
		Client:     RegistryClient(httpClient),
		Credential: auth.StaticCredential(reg.Reference.Registry, cred),
		Cache:      auth.NewCache(),
	}
	if err := reg.Ping(ctx); err != nil {
		if isUnauthorized(err) {
			return fmt.Errorf("oci: verify credential for %q: %v: %w", registryHost, err, ErrFetchUnauthorized())
		}
		return fmt.Errorf("oci: verify credential for %q: %w", registryHost, err)
	}
	return nil
}

// isUnauthorized reports whether a raw oras-go error represents an
// authentication failure: the registry answered 401/403, or the
// credential store holds no entry for a registry that demands basic
// auth. Both conditions have the same operator remediation (log in to
// the registry), so both map to ErrFetchUnauthorized.
func isUnauthorized(err error) bool {
	if errors.Is(err, auth.ErrBasicCredentialNotFound) {
		return true
	}
	var ec *errcode.ErrorResponse
	if errors.As(err, &ec) {
		return ec.StatusCode == http.StatusUnauthorized || ec.StatusCode == http.StatusForbidden
	}
	return false
}

// isNotFoundResponse reports whether a raw oras-go error says the registry holds
// nothing at the address asked: the not-found condition a resolve reports, or a
// registry response answering 404 — which is how a registry answers a tag listing
// for a repository it does not hold, since a listing reports its status as a
// response rather than as that condition.
func isNotFoundResponse(err error) bool {
	if errors.Is(err, errdef.ErrNotFound) {
		return true
	}
	var ec *errcode.ErrorResponse
	return errors.As(err, &ec) && ec.StatusCode == http.StatusNotFound
}

// ociManifestView is the subset of an OCI image manifest this package reads:
// the artifact type, the config descriptor, the layer descriptors, and the
// manifest-level annotations. It is the single shape every manifest parse in
// this package unmarshals into, so the read paths cannot disagree about a
// manifest's structure.
type ociManifestView struct {
	ArtifactType string               `json:"artifactType"`
	Config       ocispec.Descriptor   `json:"config"`
	Layers       []ocispec.Descriptor `json:"layers"`
	Annotations  map[string]string    `json:"annotations"`
}

// parseManifest unmarshals an OCI image manifest's JSON bytes into the subset
// this package reads. It is the one manifest-parse home so no read path
// re-derives the manifest shape independently.
func parseManifest(manifestBytes []byte) (ociManifestView, error) {
	var m ociManifestView
	if err := json.Unmarshal(manifestBytes, &m); err != nil {
		return ociManifestView{}, err
	}
	return m, nil
}

// FetchManifestAnnotations resolves tag on target and returns the annotation
// map stamped on its OCI manifest (the searchable catalog facets a publish
// records; see SearchableMetadata for the aggrega-scoped keys) together with the
// resolved manifest digest. The digest comes from the resolve step, before any
// content is fetched, so an index build can record it as a cheap revalidation
// validator: a later refresh re-resolves the same tag and skips the manifest
// read when the digest is unchanged. It reads the manifest through fetchManifest,
// the read every registry read begins with, and parses it through the one
// manifest-parse home, returning without fetching any content blob, so a
// manifest's facets are read cheaply without re-deriving the manifest shape. A
// manifest carrying no annotations returns a nil map and a nil error; the digest
// is returned regardless.
//
// Being that read, it refuses what fetchManifest refuses: an artifact published
// under a media type this compiler no longer reads is ErrRetiredMediaType here as
// it is at a pull, so an index is never built from facets of an artifact a pull
// would then refuse.
func FetchManifestAnnotations(ctx context.Context, target oras.ReadOnlyTarget, tag string) (map[string]string, string, error) {
	manifestDesc, manifestBytes, err := fetchManifest(ctx, target, tag)
	if err != nil {
		return nil, "", err
	}
	view, err := parseManifest(manifestBytes)
	if err != nil {
		return nil, "", fmt.Errorf("oci: parse manifest %q: %w", tag, err)
	}
	return view.Annotations, manifestDesc.Digest.String(), nil
}

// descriptorFromManifest parses an OCI image manifest's JSON bytes and
// returns the descriptor for the manifest's content layer plus the
// artifact's mediatype.
//
// When a caller supplies an expected content media type, the content
// layer is chosen by that media type, not by position: exactly the layer
// whose descriptor media type equals the expectation is selected, and
// every other layer is ignored. This is what lets a signed chart — whose
// manifest bundles a detached provenance layer beside the chart content,
// in either order — resolve to the chart content rather than to whichever
// layer happens to be listed first. Zero matches or more than one match
// halts loudly (ErrNoContentLayer / ErrAmbiguousContentLayer) rather than
// guess.
//
// When no expected media type is supplied, the manifest is expected to
// carry at least one layer, and the first is returned: for a Plugin that is the
// .wasm, which any notices layer follows. The mediatype is taken from the
// manifest's artifactType field, falling back to the selected layer's own
// mediatype.
func descriptorFromManifest(manifestBytes []byte, expectedMediaType ...string) (ocispec.Descriptor, string, error) {
	manifest, err := parseManifest(manifestBytes)
	if err != nil {
		return ocispec.Descriptor{}, "", err
	}
	if len(manifest.Layers) == 0 {
		return ocispec.Descriptor{}, "", errors.New("oci: manifest carries no layers")
	}
	desc, err := selectContentLayer(manifest.Layers, firstNonEmpty(expectedMediaType))
	if err != nil {
		return ocispec.Descriptor{}, "", err
	}
	mediaType := manifest.ArtifactType
	if mediaType == "" {
		mediaType = desc.MediaType
	}
	return desc, mediaType, nil
}

// firstNonEmpty returns the first non-empty string in vals, or "" when
// none is present. It normalises the variadic expected-media-type
// argument to a single value.
func firstNonEmpty(vals []string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// selectContentLayer chooses the single artifact layer a pull should
// treat as the content blob. When expected is non-empty, selection is a
// deterministic allow-list by media type: exactly the layer whose
// descriptor media type equals expected is chosen, and every other layer
// (a chart's detached provenance layer, any future non-content layer) is
// ignored. Zero matches or more than one match halts loudly rather than
// pick a layer, so a manifest that does not carry the expected content —
// or carries it ambiguously — surfaces a clear error instead of the wrong
// blob.
//
// When expected is empty the caller has stated no content media type; the
// manifest's first layer is returned; for a Plugin that is the .wasm, which
// any notices layer follows.
func selectContentLayer(layers []ocispec.Descriptor, expected string) (ocispec.Descriptor, error) {
	if expected == "" {
		return layers[0], nil
	}
	var matches []ocispec.Descriptor
	for _, l := range layers {
		if l.MediaType == expected {
			matches = append(matches, l)
		}
	}
	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		return ocispec.Descriptor{}, fmt.Errorf("oci: manifest has no layer of media type %q: %w", expected, ErrNoContentLayer())
	default:
		return ocispec.Descriptor{}, fmt.Errorf("oci: manifest has %d layers of media type %q; expected exactly one: %w", len(matches), expected, ErrAmbiguousContentLayer())
	}
}

// ListTags returns every tag the registry has for the target, fully
// materialized via registry.Tags paged iteration. For in-memory test
// targets (memory.Store) the returned set follows the store's
// internal order; for remote.Repository targets pagination is
// transparent. The target must implement registry.TagLister; a
// typed error names the missing capability when it does not.
//
// Returns an empty slice (not nil) when the target has zero tags.
//
// A registry that holds no repository at the target answers the listing not
// found, and that is reported as ErrFetchNotFound — the condition a resolve
// reports for a tag the registry does not hold (resolveReference). So a reader
// that lists the tags to choose a version for a reference naming none meets the
// same condition, for an artifact the registry does not publish, as a reader
// that resolves the version a reference names.
func ListTags(ctx context.Context, target oras.ReadOnlyTarget) ([]string, error) {
	lister, ok := target.(registry.TagLister)
	if !ok {
		return nil, fmt.Errorf("oci: target does not support tag listing: %T", target)
	}
	tags, err := registry.Tags(ctx, lister)
	if err != nil {
		if isNotFoundResponse(err) {
			return nil, fmt.Errorf("list tags: %v: %w", err, ErrFetchNotFound())
		}
		return nil, fmt.Errorf("oci: list tags: %w", err)
	}
	if tags == nil {
		return []string{}, nil
	}
	return tags, nil
}

// strictSemverTag parses a registry tag as strict SemVer 2.0.0 — the same
// grammar entity schemas mandate for metadata.version: MAJOR.MINOR.PATCH with
// an optional -prerelease and +build, no "v" prefix, and exactly three numeric
// parts. It reports the parsed version and whether the tag conformed.
// Non-conformant tags (a "v" prefix, a four-part "1.2.3.4", a bare "latest")
// report ok=false. Every "is this a usable version tag" decision derives from
// this one parse, so the selection and presence checks cannot disagree about
// what counts as a version.
func strictSemverTag(tag string) (*semver.Version, bool) {
	v, err := semver.StrictNewVersion(tag)
	if err != nil {
		return nil, false
	}
	return v, true
}

// SelectLatestVersion returns the highest tag that conforms to strict SemVer
// 2.0.0 (MAJOR.MINOR.PATCH, optional -prerelease / +build, no "v" prefix,
// exactly three numeric parts). Non-conformant tags — a "v" prefix, a four-part
// "1.2.3.4", a bare "latest" — are skipped and never selected. When
// includePreRelease is false, tags carrying a pre-release component are also
// skipped. Returns an empty string when no qualifying tag is present; the
// function never fails (all skip-or-include decisions are made silently).
func SelectLatestVersion(tags []string, includePreRelease bool) string {
	var highest *semver.Version
	var highestOrig string
	for _, t := range tags {
		v, ok := strictSemverTag(t)
		if !ok {
			continue
		}
		if !includePreRelease && v.Prerelease() != "" {
			continue
		}
		if highest == nil || v.GreaterThan(highest) {
			highest = v
			highestOrig = t
		}
	}
	return highestOrig
}

// SelectHighestBelow returns the highest tag that conforms to strict SemVer
// 2.0.0 and is strictly below candidate. It answers "the version just beneath
// this one" — the correct predecessor for a back-patch, where the highest tag
// overall may sit far above the candidate. Tags carrying a pre-release component
// are skipped, as are non-conformant tags (a "v" prefix, a four-part "1.2.3.4",
// a bare "latest") and any tag equal to or above candidate. Conformance and
// ordering derive from the same parse as SelectLatestVersion, so the two never
// disagree about what counts as a version. Returns an empty string when no
// qualifying tag is present; like SelectLatestVersion it never fails. When
// candidate itself is not strict SemVer there is no meaningful bound, and the
// result is the empty string.
func SelectHighestBelow(tags []string, candidate string) string {
	cand, ok := strictSemverTag(candidate)
	if !ok {
		return ""
	}
	var highest *semver.Version
	var highestOrig string
	for _, t := range tags {
		v, ok := strictSemverTag(t)
		if !ok {
			continue
		}
		if v.Prerelease() != "" {
			continue
		}
		if !v.LessThan(cand) {
			continue
		}
		if highest == nil || v.GreaterThan(highest) {
			highest = v
			highestOrig = t
		}
	}
	return highestOrig
}

// HasAnySemverTag reports whether at least one tag conforms to strict SemVer
// 2.0.0 (regardless of pre-release status). It derives conformance from the
// same parse as SelectLatestVersion, so the two never disagree about what
// counts as a version. Used by the CLI to distinguish "no previous version
// exists" (true first publication) from "previous versions exist but all are
// pre-release" (different diagnostic).
func HasAnySemverTag(tags []string) bool {
	for _, t := range tags {
		if _, ok := strictSemverTag(t); ok {
			return true
		}
	}
	return false
}

// SortedSemverTags returns the subset of tags that conform to strict SemVer
// 2.0.0, in ascending SemVer precedence order. Non-conformant tags (a "v"
// prefix, a four-part "1.2.3.4", a bare "latest") are dropped. Conformance
// derives from the same parse as SelectLatestVersion / SelectHighestBelow, so
// callers cannot disagree about what counts as a version. Pre-release tags are
// retained — they are strict-SemVer-conforming — and a caller that wants only
// released versions filters them out itself.
//
// The order is a total order: when two tags share SemVer precedence (build
// metadata is ignored by precedence, so "1.0.0+a" and "1.0.0+b" compare equal),
// the original tag strings break the tie, so the result is deterministic
// regardless of the input order.
func SortedSemverTags(tags []string) []string {
	type parsed struct {
		orig string
		ver  *semver.Version
	}
	conforming := make([]parsed, 0, len(tags))
	for _, t := range tags {
		v, ok := strictSemverTag(t)
		if !ok {
			continue
		}
		conforming = append(conforming, parsed{orig: t, ver: v})
	}
	sort.Slice(conforming, func(i, j int) bool {
		if c := conforming[i].ver.Compare(conforming[j].ver); c != 0 {
			return c < 0
		}
		return conforming[i].orig < conforming[j].orig
	})
	out := make([]string, len(conforming))
	for i, p := range conforming {
		out[i] = p.orig
	}
	return out
}

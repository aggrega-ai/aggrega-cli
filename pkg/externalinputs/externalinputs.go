// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package externalinputs owns the content-addressed store for remote content
// that provider plugins consume during rendering, and the acquisition
// doctrine around it.
//
// The doctrine has two moments. Acquisition may fetch: with acquisition
// enabled, an unpinned URL is fetched, its bytes are pinned into the store
// under their digest, and a record of the pin accumulates for the lockfile.
// Consumption sees only pinned bytes: a pinned URL is served from the store,
// the recorded digest always wins, and a pinned entry never re-pins. A
// missing store blob is refilled from the network in any mode — including a
// frozen closure — because a refill verified against the recorded digest
// cannot change output; an unpinned URL in a frozen closure halts loudly
// instead of fetching.
//
// The store layout is `<root>/sha256/<hex-digest>`. Blob writes are atomic
// (temp file + rename) so a crashed acquisition never leaves a partial blob
// under a digest name.
package externalinputs

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/digest"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
)

// defaultFetchTimeout bounds plain HTTP(S) input fetches when the caller
// supplies no client, so a hung endpoint cannot stall an acquisition
// indefinitely. External inputs can be multi-megabyte archives, so the bound
// is wider than a small-document fetch would need.
const defaultFetchTimeout = 60 * time.Second

// Service is the acquisition-and-consumption gate for external inputs. It
// holds the pinned-records view loaded from the lockfile by the caller,
// accumulates records for newly-acquired URLs, and serves every Fetch through
// the content-addressed store rooted at the directory it was constructed
// with.
//
// The pin discipline in brief: acquisition may fetch and pins; a pinned entry
// always wins and never re-pins; consumers of pinned URLs see only pinned
// bytes. Methods are safe for concurrent use.
type Service struct {
	root    string
	acquire bool
	client  *http.Client

	mu sync.Mutex
	// records holds every known pin keyed by URL: the pinned view passed at
	// construction plus acquisitions made through this Service.
	records map[string]lockfile.ExternalInput
	// acquired marks the URLs pinned by this Service instance (absent from
	// the construction-time view); AcquiredRecords reports exactly these.
	acquired map[string]bool
}

// StoreRoot returns the root of the external-input subtree of the workspace's
// artifact cache:
//
//	<workspaceRoot>/<cacheDir>/inputs/
//
// It is the single site that names the `inputs/` segment; the build wires the
// service over it and cache-inspection tools enumerate the store from it, so
// the segment is spelled once. The `sha256/<digest>` layout beneath it is
// owned privately by the service.
//
// The workspace root is a parameter rather than left to the caller to prepend
// because the configured cache directory is workspace-relative: it names a
// location inside the workspace, so it resolves to a real path only against
// that root. A signature taking the directory alone can be called correctly
// only by a caller that already joined the two, and one that has not gets a
// relative path — which the process working directory then resolves, giving the
// same workspace as many stores as the places a command was run from. Taking
// both is what makes that call impossible to write.
func StoreRoot(workspaceRoot, cacheDir string) string {
	return filepath.Join(workspaceRoot, cacheDir, "inputs")
}

// NewService constructs the external-inputs service over the store rooted at
// root; the caller passes its artifact cache's inputs directory, and the
// service owns the `sha256/<digest>` layout beneath it. acquire enables
// acquisition of unpinned URLs; when false the closure is frozen — pinned
// inputs are served, but an unpinned URL is an error.
//
// client transports plain HTTP(S) fetches and is obtained from the
// network-egress gate (pkg/egress) by the caller. It is nil in two distinct
// cases, and a nil client means the same thing in both: pinned inputs are still
// served from the store, while any fetch — an acquisition or a missing-blob
// refill — refuses with ErrOfflineEgress. The gate returns none for an offline
// workspace, which is the case that error names; and the transport-free variant
// of the consumption posture passes none at all, by construction, so it cannot
// reach the network however the workspace is configured. A caller composing that
// variant therefore has no missing-blob refill, and a pinned entry whose store
// blob has gone missing refuses there rather than being repaired — which is why
// it is not what a rendering surface composes.
//
// pinned is the lockfile's external-inputs view; when it carries duplicate URLs
// the last record wins.
func NewService(root string, acquire bool, client *http.Client, pinned []lockfile.ExternalInput) *Service {
	if client != nil {
		client.Timeout = defaultFetchTimeout
	}
	records := make(map[string]lockfile.ExternalInput, len(pinned))
	for _, rec := range pinned {
		records[rec.URL] = cloneRecord(rec)
	}
	return &Service{
		root:     root,
		acquire:  acquire,
		client:   client,
		records:  records,
		acquired: map[string]bool{},
	}
}

// Fetch returns the bytes for rawURL under the pin discipline, recording
// requiredBy in the matching record's provenance.
//
//   - A pinned URL is served from the store blob named by its recorded
//     digest; the blob is verified on every read, and a mismatch is a loud
//     store-corruption error. A missing blob is refilled from the network
//     wherever this service holds a transport — the refill is verified against
//     the recorded digest before anything is written or returned, so it cannot
//     change output — and refuses where it holds none. A pinned entry never
//     re-pins.
//   - An unpinned URL with acquisition enabled is fetched, pinned into the
//     store under its digest, and recorded.
//   - An unpinned URL with acquisition disabled is an error naming the URL
//     and the remedy.
//
// The URL must pass the scheme allowlist (see classify) before any of the
// above applies.
func (s *Service) Fetch(ctx context.Context, rawURL string, requiredBy string) ([]byte, error) {
	kind, err := classify(rawURL)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if rec, ok := s.records[rawURL]; ok {
		return s.servePinned(ctx, kind, rawURL, rec, requiredBy)
	}
	if !s.acquire {
		return nil, fmt.Errorf(
			"%q is not pinned in the lockfile; run `aggrega pull --release <release>` to pin every external input this Release's render consumes, then build again: %w",
			rawURL, ErrAcquisitionDisabled())
	}
	return s.acquireAndPin(ctx, kind, rawURL, requiredBy)
}

// AcquiredRecords returns the records for URLs pinned by this Service
// instance — URLs absent from the pinned view at construction — sorted by URL
// for deterministic emission. The returned records do not alias the Service's
// internal state.
func (s *Service) AcquiredRecords() []lockfile.ExternalInput {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]lockfile.ExternalInput, 0, len(s.acquired))
	for u := range s.acquired {
		out = append(out, cloneRecord(s.records[u]))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].URL < out[j].URL })
	return out
}

// Records returns every record the Service holds — the construction-time
// pinned view with any provenance appended since, plus this instance's
// acquisitions — sorted by URL for deterministic emission. The full set is
// what a lockfile write persists as external_inputs. The returned records do
// not alias the Service's internal state.
func (s *Service) Records() []lockfile.ExternalInput {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]lockfile.ExternalInput, 0, len(s.records))
	for _, rec := range s.records {
		out = append(out, cloneRecord(rec))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].URL < out[j].URL })
	return out
}

// servePinned serves a pinned URL: store read with per-read verification, or
// a digest-gated network refill when the blob is missing. Callers hold s.mu.
func (s *Service) servePinned(ctx context.Context, kind inputKind, rawURL string, rec lockfile.ExternalInput, requiredBy string) ([]byte, error) {
	if !isHexDigest(rec.Sha256) {
		return nil, fmt.Errorf("externalinputs: %q: pinned digest %q is not a lowercase hex sha256", rawURL, rec.Sha256)
	}
	blobPath := s.blobPath(rec.Sha256)
	b, err := os.ReadFile(blobPath)
	switch {
	case err == nil:
		if verr := verifyStoredBlob(kind, rawURL, rec, b); verr != nil {
			return nil, verr
		}
	case os.IsNotExist(err):
		b, err = s.refill(ctx, kind, rawURL, rec)
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("externalinputs: read pinned blob %s for %q: %w", blobPath, rawURL, err)
	}
	s.appendProvenance(rawURL, requiredBy)
	return b, nil
}

// verifyStoredBlob checks a present store blob against its pinned record
// before the bytes are served. An https input re-hashes the payload on every
// read, so a tampered or corrupted store surfaces loudly with both digests.
// An oci input is pinned by its manifest digest, which commits to the payload
// through the registry's manifest document rather than by hashing the payload
// directly; the store holds only the payload, so a present oci blob is
// checked against the recorded size, and full digest verification anchors at
// acquisition and at every digest-addressed refill.
func verifyStoredBlob(kind inputKind, rawURL string, rec lockfile.ExternalInput, b []byte) error {
	switch kind {
	case kindOCI:
		if int64(len(b)) != rec.Size {
			return fmt.Errorf("externalinputs: %q: store blob sha256/%s is %d bytes but the pinned record says %d: %w",
				rawURL, rec.Sha256, len(b), rec.Size, ErrStoreCorrupt())
		}
		return nil
	default:
		if got := payloadHex(b); got != rec.Sha256 {
			return fmt.Errorf("externalinputs: %q: store blob sha256 is %s, pinned digest is %s: %w",
				rawURL, got, rec.Sha256, ErrStoreCorrupt())
		}
		return nil
	}
}

// refill refetches a pinned URL whose store blob is missing. The recorded
// digest gates the result before anything is written or returned, so a refill
// can never change output — which is why it runs in any mode, including a
// frozen closure. Callers hold s.mu.
func (s *Service) refill(ctx context.Context, kind inputKind, rawURL string, rec lockfile.ExternalInput) ([]byte, error) {
	switch kind {
	case kindOCI:
		repoRef, _, err := parseOCIReference(rawURL)
		if err != nil {
			return nil, err
		}
		// The refill is manifest-addressed: it pulls by the recorded digest,
		// not by the URL's tag, so a moved tag cannot affect what a pinned
		// entry serves.
		want := "sha256:" + rec.Sha256
		res, err := s.ociPull(ctx, repoRef, want)
		if err != nil {
			return nil, fmt.Errorf("externalinputs: refill %q: %w", rawURL, err)
		}
		if res.Digest != want {
			return nil, fmt.Errorf("externalinputs: %q: refilled manifest digest is %s, pinned digest is %s: %w",
				rawURL, res.Digest, want, ErrDigestMismatch())
		}
		if err := s.writeBlob(rec.Sha256, res.Bytes); err != nil {
			return nil, err
		}
		return res.Bytes, nil
	default:
		b, err := s.httpGet(ctx, rawURL)
		if err != nil {
			return nil, err
		}
		if got := payloadHex(b); got != rec.Sha256 {
			return nil, fmt.Errorf("externalinputs: %q: refetched bytes hash to %s, pinned digest is %s: %w",
				rawURL, got, rec.Sha256, ErrDigestMismatch())
		}
		if err := s.writeBlob(rec.Sha256, b); err != nil {
			return nil, err
		}
		return b, nil
	}
}

// acquireAndPin fetches an unpinned URL, pins its bytes into the store, and
// records the pin. Callers hold s.mu and have already confirmed acquisition
// is enabled.
func (s *Service) acquireAndPin(ctx context.Context, kind inputKind, rawURL, requiredBy string) ([]byte, error) {
	var payload []byte
	var pinHex string
	switch kind {
	case kindOCI:
		repoRef, ref, err := parseOCIReference(rawURL)
		if err != nil {
			return nil, err
		}
		res, err := s.ociPull(ctx, repoRef, ref)
		if err != nil {
			return nil, fmt.Errorf("externalinputs: acquire %q: %w", rawURL, err)
		}
		pinHex, err = manifestHex(res.Digest)
		if err != nil {
			return nil, fmt.Errorf("externalinputs: acquire %q: %w", rawURL, err)
		}
		payload = res.Bytes
	default:
		b, err := s.httpGet(ctx, rawURL)
		if err != nil {
			return nil, err
		}
		payload = b
		pinHex = payloadHex(b)
	}

	if err := s.writeBlob(pinHex, payload); err != nil {
		return nil, err
	}
	rec := lockfile.ExternalInput{
		Sha256: pinHex,
		Size:   int64(len(payload)),
		URL:    rawURL,
	}
	if requiredBy != "" {
		rec.RequiredBy = []string{requiredBy}
	}
	s.records[rawURL] = rec
	s.acquired[rawURL] = true
	return payload, nil
}

// appendProvenance adds requiredBy to a known record's provenance when
// absent. Callers hold s.mu.
func (s *Service) appendProvenance(rawURL, requiredBy string) {
	if requiredBy == "" {
		return
	}
	rec := s.records[rawURL]
	if slices.Contains(rec.RequiredBy, requiredBy) {
		return
	}
	rec.RequiredBy = append(slices.Clone(rec.RequiredBy), requiredBy)
	s.records[rawURL] = rec
}

// httpGet fetches rawURL over the service's HTTP client and returns the body
// bytes. Non-2xx statuses are errors naming the URL and status.
func (s *Service) httpGet(ctx context.Context, rawURL string) ([]byte, error) {
	if s.client == nil {
		return nil, fmt.Errorf("externalinputs: fetch %q: %w", rawURL, egress.ErrOfflineEgress())
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("externalinputs: build request for %q: %w", rawURL, err)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("externalinputs: fetch %q: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("externalinputs: fetch %q: HTTP %d", rawURL, resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("externalinputs: read %q: %w", rawURL, err)
	}
	return b, nil
}

// ociPull resolves and pulls an artifact through pkg/oci's client. ref is a
// tag or a digest reference ("sha256:<hex>"); the underlying pull verifies
// the payload against the manifest's layer descriptor either way.
func (s *Service) ociPull(ctx context.Context, repoRef, ref string) (*oci.PullResult, error) {
	if s.client == nil {
		return nil, fmt.Errorf("externalinputs: pull %q: %w", repoRef, egress.ErrOfflineEgress())
	}
	repo, err := oci.NewRemoteRepository(repoRef, s.client)
	if err != nil {
		return nil, err
	}
	return oci.Pull(ctx, repo, ref)
}

// blobPath returns the store path for a hex digest: <root>/sha256/<hex>.
func (s *Service) blobPath(hexDigest string) string {
	return filepath.Join(s.root, "sha256", hexDigest)
}

// writeBlob atomically writes b to the store under hexDigest: the bytes land
// in a temp file in the same directory and are renamed into place, so a
// concurrent reader never observes a partial blob under a digest name.
func (s *Service) writeBlob(hexDigest string, b []byte) error {
	dir := filepath.Join(s.root, "sha256")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("externalinputs: create store directory %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".pending-*")
	if err != nil {
		return fmt.Errorf("externalinputs: create temp blob in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(b); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("externalinputs: write temp blob %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("externalinputs: close temp blob %s: %w", tmpName, err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("externalinputs: chmod temp blob %s: %w", tmpName, err)
	}
	final := filepath.Join(dir, hexDigest)
	if err := os.Rename(tmpName, final); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("externalinputs: rename temp blob into %s: %w", final, err)
	}
	return nil
}

// inputKind classifies an allowed external-input URL by how it is fetched
// and pinned.
type inputKind int

const (
	// kindHTTP covers https URLs and plain-http URLs to loopback hosts,
	// pinned by the SHA-256 of the payload bytes.
	kindHTTP inputKind = iota
	// kindOCI covers oci:// registry artifact references, pinned by the OCI
	// manifest digest the reference resolves to.
	kindOCI
)

// classify gates rawURL against the scheme allowlist and reports how to fetch
// it. The allowlist and the foreseen consumer of each scheme:
//
//   - https — remote `$ref` schema documents and other TLS-served content a
//     render consumes.
//   - http, loopback hosts only (localhost, 127.0.0.0/8, ::1) — plain-HTTP
//     test servers and local development endpoints. A non-loopback http URL
//     is rejected so cleartext can only ever reach the local machine.
//   - oci — non-vendored Helm chart dependencies pulled from OCI registries,
//     and OCI-distributed kustomize/timoni bases when those providers land.
//
// Every other scheme is rejected with an error naming the scheme and the
// allowed set.
func classify(rawURL string) (inputKind, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return 0, fmt.Errorf("externalinputs: parse URL %q: %w", rawURL, err)
	}
	switch u.Scheme {
	case "https":
		return kindHTTP, nil
	case "http":
		if registryhost.IsLoopback(u.Host) {
			return kindHTTP, nil
		}
		return 0, fmt.Errorf(
			"externalinputs: %q: plain http is allowed for loopback hosts only (allowed schemes: https, http to a loopback host, oci): %w",
			rawURL, ErrSchemeNotAllowed())
	case "oci":
		return kindOCI, nil
	default:
		return 0, fmt.Errorf(
			"externalinputs: %q: scheme %q is not allowed (allowed schemes: https, http to a loopback host, oci): %w",
			rawURL, u.Scheme, ErrSchemeNotAllowed())
	}
}

// parseOCIReference splits an oci:// input URL into the repository reference
// pkg/oci's client accepts and the tag or digest that selects the artifact:
// `oci://host/repo:1.2.3` yields ("host/repo", "1.2.3");
// `oci://host/repo@sha256:<hex>` yields ("host/repo", "sha256:<hex>").
// A reference carrying neither a tag nor a digest is rejected — an
// unversioned OCI reference cannot be pinned reproducibly.
func parseOCIReference(rawURL string) (repoRef, ref string, err error) {
	rest := strings.TrimPrefix(rawURL, "oci://")
	if at := strings.Index(rest, "@"); at >= 0 {
		repoRef, ref = rest[:at], rest[at+1:]
		if hex, ok := strings.CutPrefix(ref, "sha256:"); !ok || !isHexDigest(hex) {
			return "", "", fmt.Errorf("externalinputs: %q: digest reference %q is not sha256:<hex>", rawURL, ref)
		}
		return repoRef, ref, nil
	}
	slash := strings.LastIndex(rest, "/")
	colon := strings.LastIndex(rest, ":")
	// A colon at or before the last slash belongs to the registry host's
	// port, not to a tag.
	if colon <= slash || colon == len(rest)-1 {
		return "", "", fmt.Errorf("externalinputs: %q: an oci input reference must carry a tag or digest", rawURL)
	}
	return rest[:colon], rest[colon+1:], nil
}

// payloadHex returns the lowercase-hex SHA-256 of b — the store key and
// record pin for byte-addressed inputs. It derives from the canonical
// content-address spelling in pkg/digest so the two computations can never
// diverge.
func payloadHex(b []byte) string {
	return digest.BareHex(digest.ContentDigest(b))
}

// manifestHex extracts the hex portion of an OCI manifest digest string
// ("sha256:<hex>"). Any other algorithm is rejected: the input store is
// sha256-addressed.
//
// This is a validating extractor, not a plain prefix strip: it deliberately
// does not route through the shared prefix-free normalizer, which is permissive
// (it also strips the dash form, lowercases, and never errors). manifestHex must
// instead reject any non-sha256 or non-lowercase-hex input, so it keeps its own
// checked path.
func manifestHex(d string) (string, error) {
	hex, ok := strings.CutPrefix(d, "sha256:")
	if !ok || !isHexDigest(hex) {
		return "", fmt.Errorf("manifest digest %q is not sha256-addressed", d)
	}
	return hex, nil
}

// isHexDigest reports whether s is a 64-character lowercase hex string — the
// only shape the store accepts as a blob name. Pinned records are read from a
// user-editable lockfile, so the check also keeps a malformed digest from
// ever reaching a filesystem path.
func isHexDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// cloneRecord deep-copies a record so callers and the Service never share a
// RequiredBy backing array.
func cloneRecord(rec lockfile.ExternalInput) lockfile.ExternalInput {
	out := rec
	out.RequiredBy = slices.Clone(rec.RequiredBy)
	return out
}

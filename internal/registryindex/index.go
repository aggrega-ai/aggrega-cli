// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package registryindex

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// Record is one repository's entry in a host's local searchable index: the
// artifact's index URI, the identity and metadata read from its latest published
// version's manifest annotations, the versions published under it, and the
// withdrawal facts discovered for them.
//
// Versions carries every strict-SemVer version published under the URI, so an
// offline reader answers a version question without a registry round trip.
// ListingDigest is the validator the tag listing was read under, so a later
// refresh can revalidate the tag set with a conditional request and skip the
// re-list when the registry answers that the listing is unchanged; it is empty
// when the registry serves no validator or the listing spanned more than one
// page. Deprecations records, per withdrawn version, the authoritative marker
// discovered from that version's referrers together with that marker's verified
// signature, so a consumer reads a signed claim from the local index rather than
// probing the registry live; a version whose marker does not verify contributes
// no entry. Published carries, for every entry of Versions and in the same order,
// the manifest digest that version's tag resolved to, the verified signer of its
// own signature and its publication date with that date's source, so a reader
// addresses each published version by the bytes it names rather than by a tag.
// All are omitted when empty.
//
// The facets — kind, description, category, labels, plugin type and
// capabilities — are the latest version's alone, read from its manifest. An older
// version's facets are not recorded; a reader wanting them reads that version by
// its digest.
type Record struct {
	URI            string                    `json:"uri"`
	Name           string                    `json:"name"`
	Version        string                    `json:"version"`
	Versions       []string                  `json:"versions,omitempty"`
	Kind           string                    `json:"kind,omitempty"`
	Description    string                    `json:"description,omitempty"`
	Category       string                    `json:"category,omitempty"`
	Labels         *types.OrderedMap[string] `json:"labels,omitempty"`
	PluginType     string                    `json:"pluginType,omitempty"`
	Capabilities   []string                  `json:"capabilities,omitempty"`
	ManifestDigest string                    `json:"manifestDigest,omitempty"`
	ListingDigest  string                    `json:"listingDigest,omitempty"`
	Deprecations   []VersionDeprecation      `json:"deprecations,omitempty"`
	Published      []PublishedVersion        `json:"published,omitempty"`
}

// PublishedVersion is one published version as the index build read it: the
// version, the manifest digest its tag resolved to, the signer of the version's
// own signature where the host's trust policy verified one, and when the version
// was published, where anything records it, with the source that says so.
//
// Signer is set only for a signature that verified, so its presence is a trust
// decision already made, as it is on a recorded deprecation. It is absent for a
// version with no signature, for one whose signatures do not verify, and on a
// host whose policy pins no trust anchor or switches verification off. None of
// those gives a basis for naming a signer, and a reader must not read the absence
// as more than that.
//
// PublishedAt is an RFC 3339 UTC timestamp and PublishedAtSource names where it
// came from (see PublicationDate): the transparency log's attested time for a
// keyless signature this host verified, or the publisher's own statement in a
// published-at referrer. The two are kept apart so a reader never mistakes the
// publisher's clock for an attested time. Both are absent where neither source
// gives a date — a publication that wrote no published-at referrer and whose
// signature this host did not verify as keyless — and a reader must not read that
// absence as more than that either.
//
// Only manifests, signature referrers and published-at referrers are read to
// produce it; the entity's content is never fetched.
type PublishedVersion struct {
	Version           string                      `json:"version"`
	ManifestDigest    string                      `json:"manifestDigest"`
	Signer            *lockfile.VerifiedSignature `json:"signer,omitempty"`
	PublishedAt       string                      `json:"publishedAt,omitempty"`
	PublishedAtSource string                      `json:"publishedAtSource,omitempty"`
}

// CoversVersions reports whether the record carries a published-version entry
// for every version it lists, in the same order. A record written before those
// entries were recorded does not, so a refresh reads that repository again
// rather than reuse it.
//
// It does not ask whether a publication date was looked for. A version may
// legitimately have none, so an entry without one says nothing about when it was
// written, and a record written before dates were read is reused by a
// conditional refresh until a full refresh reads the repository again.
func (r Record) CoversVersions() bool {
	if len(r.Published) != len(r.Versions) {
		return false
	}
	for i, v := range r.Versions {
		if r.Published[i].Version != v || r.Published[i].ManifestDigest == "" {
			return false
		}
	}
	return true
}

// VersionDeprecation pairs a strict-SemVer version with the authoritative
// deprecation marker discovered on that version's referrers, plus the verified
// signature of that marker. The whole marker is stored (all its fields), so a
// display reconstructs the same view off the local index that a live referrer
// probe produced. The marshalled marker carries a fixed field order, so a record
// is byte-identical for identical registry state.
//
// Signer is what makes the record a trust decision rather than a hint: a marker
// is recorded only when its OWN signature verifies against the honored signer
// set, so a present record implies a verified one and a consumer needs no second
// judgement. It is a pointer so a record written before verification existed
// decodes with no signer and is therefore not honored — the safe direction, since
// an unverified record can then only fail to exclude a version, never exclude one
// on unauthenticated evidence.
type VersionDeprecation struct {
	Version string                      `json:"version"`
	Marker  deprecation.Marker          `json:"marker"`
	Signer  *lockfile.VerifiedSignature `json:"signer,omitempty"`
}

// errNotBuilt reports that a registry host has no local searchable index on
// disk. It is distinct from a read or parse failure so a caller can tell "the
// index has not been built yet" from a genuine on-disk fault and fail loudly
// with the right guidance.
var errNotBuilt = errors.New("no local index has been built for this registry")

// IsNotBuilt reports whether an error from LoadHost means the host simply has no
// index yet, as opposed to a genuine on-disk fault.
//
// It is a predicate rather than an exported handle deliberately. This is an
// internal classification a caller branches on to write its own guidance — one
// surface offers to build the index, another carries on unscreened — and none of
// them reports it to a user as a condition. A condition a user is told to match
// carries a short code and a documented entry, and this is not one of those; an
// exported handle would enrol it in that register by its shape alone.
func IsNotBuilt(err error) bool { return errors.Is(err, errNotBuilt) }

// Loaded is one host's local searchable index read back off disk: the host it
// was built from, its entity records in the deterministic order the index writer
// stored them, and the freshness timestamp from the sibling file. HasTimestamp
// distinguishes a genuinely absent timestamp sibling (LastUpdated is the zero
// time) from one that happens to parse to the zero time, so a caller can decide
// how to present freshness.
type Loaded struct {
	Host         string
	Records      []Record
	LastUpdated  time.Time
	HasTimestamp bool
}

// LoadHost reads one host's local searchable index from the artifact cache and
// returns its records plus the freshness timestamp. The host's per-repository
// files live in a directory located through the single index-path helper, and
// the timestamp sibling beside it, so a reader and the writer cannot disagree
// about a host's on-disk layout.
//
// An absent host directory reports as an unbuilt index, which IsNotBuilt tells
// apart from a read or parse fault, so the caller can distinguish "run the index
// build first" from a corrupt cache. A present directory with no per-repo files
// is a built-but-empty index (zero records). A per-repo file that cannot be read
// or unmarshalled returns a wrapped error naming the path. Records are sorted
// into the deterministic URI-then-version order at load, so the assembled order
// does not depend on the directory walk order.
//
// A missing timestamp sibling is not fatal: the records are returned with
// HasTimestamp false and the zero time, since the records stand on their own and
// only the freshness display depends on the timestamp. A timestamp sibling that
// is present but unreadable or not a valid RFC3339 line is a genuine fault and
// returns a wrapped error.
//
// The host directory, each record and the timestamp are held, before each is
// read, to the root the index's write is held to (writescope.RootForCache): one
// that leads through a link out of that root is not read, and the read hold's
// refusal is returned as itself, naming the link — neither an unbuilt index nor a
// read fault, so IsNotBuilt does not take it for one.
func LoadHost(workspaceRoot, cacheDir, host string) (Loaded, error) {
	base := oci.IndexBasePath(workspaceRoot, cacheDir, host)
	hostDir := base
	heldTo := writescope.RootForCache(workspaceRoot, cacheDir)
	if err := readscope.CheckCacheRead(heldTo, hostDir); err != nil {
		return Loaded{}, err
	}
	entries, err := os.ReadDir(hostDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Loaded{}, errNotBuilt
		}
		return Loaded{}, fmt.Errorf("read index %s: %w", hostDir, err)
	}
	var records []Record
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		recordPath := filepath.Join(hostDir, entry.Name())
		if err := readscope.CheckCacheRead(heldTo, recordPath); err != nil {
			return Loaded{}, err
		}
		data, rerr := os.ReadFile(recordPath)
		if rerr != nil {
			return Loaded{}, fmt.Errorf("read index %s: %w", recordPath, rerr)
		}
		var rec Record
		if uerr := json.Unmarshal(data, &rec); uerr != nil {
			return Loaded{}, fmt.Errorf("parse index %s: %w", recordPath, uerr)
		}
		records = append(records, rec)
	}
	SortRecords(records)
	result := Loaded{Host: host, Records: records}

	timestampPath := base + ".updated"
	if err := readscope.CheckCacheRead(heldTo, timestampPath); err != nil {
		return Loaded{}, err
	}
	stamp, err := os.ReadFile(timestampPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return result, nil
		}
		return Loaded{}, fmt.Errorf("read index timestamp %s: %w", timestampPath, err)
	}
	ts, err := time.Parse(time.RFC3339, strings.TrimSpace(string(stamp)))
	if err != nil {
		return Loaded{}, fmt.Errorf("parse index timestamp %s: %w", timestampPath, err)
	}
	result.LastUpdated = ts
	result.HasTimestamp = true
	return result, nil
}

// LessRecordURIVersion is the deterministic order for index records: by URI,
// then by version within a URI. It is the single ordering both the local index
// writer and any consumer that aggregates records across hosts sort by, so a
// reader cannot drift from the order the writer stamped.
func LessRecordURIVersion(a, b Record) bool {
	if a.URI != b.URI {
		return a.URI < b.URI
	}
	return a.Version < b.Version
}

// SortRecords sorts records in place into the deterministic URI-then-version
// order shared by the index writer and its consumers.
func SortRecords(records []Record) {
	sort.Slice(records, func(i, j int) bool {
		return LessRecordURIVersion(records[i], records[j])
	})
}

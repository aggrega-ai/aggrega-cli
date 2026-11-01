// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package registryops

import (
	"bytes"
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

// The reads a fault can be aimed at: the three filtered referrer listings a
// version's signer, date and marker are read from, the manifest of a referrer, and
// a blob — a signature's, a marker's or a statement's payload.
const (
	readSignatureListing   = "signature listing"
	readMarkerListing      = "marker listing"
	readPublishedAtListing = "published-at listing"
	readReferrerManifest   = "referrer manifest"
	readBlob               = "blob"
)

// readFault fails one kind of read of one repository, the way a registry or the
// network between does in passing or for good: a status (with a Retry-After header
// where retryAfter is set), a connection reset with no answer, or an answer whose
// body is cut short. times is how many matching reads fail; zero or less fails
// every one. The registry fills in when each matching read arrived and how many it
// failed.
type readFault struct {
	repo       string
	read       string
	times      int
	status     int
	retryAfter string
	reset      bool
	truncate   bool

	arrivals []time.Time
	fired    int
}

func (r *validatingRegistry) addFault(f *readFault) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.faults = append(r.faults, f)
}

// faultState returns how many reads a fault failed and when every matching read
// arrived.
func (r *validatingRegistry) faultState(f *readFault) (int, []time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return f.fired, slices.Clone(f.arrivals)
}

// readOf names the read a request is, among those a fault can be aimed at, and the
// repository it reads.
func (r *validatingRegistry) readOf(req *http.Request) (repo, read string) {
	if req.Method != http.MethodGet {
		return "", ""
	}
	path := req.URL.Path
	switch {
	case strings.Contains(path, "/referrers/"):
		repo = strings.TrimPrefix(path[:strings.LastIndex(path, "/referrers/")], "/v2/")
		switch req.URL.Query().Get("artifactType") {
		case signing.BundleMediaType:
			return repo, readSignatureListing
		case deprecation.ArtifactType:
			return repo, readMarkerListing
		case oci.MediaTypePublishedAt:
			return repo, readPublishedAtListing
		}
	case strings.Contains(path, "/manifests/"):
		i := strings.LastIndex(path, "/manifests/")
		repo = strings.TrimPrefix(path[:i], "/v2/")
		if r.isReferrer(repo, path[i+len("/manifests/"):]) {
			return repo, readReferrerManifest
		}
	case strings.Contains(path, "/blobs/") && !strings.Contains(path, "/uploads/"):
		return strings.TrimPrefix(path[:strings.LastIndex(path, "/blobs/")], "/v2/"), readBlob
	}
	return "", ""
}

// injectFault fails the request where a fault is aimed at it and has failures
// left, and reports whether it did. A failed read is counted under its own kind,
// so the counts of reads served are the counts a clean registry would show.
func (r *validatingRegistry) injectFault(w http.ResponseWriter, req *http.Request) bool {
	repo, read := r.readOf(req)
	if read == "" {
		return false
	}
	r.mu.Lock()
	var hit *readFault
	for _, f := range r.faults {
		if f.repo != repo || f.read != read {
			continue
		}
		f.arrivals = append(f.arrivals, time.Now())
		if f.times <= 0 || f.fired < f.times {
			f.fired++
			hit = f
		}
		break
	}
	r.mu.Unlock()
	if hit == nil {
		return false
	}
	r.count("failed " + read)
	switch {
	case hit.reset:
		resetConnection(w)
	case hit.truncate:
		r.serveCutShort(w, req)
	default:
		if hit.retryAfter != "" {
			w.Header().Set("Retry-After", hit.retryAfter)
		}
		http.Error(w, "the registry failed this read", hit.status)
	}
	return true
}

// resetConnection drops the connection without an answer, with a reset rather
// than an orderly close, as a connection the peer resets reaches a client.
func resetConnection(w http.ResponseWriter) {
	conn, _, err := w.(http.Hijacker).Hijack()
	if err != nil {
		return
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetLinger(0)
	}
	_ = conn.Close()
}

// serveCutShort answers the read as the registry would, announcing the whole
// body's length, and closes the connection after half of it.
func (r *validatingRegistry) serveCutShort(w http.ResponseWriter, req *http.Request) {
	rec := httptest.NewRecorder()
	r.inner.ServeHTTP(rec, req)
	body := rec.Body.Bytes()
	conn, buf, err := w.(http.Hijacker).Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	fmt.Fprintf(buf, "HTTP/1.1 %d %s\r\n", rec.Code, http.StatusText(rec.Code))
	rec.Header().Set("Content-Length", strconv.Itoa(len(body)))
	_ = rec.Header().Write(buf)
	_, _ = buf.WriteString("\r\n")
	_, _ = buf.Write(body[:len(body)/2])
	_ = buf.Flush()
}

// failureFixture is one registry holding the two repositories every test below
// reads, each version signed with a key the workspaces pin: component/core/api
// with 1.0.0, which also carries a signed deprecation marker, and 1.1.0; and
// component/core/web with 0.1.0.
type failureFixture struct {
	reg    *validatingRegistry
	signer signing.Signer
	pub    string
}

const (
	failingRepo = "component/core/api"
	cleanRepo   = "component/core/web"
)

func newFailureFixture(t *testing.T) *failureFixture {
	t.Helper()
	signer, pub := keyedSigner(t)
	reg := newValidatingRegistry(t)
	api := reg.repository(t, failingRepo)
	publish(t, api, "1.0.0", "deprecated")
	publish(t, api, "1.1.0", "current")
	signTag(t, api, "1.0.0", signer)
	signTag(t, api, "1.1.0", signer)
	deprecateTag(t, api, failingRepo, "1.0.0", signer)
	web := reg.repository(t, cleanRepo)
	publish(t, web, "0.1.0", "signed")
	signTag(t, web, "0.1.0", signer)
	return &failureFixture{reg: reg, signer: signer, pub: pub}
}

// workspace resolves a fresh workspace indexing the fixture's registry under the
// trust policy pinning publicKey.
func (f *failureFixture) workspace(t *testing.T, publicKey string) *refreshFixture {
	t.Helper()
	root := t.TempDir()
	globalPath := filepath.Join(t.TempDir(), "config.yaml")
	return &refreshFixture{reg: f.reg, root: root, globalPath: globalPath,
		rw: refreshWorkspace(t, root, globalPath, f.reg.host, publicKey)}
}

// updateResult runs one walk and returns what it reported and printed. The walk
// itself must succeed: a repository it could not read is reported, never a
// failed registry.
func updateResult(t *testing.T, ws *refreshFixture, opts UpdateOptions) (UpdateResult, string) {
	t.Helper()
	opts.Now = func() time.Time { return time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC) }
	var out bytes.Buffer
	res, err := Update(t.Context(), ws.rw, opts, &out)
	if err != nil {
		t.Fatalf("Update: %v\n%s", err, out.String())
	}
	if len(res.Hosts) != 1 {
		t.Fatalf("hosts = %+v; want the one registry", res.Hosts)
	}
	return res, out.String()
}

// indexedRecord returns the record a workspace's index holds for repo, if any.
func indexedRecord(t *testing.T, ws *refreshFixture, repo string) (registryindex.Record, bool) {
	t.Helper()
	loaded, err := registryindex.LoadHost(ws.root, ws.rw.Config().ResolvedArtifactCacheDir(), ws.reg.host)
	if err != nil {
		t.Fatalf("load the index: %v", err)
	}
	for _, rec := range loaded.Records {
		if rec.URI == "oci://"+ws.reg.host+"/"+repo {
			return rec, true
		}
	}
	return registryindex.Record{}, false
}

// skippedRepos names the repositories a walk reported it could not read.
func skippedRepos(hu HostUpdate) []string {
	names := make([]string, 0, len(hu.Skipped))
	for _, s := range hu.Skipped {
		if s.Reason == "" {
			names = append(names, s.Repo+" (with no reason)")
			continue
		}
		names = append(names, s.Repo)
	}
	return names
}

// TestAReadTheRegistryFailsInPassingIsAskedAgain fails one kind of read a few
// times, fewer than the retry budget allows, and requires the walk to write
// exactly the index a walk of the same registry with nothing failing writes. Each
// row aims at one of the reads a version's signer, date or deprecation marker is
// decided from, with one of the failures a registry or the network between gives
// in passing: a 429, a 503, a reset connection, a body cut short.
//
// Each row also requires its fault to have fired as often as it was set to, so a
// fixture whose fault never met a read cannot pass as a walk that recovered.
//
// The 429 row carries a Retry-After header of two seconds, and requires the read
// to have been asked again no sooner than a second and a half later. Asked again
// after the walk's own first backoff alone, it would come half a second later; a
// slow machine only lengthens the gap, so the bound cannot be met by a walk that
// ignores the header, however loaded the machine. Requested 2 s, threshold 1.5 s,
// the walk's own first backoff 0.5 s.
func TestAReadTheRegistryFailsInPassingIsAskedAgain(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name  string
		fault readFault
	}{
		{"a 429 with Retry-After on the signature listing", readFault{read: readSignatureListing, times: 1, status: http.StatusTooManyRequests, retryAfter: "2"}},
		{"a 503 on the signature listing", readFault{read: readSignatureListing, times: 2, status: http.StatusServiceUnavailable}},
		{"a 503 on the deprecation marker listing", readFault{read: readMarkerListing, times: 2, status: http.StatusServiceUnavailable}},
		{"a 429 on the published-at listing", readFault{read: readPublishedAtListing, times: 2, status: http.StatusTooManyRequests}},
		{"a reset on a referrer's manifest", readFault{read: readReferrerManifest, times: 2, reset: true}},
		{"a 503 on a referrer's payload", readFault{read: readBlob, times: 2, status: http.StatusServiceUnavailable}},
		{"a referrer's payload cut short", readFault{read: readBlob, times: 2, truncate: true}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			f := newFailureFixture(t)
			clean := f.workspace(t, f.pub)
			updateResult(t, clean, UpdateOptions{})
			want := clean.files(t)

			fault := row.fault
			fault.repo = failingRepo
			f.reg.addFault(&fault)
			failing := f.workspace(t, f.pub)
			res, out := updateResult(t, failing, UpdateOptions{})

			fired, arrivals := f.reg.faultState(&fault)
			if fired != fault.times {
				t.Fatalf("the fault failed %d read(s); want %d, so the walk below did not meet the failure it is meant to recover from", fired, fault.times)
			}
			if got := skippedRepos(res.Hosts[0]); len(got) != 0 {
				t.Errorf("repositories reported unreadable: %v; every failure here passes within the retry budget\n%s", got, out)
			}
			if got := failing.files(t); !maps.Equal(got, want) {
				t.Errorf("the index written through the failures differs from the one a clean registry gives:\nclean   %v\nfailing %v\n%s", want, got, out)
			}
			if fault.retryAfter != "" {
				if len(arrivals) < 2 {
					t.Fatalf("the refused read was not asked again (%d arrival(s))", len(arrivals))
				}
				if gap := arrivals[1].Sub(arrivals[0]); gap < 1500*time.Millisecond {
					t.Errorf("the read refused with Retry-After: %s was asked again after %v; want no sooner than the registry asked", fault.retryAfter, gap)
				}
			}
		})
	}
}

// TestAReadThatKeepsFailingKeepsTheEarlierRecordAndNamesTheRepository fails one
// read of a repository on every attempt, after an earlier walk indexed it, and
// requires the walk to leave the repository's earlier record in place, byte for
// byte, name the repository among those it could not read, and say in its
// commentary that the earlier record is kept — on a refresh that has a change to
// read, and on a full re-read alike. The other repository is indexed as usual.
//
// The last row changes the registry's trust policy first. The earlier record was
// judged under the policy it replaced, so keeping it would put a verdict the
// policy in force never reached into the index: the repository is left out
// instead, and named.
func TestAReadThatKeepsFailingKeepsTheEarlierRecordAndNamesTheRepository(t *testing.T) {
	t.Parallel()

	t.Run("a refresh with a change to read", func(t *testing.T) {
		t.Parallel()
		f := newFailureFixture(t)
		ws := f.workspace(t, f.pub)
		updateResult(t, ws, UpdateOptions{})
		before := ws.files(t)

		// A publication date attached to 1.1.0 changes its referrers, so the refresh
		// has to read it again.
		if _, err := oci.AttachPublishedAt(t.Context(), f.reg.repository(t, failingRepo), "1.1.0",
			time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)); err != nil {
			t.Fatalf("attach a publication date: %v", err)
		}
		f.reg.addFault(&readFault{repo: failingRepo, read: readSignatureListing, status: http.StatusServiceUnavailable})
		res, out := updateResult(t, ws, UpdateOptions{})

		assertKept(t, ws, res, out, before)
	})

	t.Run("a full re-read", func(t *testing.T) {
		t.Parallel()
		f := newFailureFixture(t)
		ws := f.workspace(t, f.pub)
		updateResult(t, ws, UpdateOptions{})
		before := ws.files(t)

		f.reg.addFault(&readFault{repo: failingRepo, read: readReferrerManifest, reset: true})
		res, out := updateResult(t, ws, UpdateOptions{Upgrade: true})

		assertKept(t, ws, res, out, before)
	})

	t.Run("a record judged under another trust policy is not kept", func(t *testing.T) {
		t.Parallel()
		f := newFailureFixture(t)
		ws := f.workspace(t, f.pub)
		updateResult(t, ws, UpdateOptions{})

		_, otherPub := keyedSigner(t)
		ws.rw = refreshWorkspace(t, ws.root, ws.globalPath, f.reg.host, otherPub)
		f.reg.addFault(&readFault{repo: failingRepo, read: readSignatureListing, status: http.StatusServiceUnavailable})
		res, out := updateResult(t, ws, UpdateOptions{})

		if got := skippedRepos(res.Hosts[0]); !slices.Equal(got, []string{failingRepo}) {
			t.Errorf("repositories reported unreadable = %v; want %s\n%s", got, failingRepo, out)
		}
		if rec, found := indexedRecord(t, ws, failingRepo); found {
			t.Errorf("the index keeps a record judged under the trust policy it no longer has: %+v\n%s", rec, out)
		}
		if !strings.Contains(out, "skipped "+failingRepo+": ") {
			t.Errorf("the commentary does not name the repository left out:\n%s", out)
		}
		if _, found := indexedRecord(t, ws, cleanRepo); !found {
			t.Errorf("the repository with nothing failing was not indexed\n%s", out)
		}
	})
}

// assertKept holds a walk to having kept failingRepo's earlier record and named
// it, and to having indexed cleanRepo.
func assertKept(t *testing.T, ws *refreshFixture, res UpdateResult, out string, before map[string]string) {
	t.Helper()
	if got := skippedRepos(res.Hosts[0]); !slices.Equal(got, []string{failingRepo}) {
		t.Errorf("repositories reported unreadable = %v; want %s\n%s", got, failingRepo, out)
	}
	if res.Hosts[0].Indexed != 2 {
		t.Errorf("indexed = %d; want 2, the kept record and the other repository\n%s", res.Hosts[0].Indexed, out)
	}
	if got := ws.files(t); !maps.Equal(got, before) {
		t.Errorf("the walk changed the index; the repository it could not read keeps its earlier record and nothing else changed:\nbefore %v\nafter  %v\n%s", before, got, out)
	}
	if !strings.Contains(out, "could not read "+failingRepo+": ") || !strings.Contains(out, "its earlier record is kept") {
		t.Errorf("the commentary does not say the repository could not be read and its earlier record is kept:\n%s", out)
	}
	if strings.Contains(out, failingRepo+":1.0.0: marker present") {
		t.Errorf("the commentary reports a marker read in the walk that was not recorded:\n%s", out)
	}
}

// TestAVersionIsNeverRecordedUnsignedOrUnmarkedBecauseAReadFailed builds an index
// with no earlier one while one read of a repository fails on every attempt, and
// requires the repository to be left out of the index and named, never recorded
// with what the failure left out: a version with no signer, no date or no
// deprecation marker. The other repository is indexed, signed, as usual.
//
// Turning the failure back into absence — recording the version from whatever was
// read — fails every row: the signature rows record 1.0.0 or 1.1.0 with no
// signer, the marker row records 1.0.0 with no marker, and the published-at row
// records the repository as though its date read had answered.
func TestAVersionIsNeverRecordedUnsignedOrUnmarkedBecauseAReadFailed(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name  string
		fault readFault
	}{
		{"the signature listing answers 503", readFault{read: readSignatureListing, status: http.StatusServiceUnavailable}},
		{"a referrer's manifest is reset", readFault{read: readReferrerManifest, reset: true}},
		{"a referrer's payload is cut short", readFault{read: readBlob, truncate: true}},
		{"the deprecation marker listing answers 429", readFault{read: readMarkerListing, status: http.StatusTooManyRequests}},
		{"the published-at listing answers 503", readFault{read: readPublishedAtListing, status: http.StatusServiceUnavailable}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			f := newFailureFixture(t)
			fault := row.fault
			fault.repo = failingRepo
			f.reg.addFault(&fault)
			ws := f.workspace(t, f.pub)
			res, out := updateResult(t, ws, UpdateOptions{})

			if fired, _ := f.reg.faultState(&fault); fired == 0 {
				t.Fatal("the fault never met a read, so the walk below is not a walk through a failure")
			}
			if rec, found := indexedRecord(t, ws, failingRepo); found {
				t.Errorf("the repository was recorded from a walk whose read failed: %+v\n%s", rec, out)
			}
			if got := skippedRepos(res.Hosts[0]); !slices.Equal(got, []string{failingRepo}) {
				t.Errorf("repositories reported unreadable = %v; want %s\n%s", got, failingRepo, out)
			}
			rec, found := indexedRecord(t, ws, cleanRepo)
			if !found || len(rec.Published) != 1 || rec.Published[0].Signer == nil {
				t.Errorf("the repository with nothing failing was not indexed signed: %+v\n%s", rec, out)
			}
		})
	}
}

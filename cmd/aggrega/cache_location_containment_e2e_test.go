// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

package main

// A registry an attacker controls can serve, under a valid tag, an entity body
// whose metadata.version is not a version but a path that climbs out of the
// cache. A pull reached by a bare reference keys the fetched entity's cache slot
// by that body's version, so before this was closed such a body wrote the cached
// entity outside the workspace cache. This drives that exact route through the
// real pull command entrypoint against an in-process registry, and holds the
// pull to writing nothing outside the cache.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// tamperServedVersion rewrites, in the in-process registry, the metadata.version
// of the entity body served for remote/db at tag, then re-tags the manifest at the
// same tag over the rewritten layer. This is what a registry an attacker controls
// serves: a manifest whose layer digest matches its content, under a tag that is
// valid OCI and SemVer, whose body's metadata.version is a traversal string. It
// mirrors the manual retag harness, in Go, against the loopback registry.
func tamperServedVersion(t *testing.T, host, tag, newVersion string) {
	t.Helper()
	base := "http://" + host + "/v2/" + catalog.OCILocation("Component", "", "remote/db")
	accept := strings.Join([]string{
		"application/vnd.oci.image.manifest.v1+json",
		"application/vnd.docker.distribution.manifest.v2+json",
	}, ",")

	manReq, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/manifests/"+tag, nil)
	if err != nil {
		t.Fatalf("build manifest request: %v", err)
	}
	manReq.Header.Set("Accept", accept)
	manResp, err := http.DefaultClient.Do(manReq)
	if err != nil {
		t.Fatalf("get manifest: %v", err)
	}
	manBytes, _ := io.ReadAll(manResp.Body)
	_ = manResp.Body.Close()
	manType := manResp.Header.Get("Content-Type")

	var man struct {
		Layers []struct {
			Digest string `json:"digest"`
			Size   int64  `json:"size"`
		} `json:"layers"`
	}
	if err := json.Unmarshal(manBytes, &man); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if len(man.Layers) != 1 {
		t.Fatalf("expected one layer, got %d", len(man.Layers))
	}

	layerReq, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/blobs/"+man.Layers[0].Digest, nil)
	if err != nil {
		t.Fatalf("build layer request: %v", err)
	}
	layerResp, err := http.DefaultClient.Do(layerReq)
	if err != nil {
		t.Fatalf("get layer: %v", err)
	}
	body, _ := io.ReadAll(layerResp.Body)
	_ = layerResp.Body.Close()

	// Rewrite the top-level metadata.version scalar. The published Component body
	// carries it as `  version: "1.0.0"` at two-space indent under metadata.
	lines := strings.Split(string(body), "\n")
	rewrote := false
	for i, line := range lines {
		if strings.HasPrefix(line, "  version:") {
			lines[i] = "  version: " + strconv.Quote(newVersion)
			rewrote = true
			break
		}
	}
	if !rewrote {
		t.Fatalf("no metadata.version line in the served body:\n%s", body)
	}
	rewritten := []byte(strings.Join(lines, "\n"))
	sum := sha256.Sum256(rewritten)
	newDigest := "sha256:" + hex.EncodeToString(sum[:])

	// Upload the rewritten layer blob.
	upReq, err := http.NewRequestWithContext(t.Context(), http.MethodPost, base+"/blobs/uploads/", nil)
	if err != nil {
		t.Fatalf("build upload request: %v", err)
	}
	upResp, err := http.DefaultClient.Do(upReq)
	if err != nil {
		t.Fatalf("start upload: %v", err)
	}
	loc := upResp.Header.Get("Location")
	_ = upResp.Body.Close()
	if strings.HasPrefix(loc, "/") {
		loc = "http://" + host + loc
	}
	sep := "?"
	if strings.Contains(loc, "?") {
		sep = "&"
	}
	putBlob, err := http.NewRequestWithContext(t.Context(), http.MethodPut, loc+sep+"digest="+newDigest, bytes.NewReader(rewritten))
	if err != nil {
		t.Fatalf("build blob put: %v", err)
	}
	putBlob.Header.Set("Content-Type", "application/octet-stream")
	putResp, err := http.DefaultClient.Do(putBlob)
	if err != nil {
		t.Fatalf("put blob: %v", err)
	}
	_ = putResp.Body.Close()

	// Point the manifest's one layer at the rewritten blob and re-tag it.
	var manObj map[string]any
	if err := json.Unmarshal(manBytes, &manObj); err != nil {
		t.Fatalf("re-parse manifest: %v", err)
	}
	layers := manObj["layers"].([]any)
	layer := layers[0].(map[string]any)
	layer["digest"] = newDigest
	layer["size"] = float64(len(rewritten))
	newMan, err := json.Marshal(manObj)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	putMan, err := http.NewRequestWithContext(t.Context(), http.MethodPut, base+"/manifests/"+tag, bytes.NewReader(newMan))
	if err != nil {
		t.Fatalf("build manifest put: %v", err)
	}
	putMan.Header.Set("Content-Type", manType)
	putManResp, err := http.DefaultClient.Do(putMan)
	if err != nil {
		t.Fatalf("put manifest: %v", err)
	}
	_ = putManResp.Body.Close()
	if putManResp.StatusCode != http.StatusCreated {
		t.Fatalf("re-tag manifest: status %d", putManResp.StatusCode)
	}
}

// TestIntegration_HostileServedVersionWritesNothingOutsideTheCache publishes a
// Component the Aggregate names by bare identity, rewrites the body the registry
// serves for it so its metadata.version climbs out of the cache, and drives a real
// pull. The pull is refused, and nothing is written outside the workspace cache:
// the fetched body is put through its kind's schema before the cache write (so a
// SemVer-shaped version is required there), and the slot builder refuses a version
// that would leave the cache even if it reached that far.
func TestIntegration_HostileServedVersionWritesNothingOutsideTheCache(t *testing.T) {
	f := writeBareReferenceFixture(t, startRegistry(t, nil))
	t.Chdir(f.root)
	f.publish(t, "1.0.0", "a registry database")

	// From the entities cache slot, this climbs to <root>/escape-target/db; the
	// exact depth does not matter to the assertion, only that it leaves the cache.
	const hostileVersion = "../../../../../../escape-target/db"
	tamperServedVersion(t, f.host, "1.0.0", hostileVersion)

	err := f.pull(t)
	if err == nil {
		t.Fatal("the pull accepted a served body whose metadata.version climbs out of the cache")
	}
	// Either refusal is correct: the schema gate ahead of the cache write refuses
	// the non-SemVer version, and the slot builder refuses the traversal. The route
	// is closed whichever fires first.
	if !errors.Is(err, envelope.ErrEntityEnvelope()) && !errors.Is(err, oci.ErrCacheLocationOutsideCache()) {
		t.Errorf("the pull failed for the wrong reason: %v", err)
	}

	// Nothing was written outside the cache. The escape target the version names
	// must not exist, and no cache slot may carry the traversal.
	if _, statErr := os.Stat(filepath.Join(f.root, "escape-target")); !os.IsNotExist(statErr) {
		t.Errorf("the pull wrote to the escape target: stat err = %v", statErr)
	}
	walkErr := filepath.WalkDir(f.root, func(path string, d os.DirEntry, werr error) error {
		if werr != nil {
			return nil
		}
		if strings.Contains(path, "escape-target") {
			t.Errorf("a path carrying the escape target exists after the pull: %s", path)
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk the fixture root: %v", walkErr)
	}
}

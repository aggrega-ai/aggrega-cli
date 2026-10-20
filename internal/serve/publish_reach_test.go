// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
)

// reachSecondRegistry is the second destination the reach rows publish to. Like
// publishTestRegistry it is not a loopback address: every row injects its own
// target, so nothing here can reach it.
const reachSecondRegistry = "second.example.test"

// refusesDates is an in-memory store that refuses every upload of a publication
// date, the way a registry failing that write past its retries leaves a first
// publication: the content and its tag land, and the date beside them does not.
type refusesDates struct {
	*memory.Store
}

func (r *refusesDates) Push(ctx context.Context, desc ocispec.Descriptor, rd io.Reader) error {
	if desc.MediaType == oci.MediaTypePublishedAt {
		return errors.New("the test registry refuses this upload")
	}
	return r.Store.Push(ctx, desc, rd)
}

// reachWorkspace is a workspace whose two destinations each pin the public half
// of one held key, so a signature that key made verifies under either registry's
// trust policy — which is what lets a signing batch find a reference already
// signed — and whose signing block names that key, so every reference of a batch
// is signed with one credential. Nothing requires a signature, so a publication
// lands unsigned and the signing method is what attaches one.
func reachWorkspace(t *testing.T) string {
	t.Helper()
	privPEM, pubPEM, err := signing.GenerateKeyedPEM()
	if err != nil {
		t.Fatalf("GenerateKeyedPEM: %v", err)
	}
	keyPath := filepath.Join(t.TempDir(), "signing-key.pem")
	if err := os.WriteFile(keyPath, privPEM, 0o600); err != nil {
		t.Fatalf("write the signing key: %v", err)
	}
	var pin strings.Builder
	for _, line := range strings.Split(strings.TrimRight(string(pubPEM), "\n"), "\n") {
		pin.WriteString("      " + line + "\n")
	}
	return defaultRegistryWorkspace + "registries:\n" +
		"  - host: " + publishTestRegistry + "\n    public_key: |\n" + pin.String() +
		"  - host: " + reachSecondRegistry + "\n    public_key: |\n" + pin.String() +
		"signing:\n  key_path: " + keyPath + "\n"
}

// reachRow is one thing a publication call can do, and the reach its answer must
// state for it. Each row carries one difference from the row that precedes it.
type reachRow struct {
	name string
	// defect is what a wrong answer on this row would mean.
	defect string
	// run drives the call this row is about, setting up whatever it needs first,
	// and returns the reach the answer stated.
	run            func(t *testing.T, srv *Server, store *memory.Store) publicationReach
	wantRegistries []string
	wantBeyond     bool
}

// TestThePublicationReachOnTheDaemonIsWhatTheCallAttached drives every
// publication method through this surface's own request projection into the
// operation its dispatch runs, against a registry this test holds, and holds each
// answer's reach to what the call actually attached: true exactly when it
// uploaded content or attached a signature, a publication date or a deprecation
// marker, and naming every registry the call addressed, each once, in the order
// it first addressed them.
//
// The dry run is driven through the wire itself, which is the whole of what a
// request can reach without a destination.
func TestThePublicationReachOnTheDaemonIsWhatTheCallAttached(t *testing.T) {
	t.Parallel()

	entityRef := publishTestRegistry + "/component/core/api-server:1.0.0"

	rows := []reachRow{
		{
			name:           "a first publication",
			defect:         "a publication that uploaded its content does not say it reached beyond this machine",
			wantRegistries: []string{publishTestRegistry},
			wantBeyond:     true,
			run: func(t *testing.T, srv *Server, store *memory.Store) publicationReach {
				return publishThroughDaemon(t, srv, store).Reach
			},
		},
		{
			name:           "the same publication run again over a complete version",
			defect:         "a re-run that attached nothing claims a landing that did not happen",
			wantRegistries: []string{publishTestRegistry},
			run: func(t *testing.T, srv *Server, store *memory.Store) publicationReach {
				publishThroughDaemon(t, srv, store)
				again := publishThroughDaemon(t, srv, store)
				if !again.AlreadyPublished {
					t.Fatalf("the second publication is not reported as already published: %+v", again)
				}
				return again.Reach
			},
		},
		{
			name:           "the same publication run again over a version missing its date",
			defect:         "a re-run that attached the date the version lacked does not say it reached beyond this machine",
			wantRegistries: []string{publishTestRegistry},
			wantBeyond:     true,
			run: func(t *testing.T, srv *Server, store *memory.Store) publicationReach {
				cfg, paramErr := srv.publishEntityConfig(publishEntityParams{Entity: "1.0.0/entity.yaml"}, io.Discard)
				if paramErr != nil {
					t.Fatalf("publishEntityConfig: %s", paramErr.Message)
				}
				cfg.PushTarget = &refusesDates{Store: store}
				if _, runErr := runPublishEntity(t.Context(), cfg); runErr == nil {
					t.Fatal("the first publication succeeded; this row needs its date to have been refused")
				}
				again := publishThroughDaemon(t, srv, store)
				if !again.AlreadyPublished {
					t.Fatalf("the completing publication is not reported as already published: %+v", again)
				}
				return again.Reach
			},
		},
		{
			name:           "a dry run",
			defect:         "a dry run claims to have reached beyond this machine",
			wantRegistries: []string{publishTestRegistry},
			run: func(t *testing.T, srv *Server, _ *memory.Store) publicationReach {
				resp := callDaemonMethod(t, srv, "aggrega/publishEntity", map[string]any{
					"entity": "1.0.0/entity.yaml",
					"dryRun": true,
				})
				var res publishEntityResult
				decodeDaemonResult(t, resp, &res)
				return res.Reach
			},
		},
		{
			name:           "a signing batch of references already signed",
			defect:         "a batch that attached nothing claims a landing that did not happen",
			wantRegistries: []string{publishTestRegistry},
			run: func(t *testing.T, srv *Server, store *memory.Store) publicationReach {
				publishThroughDaemon(t, srv, store)
				signThroughDaemon(t, srv, store, entityRef)
				again := signThroughDaemon(t, srv, store, entityRef)
				if len(again.Signed) != 1 || again.Signed[0].Reference != entityRef {
					t.Fatalf("a reference already signed is still reported signed; got %+v", again.Signed)
				}
				return again.Reach
			},
		},
		{
			name:           "a signing batch mixing a reference already signed and one unsigned",
			defect:         "a batch that attached one signature does not say it reached beyond this machine",
			wantRegistries: []string{publishTestRegistry},
			wantBeyond:     true,
			run: func(t *testing.T, srv *Server, store *memory.Store) publicationReach {
				publishThroughDaemon(t, srv, store)
				signThroughDaemon(t, srv, store, entityRef)
				unsigned := publishArtifactThroughDaemon(t, srv, store, "handbook", "2.0.0", publishTestRegistry)
				return signThroughDaemon(t, srv, store, entityRef, unsigned).Reach
			},
		},
		{
			name: "a signing batch spanning two registries under one credential",
			defect: "a batch spanning registries names only one of them, or names them in an order other than " +
				"the one it addressed them in, or names one twice",
			wantRegistries: []string{reachSecondRegistry, publishTestRegistry},
			wantBeyond:     true,
			run: func(t *testing.T, srv *Server, store *memory.Store) publicationReach {
				publishThroughDaemon(t, srv, store)
				second := publishArtifactThroughDaemon(t, srv, store, "handbook", "2.0.0", reachSecondRegistry)
				first := publishArtifactThroughDaemon(t, srv, store, "guide", "3.0.0", publishTestRegistry)
				return signThroughDaemon(t, srv, store, second, entityRef, first).Reach
			},
		},
		{
			name:           "a deprecation",
			defect:         "an attached marker does not say it reached beyond this machine",
			wantRegistries: []string{publishTestRegistry},
			wantBeyond:     true,
			run: func(t *testing.T, srv *Server, store *memory.Store) publicationReach {
				publishThroughDaemon(t, srv, store)
				cfg, paramErr := srv.deprecateArtifactConfig(deprecateArtifactParams{
					Reference: entityRef, Severity: "warning", Reason: "superseded by 2.0.0",
				}, io.Discard)
				if paramErr != nil {
					t.Fatalf("deprecateArtifactConfig: %s", paramErr.Message)
				}
				cfg.Target = store
				res, runErr := runDeprecateArtifact(t.Context(), cfg)
				if runErr != nil {
					t.Fatalf("deprecate: %s", runErr.Message)
				}
				return res.Reach
			},
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			srv, _ := publishFixture(t, reachWorkspace(t))
			got := row.run(t, srv, memory.New())
			if got.BeyondThisMachine != row.wantBeyond {
				t.Errorf("beyondThisMachine = %v; want %v (defect: %s)", got.BeyondThisMachine, row.wantBeyond, row.defect)
			}
			if !slices.Equal(got.Registries, row.wantRegistries) {
				t.Errorf("registries = %q; want %q (defect: %s)", got.Registries, row.wantRegistries, row.defect)
			}
		})
	}
}

// signThroughDaemon signs references held in store through this surface's own
// request projection.
func signThroughDaemon(t *testing.T, srv *Server, store *memory.Store, references ...string) signArtifactsResult {
	t.Helper()
	cfg, paramErr := srv.signArtifactsConfig(signArtifactsParams{References: references}, io.Discard)
	if paramErr != nil {
		t.Fatalf("signArtifactsConfig: %s", paramErr.Message)
	}
	cfg.Target = store
	res, runErr := runSignArtifacts(t.Context(), cfg)
	if runErr != nil {
		t.Fatalf("sign: %s", runErr.Message)
	}
	return res
}

// publishArtifactThroughDaemon publishes the fixture's artifact file into store
// under name and tag at registry, through this surface's own request projection,
// and returns the reference a signing batch addresses it by.
func publishArtifactThroughDaemon(t *testing.T, srv *Server, store *memory.Store, name, tag, registry string) string {
	t.Helper()
	cfg, paramErr := srv.publishArtifactConfig(publishArtifactParams{
		File: "bundle.txt", Name: name, Tag: tag, Registry: registry,
	}, io.Discard)
	if paramErr != nil {
		t.Fatalf("publishArtifactConfig: %s", paramErr.Message)
	}
	cfg.PushTarget = store
	res, runErr := runPublishArtifact(t.Context(), cfg)
	if runErr != nil {
		t.Fatalf("publish the artifact: %s", runErr.Message)
	}
	if !res.Reach.BeyondThisMachine {
		t.Fatalf("an artifact that landed does not say it reached beyond this machine: %+v", res.Reach)
	}
	return res.Reference + ":" + tag
}

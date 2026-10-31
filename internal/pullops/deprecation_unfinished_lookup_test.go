// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pullops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/registry"

	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The registry and artifact the unfinished-lookup rows discover markers for.
const (
	unfinishedLookupHost    = "lookup.example.com"
	unfinishedLookupURI     = unfinishedLookupHost + "/plugin/linter/withdrawn-linter"
	unfinishedLookupVersion = "1.0.0"
)

// The two notices a marker discovery prints when it records nothing, as the rows
// read them.
const (
	unfinishedLookupNotice = "marker lookup did not complete"
	unhonoredMarkerNotice  = "marker present but not honored"
)

// errMarkerListingRefused is the failure refusesMarkerListing answers a
// deprecation-marker listing with.
var errMarkerListingRefused = errors.New("the registry refused the referrer listing")

// refusesMarkerListing is an in-memory registry whose referrer listing fails for
// deprecation markers and answers every other listing as the store would, so a
// row can fail the marker lookup alone while the artifact's own signature lookup
// still answers.
type refusesMarkerListing struct {
	*memory.Store
}

func (r refusesMarkerListing) Referrers(ctx context.Context, desc ocispec.Descriptor, artifactType string, fn func([]ocispec.Descriptor) error) error {
	if artifactType == deprecation.ArtifactType {
		return errMarkerListingRefused
	}
	refs, err := registry.Referrers(ctx, r.Store, desc, artifactType)
	if err != nil {
		return err
	}
	return fn(refs)
}

// unfinishedLookupArtifact publishes a plugin artifact into a fresh store and
// returns the store and the entity a discovery asks about. markerShape attaches an
// unsigned deprecation marker when it is not "": "tagged" tags the marker by its
// digest, so its own signature listing answers that it carries none, and
// "untagged" leaves it untagged, so that listing cannot complete — the in-memory
// store resolves only what it has tagged.
func unfinishedLookupArtifact(t *testing.T, markerShape string) (*memory.Store, pullverify.Entity) {
	t.Helper()
	ctx := context.Background()
	store := memory.New()
	manifestDg, err := oci.Push(ctx, store, unfinishedLookupVersion, oci.MediaTypeComponent, []byte("\x00asm\x01\x00\x00\x00"), nil)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	if markerShape != "" {
		payload, err := deprecation.Marshal(deprecation.Marker{
			Severity: deprecation.SeverityRetracted,
			Reason:   "withdrawn for this test",
			IssuedOn: "2026-05-01T00:00:00Z",
		})
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		marker, err := oci.AttachReferrer(ctx, store, unfinishedLookupVersion, deprecation.ArtifactType, payload)
		if err != nil {
			t.Fatalf("attach the marker: %v", err)
		}
		if markerShape == "tagged" {
			if err := store.Tag(ctx, marker, marker.Digest.String()); err != nil {
				t.Fatalf("tag the marker by digest: %v", err)
			}
		}
	}
	return store, pullverify.Entity{
		URI:            unfinishedLookupURI,
		ManifestDigest: manifestDg,
		Host:           unfinishedLookupHost,
		Kind:           "Plugin",
	}
}

// targetForStore answers every entity with target at the artifact's version.
func targetForStore(target oras.ReadOnlyTarget) pullverify.TargetFor {
	return func(pullverify.Entity) (oras.ReadOnlyTarget, string, error) {
		return target, unfinishedLookupVersion, nil
	}
}

// deprecationDiscoveries are the two passes a pull runs marker discovery through,
// each driven over one entity. A pass that records a marker is reported as an
// error, so a row asserting nothing was recorded reads it from the same value.
var deprecationDiscoveries = []struct {
	name string
	run  func(ctx context.Context, wsCfg workspaceconfig.Config, e pullverify.Entity, targetFor pullverify.TargetFor, diag io.Writer) error
}{
	{
		name: "the closure pass",
		run: func(ctx context.Context, wsCfg workspaceconfig.Config, e pullverify.Entity, targetFor pullverify.TargetFor, diag io.Writer) error {
			return discoverClosureDeprecations(ctx, &lockfile.Lockfile{}, []pullverify.Entity{e}, nil, wsCfg, targetFor, diag)
		},
	},
	{
		name: "the verdict-plugin discovery",
		run: func(ctx context.Context, wsCfg workspaceconfig.Config, e pullverify.Entity, targetFor pullverify.TargetFor, diag io.Writer) error {
			rec, err := discoverVerdictPluginDeprecation(ctx, wsCfg, e, targetFor, diag)
			if rec != nil {
				return fmt.Errorf("a marker was recorded: %+v", *rec)
			}
			return err
		},
	},
}

// TestAnUnfinishedDeprecationLookupIsNotReportedAsAMarker proves, through both
// passes a pull discovers deprecation markers in, that a lookup which never
// learned whether a marker is attached — the registry target could not be
// obtained, or the marker listing failed — is reported as a lookup that did not
// complete, with its cause, and never as a marker that was present and not
// honored. The workspace configures no policy for the registry, so neither halts
// and each row reads the notice. The controls hold the other side: a marker that
// WAS listed and carries no signature, and one whose own signature listing could
// not complete, still read as a marker present and not honored, and an artifact
// carrying no marker prints nothing.
func TestAnUnfinishedDeprecationLookupIsNotReportedAsAMarker(t *testing.T) {
	t.Parallel()
	errTargetRefused := errors.New("no registry target for this test")

	cases := []struct {
		name   string
		defect string
		// target builds the store and the target factory the row discovers through.
		target func(t *testing.T) (pullverify.Entity, pullverify.TargetFor)
		// want is the notice the row must print, "" for none; cause is a fragment
		// of the reason that notice must carry.
		want, cause string
	}{
		{
			name:   "the marker listing fails",
			defect: "a referrer listing that never answered is reported as a marker that was present",
			target: func(t *testing.T) (pullverify.Entity, pullverify.TargetFor) {
				store, e := unfinishedLookupArtifact(t, "")
				return e, targetForStore(refusesMarkerListing{Store: store})
			},
			want:  unfinishedLookupNotice,
			cause: "list deprecation referrers: ",
		},
		{
			name:   "the registry target cannot be obtained",
			defect: "a lookup that never reached the registry is reported as a marker that was present",
			target: func(t *testing.T) (pullverify.Entity, pullverify.TargetFor) {
				_, e := unfinishedLookupArtifact(t, "")
				return e, func(pullverify.Entity) (oras.ReadOnlyTarget, string, error) {
					return nil, "", errTargetRefused
				}
			},
			want:  unfinishedLookupNotice,
			cause: "resolve target: " + errTargetRefused.Error(),
		},
		{
			name:   "control: a listed marker carrying no signature",
			defect: "a marker that was listed and not honored stops being reported as present",
			target: func(t *testing.T) (pullverify.Entity, pullverify.TargetFor) {
				store, e := unfinishedLookupArtifact(t, "tagged")
				return e, targetForStore(store)
			},
			want: unhonoredMarkerNotice,
		},
		{
			name:   "control: a listed marker whose own signature listing cannot complete",
			defect: "the classification reads the failed-listing condition, which a listed marker's signature lookup carries too",
			target: func(t *testing.T) (pullverify.Entity, pullverify.TargetFor) {
				store, e := unfinishedLookupArtifact(t, "untagged")
				return e, targetForStore(store)
			},
			want:  unhonoredMarkerNotice,
			cause: "list marker signature referrers: ",
		},
		{
			name:   "control: an artifact carrying no marker",
			defect: "an artifact with nothing attached is reported",
			target: func(t *testing.T) (pullverify.Entity, pullverify.TargetFor) {
				store, e := unfinishedLookupArtifact(t, "")
				return e, targetForStore(store)
			},
		},
	}
	for _, pass := range deprecationDiscoveries {
		for _, tc := range cases {
			t.Run(pass.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()
				e, targetFor := tc.target(t)
				var diag bytes.Buffer
				if err := pass.run(context.Background(), workspaceconfig.Config{}, e, targetFor, &diag); err != nil {
					t.Fatalf("discovery: %v (defect: %s)", err, tc.defect)
				}
				out := diag.String()
				if tc.want == "" {
					if out != "" {
						t.Fatalf("diagnostics %q; want none (defect: %s)", out, tc.defect)
					}
					return
				}
				for _, notice := range []string{unfinishedLookupNotice, unhonoredMarkerNotice} {
					if got, want := strings.Contains(out, notice), notice == tc.want; got != want {
						t.Errorf("diagnostics carry %q = %v; want %v — diagnostics %q (defect: %s)", notice, got, want, out, tc.defect)
					}
				}
				if !strings.HasPrefix(out, "  deprecation: "+unfinishedLookupURI+": ") {
					t.Errorf("diagnostics %q do not name the artifact first (defect: %s)", out, tc.defect)
				}
				if tc.cause != "" && !strings.Contains(out, tc.cause) {
					t.Errorf("diagnostics %q do not carry the cause %q (defect: %s)", out, tc.cause, tc.defect)
				}
			})
		}
	}
}

// TestAnUnfinishedDeprecationLookupHaltsWhereAnUnhonoredMarkerHalts proves the
// halt is unchanged for a lookup that did not complete: under a registry that
// requires a signature for the kind and is not advisory, both passes halt on a
// failed marker listing with the same words as before, keeping the listing's
// cause and the failed-listing condition reachable, and print no notice.
func TestAnUnfinishedDeprecationLookupHaltsWhereAnUnhonoredMarkerHalts(t *testing.T) {
	t.Parallel()
	wsCfg := workspaceconfig.Config{
		Registries: []workspaceconfig.RegistryConfig{{
			Host:    unfinishedLookupHost,
			Signing: workspaceconfig.SigningConfig{Required: true},
		}},
	}
	for _, pass := range deprecationDiscoveries {
		t.Run(pass.name, func(t *testing.T) {
			t.Parallel()
			store, e := unfinishedLookupArtifact(t, "")
			var diag bytes.Buffer
			err := pass.run(context.Background(), wsCfg, e, targetForStore(refusesMarkerListing{Store: store}), &diag)
			if err == nil {
				t.Fatalf("no halt over a failed marker listing under a required signature; diagnostics %q", diag.String())
			}
			wantPrefix := "verifying the deprecation marker published for " + unfinishedLookupURI + ": list deprecation referrers: "
			if !strings.HasPrefix(err.Error(), wantPrefix) {
				t.Errorf("halt %q; want it to open %q", err, wantPrefix)
			}
			if !errors.Is(err, errMarkerListingRefused) || !errors.Is(err, oci.ErrReferrerDiscoveryUnavailable()) {
				t.Errorf("halt %v does not carry the listing's cause and the failed-listing condition", err)
			}
			if diag.Len() != 0 {
				t.Errorf("a halt also printed %q", diag.String())
			}
		})
	}
}

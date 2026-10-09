// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package publishops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2/content/memory"
	"oras.land/oras-go/v2/registry/remote/errcode"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing/signingtest"
)

// failsFirstUpload is an in-memory store that answers the first upload of a
// blob of one media type the way the registry answered the upload this retry
// exists for: a 500, before anything is stored. Every later write goes through.
type failsFirstUpload struct {
	*memory.Store
	mediaType string
	calls     int
}

func (f *failsFirstUpload) Push(ctx context.Context, desc ocispec.Descriptor, r io.Reader) error {
	if desc.MediaType == f.mediaType {
		f.calls++
		if f.calls == 1 {
			return &errcode.ErrorResponse{
				Method:     "PUT",
				StatusCode: 500,
				Errors:     errcode.Errors{{Code: "INTERNAL_ERROR", Message: "internal error"}},
			}
		}
	}
	return f.Store.Push(ctx, desc, r)
}

// retriedPluginEntity is a Plugin entity whose authored claims agree with the
// frp-step-go fixture's own report, so its publication reaches the registry.
const retriedPluginEntity = `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: patch
  pluginType: pipeline-step
  version: "1.0.0"
spec:
  abiVersion: v1alpha1
  pipelineStep:
    capabilities:
      - helm
  exports:
    - name: step
      operates: patch
      scope: own
`

// writeRetriedPlugin writes the Plugin entity alone in a directory of its own
// and returns its path with the fixture .wasm's path. It skips when the Wasm
// toolchain the fixture needs is absent.
func writeRetriedPlugin(t *testing.T) (entityPath, pluginPath string) {
	t.Helper()
	pluginPath = wasmtest.EnsureFixtureBuilt(t, "frp-step-go.wasm")
	entityPath = filepath.Join(t.TempDir(), "1.0.0", "plugin.yaml")
	if err := os.MkdirAll(filepath.Dir(entityPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(entityPath, []byte(retriedPluginEntity), 0o644); err != nil {
		t.Fatalf("write entity: %v", err)
	}
	return entityPath, pluginPath
}

// TestAPublicationSurvivesOneTransientFailureOfEachAttachment replays the
// failure a full catalog publish met — one 500 on the upload of a publication's
// date — through each publishing verb, and the same for a Plugin's signature.
//
// The publication must succeed as if the registry had not failed: no error, the
// attachment announced, exactly one referrer of the faulted kind, and the date
// the one the publication read from its clock. A publication that did not retry
// the write fails here with the sentence the original run failed with, naming
// the attachment that could not be made.
func TestAPublicationSurvivesOneTransientFailureOfEachAttachment(t *testing.T) {
	// Not parallel: mutates the process-global retry.BaseDelay seam.
	shrinkRetryDelay(t)
	stated := time.Date(2026, 10, 4, 7, 29, 28, 0, time.UTC)
	yes, no := true, false

	rows := []struct {
		name      string
		faulted   string
		tag       string
		wantSign  bool
		wantDated bool
		publish   func(t *testing.T, target *failsFirstUpload, out *bytes.Buffer) error
	}{
		{
			name: "an entity's publication date", faulted: oci.MediaTypePublishedAt, tag: "1.0.0", wantDated: true,
			publish: func(t *testing.T, target *failsFirstUpload, out *bytes.Buffer) error {
				_, err := Push(context.Background(), PushConfig{
					EntityPath: writeDatedComponent(t), Registry: datedRegistry, Sign: &no,
					Workspace: resolveIn(t, "workspace: {}\n", nil), PushTarget: target,
					Now: func() time.Time { return stated }, Out: out,
				})
				return err
			},
		},
		{
			name: "an entity's signature", faulted: signing.BundleMediaType, tag: "1.0.0", wantSign: true, wantDated: true,
			publish: func(t *testing.T, target *failsFirstUpload, out *bytes.Buffer) error {
				_, err := Push(context.Background(), PushConfig{
					EntityPath: writeDatedComponent(t), Registry: datedRegistry, Sign: &yes,
					Signer:    &signingtest.FakeSigner{},
					Workspace: resolveIn(t, "workspace: {}\n", nil), PushTarget: target,
					Now: func() time.Time { return stated }, Out: out,
				})
				return err
			},
		},
		{
			name: "a Plugin's signature", faulted: signing.BundleMediaType, tag: "1.0.0", wantSign: true, wantDated: true,
			publish: func(t *testing.T, target *failsFirstUpload, out *bytes.Buffer) error {
				entityPath, pluginPath := writeRetriedPlugin(t)
				_, err := Push(context.Background(), PushConfig{
					EntityPath: entityPath, PluginPath: pluginPath, Registry: datedRegistry, Sign: &yes,
					Signer:    &signingtest.FakeSigner{},
					Workspace: resolveIn(t, "workspace: {}\n", nil), PushTarget: target,
					Now: func() time.Time { return stated }, Out: out,
				})
				return err
			},
		},
		{
			name: "a Plugin's publication date", faulted: oci.MediaTypePublishedAt, tag: "1.0.0", wantSign: true, wantDated: true,
			publish: func(t *testing.T, target *failsFirstUpload, out *bytes.Buffer) error {
				entityPath, pluginPath := writeRetriedPlugin(t)
				_, err := Push(context.Background(), PushConfig{
					EntityPath: entityPath, PluginPath: pluginPath, Registry: datedRegistry, Sign: &yes,
					Signer:    &signingtest.FakeSigner{},
					Workspace: resolveIn(t, "workspace: {}\n", nil), PushTarget: target,
					Now: func() time.Time { return stated }, Out: out,
				})
				return err
			},
		},
		{
			name: "a data artifact's publication date", faulted: oci.MediaTypePublishedAt, tag: "2.0.0", wantDated: true,
			publish: func(t *testing.T, target *failsFirstUpload, out *bytes.Buffer) error {
				file := filepath.Join(t.TempDir(), "bundle.bin")
				if err := os.WriteFile(file, []byte("artifact bytes"), 0o644); err != nil {
					t.Fatalf("write artifact: %v", err)
				}
				_, err := PushArtifact(context.Background(), PushArtifactConfig{
					FilePath: file, Name: "demo", Tag: "2.0.0", Registry: datedRegistry,
					Workspace: resolveIn(t, "workspace: {}\n", nil), PushTarget: target,
					Now: func() time.Time { return stated }, Out: out,
				})
				return err
			},
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ctx := context.Background()
			store := memory.New()
			target := &failsFirstUpload{Store: store, mediaType: row.faulted}
			var out bytes.Buffer
			if err := row.publish(t, target, &out); err != nil {
				t.Fatalf("the publication failed on a 500 one retry clears: %v\n%s", err, out.String())
			}
			if target.calls != 2 {
				t.Errorf("the faulted upload was made %d times; want 2 (the failed attempt and one retry)", target.calls)
			}
			if row.wantSign {
				if !strings.Contains(out.String(), "Signature attached: ") {
					t.Errorf("the account does not announce the signature\n%s", out.String())
				}
				refs, err := oci.ListReferrers(ctx, store, row.tag, signing.BundleMediaType)
				if err != nil {
					t.Fatalf("ListReferrers: %v", err)
				}
				if len(refs) != 1 {
					t.Errorf("signature referrers = %d; want 1", len(refs))
				}
			}
			if row.wantDated {
				if !strings.Contains(out.String(), "Publication date attached: ") {
					t.Errorf("the account does not announce the date\n%s", out.String())
				}
				refs, err := oci.ListReferrers(ctx, store, row.tag, oci.MediaTypePublishedAt)
				if err != nil {
					t.Fatalf("ListReferrers: %v", err)
				}
				if len(refs) != 1 {
					t.Errorf("published-at referrers = %d; want 1", len(refs))
				}
				at, found, err := oci.ReadPublishedAt(ctx, store, row.tag)
				if err != nil || !found {
					t.Fatalf("ReadPublishedAt: found=%v err=%v", found, err)
				}
				if !at.Equal(stated) {
					t.Errorf("stated date %v; want %v", at, stated)
				}
			}
		})
	}
}

// batchSignFixture publishes three distinct artifacts into one store and
// returns the references a batch names, the subject each resolves against, and
// the digest each was published at.
func batchSignFixture(t *testing.T, store *memory.Store) (refs []string, subjects map[string]string, digests map[string]string) {
	t.Helper()
	subjects = map[string]string{}
	digests = map[string]string{}
	for i, tag := range []string{"1.0.0", "2.0.0", "3.0.0"} {
		dg, err := oci.Push(context.Background(), store, tag, oci.MediaTypeComponent, []byte(fmt.Sprintf("published content %d", i)), nil)
		if err != nil {
			t.Fatalf("publish subject %s: %v", tag, err)
		}
		ref := fmt.Sprintf("%s/component/core/thing-%d:%s", datedRegistry, i, tag)
		refs = append(refs, ref)
		subjects[ref] = tag
		digests[ref] = dg
	}
	return refs, subjects, digests
}

// TestABatchSignRetriesATransientSigningFailureAndNotATerminalOne proves the
// batch signs through the one signing retry: a reference whose first signing
// attempt meets the authority's transient token-processing failure is signed on
// the retry along with every other reference, and a reference whose signing
// fails terminally is tried once and reported, as the batch always has.
func TestABatchSignRetriesATransientSigningFailureAndNotATerminalOne(t *testing.T) {
	// Not parallel: mutates the process-global retry.BaseDelay seam.
	shrinkRetryDelay(t)
	transient := errors.New(`signing: keyless bundle: Fulcio returned 400: {"code":3,"message":"There was an error processing the identity token"}`)
	terminal := errors.New("permanent signing failure")

	t.Run("a transient failure on one reference", func(t *testing.T) {
		ctx := context.Background()
		store := memory.New()
		refs, subjects, digests := batchSignFixture(t, store)
		flaky := digests[refs[1]]
		attempts := map[string]int{}
		fake := &signingtest.FakeSigner{FailOn: func(digest string) error {
			attempts[digest]++
			if digest == flaky && attempts[digest] == 1 {
				return transient
			}
			return nil
		}}
		var out bytes.Buffer
		res, err := Sign(ctx, SignConfig{
			Workspace: resolveIn(t, "workspace: {}\n", nil), References: refs, SubjectRefs: subjects,
			Target: store, Signer: fake, IDToken: "supplied-token",
			EnvLookup: func(string) string { return "" }, Out: &out,
		})
		if err != nil {
			t.Fatalf("Sign: %v\n%s", err, out.String())
		}
		if len(res.Signed) != len(refs) {
			t.Errorf("signed %d references; want every one of %d", len(res.Signed), len(refs))
		}
		if attempts[flaky] != 2 {
			t.Errorf("the flaky reference was signed %d times; want 2 (the failed attempt and one retry)", attempts[flaky])
		}
		for _, ref := range refs {
			got, lerr := oci.ListReferrers(ctx, store, subjects[ref], signing.BundleMediaType)
			if lerr != nil {
				t.Fatalf("ListReferrers %s: %v", ref, lerr)
			}
			if len(got) != 1 {
				t.Errorf("%s carries %d signatures; want 1", ref, len(got))
			}
		}
	})

	t.Run("a terminal failure on one reference", func(t *testing.T) {
		ctx := context.Background()
		store := memory.New()
		refs, subjects, digests := batchSignFixture(t, store)
		bad := digests[refs[1]]
		attempts := map[string]int{}
		fake := &signingtest.FakeSigner{FailOn: func(digest string) error {
			attempts[digest]++
			if digest == bad {
				return terminal
			}
			return nil
		}}
		var out bytes.Buffer
		_, err := Sign(ctx, SignConfig{
			Workspace: resolveIn(t, "workspace: {}\n", nil), References: refs, SubjectRefs: subjects,
			Target: store, Signer: fake, IDToken: "supplied-token",
			EnvLookup: func(string) string { return "" }, Out: &out,
		})
		if err == nil {
			t.Fatal("Sign succeeded with a reference whose signing fails terminally")
		}
		if !strings.Contains(err.Error(), "1 of 3 references failed") || !strings.Contains(err.Error(), refs[1]) ||
			!strings.Contains(err.Error(), terminal.Error()) {
			t.Errorf("the batch's report does not name the failed reference and its failure: %v", err)
		}
		if attempts[bad] != 1 {
			t.Errorf("the terminally failing reference was signed %d times; want 1 (a terminal failure is not retried)", attempts[bad])
		}
		for _, ref := range []string{refs[0], refs[2]} {
			got, lerr := oci.ListReferrers(ctx, store, subjects[ref], signing.BundleMediaType)
			if lerr != nil {
				t.Fatalf("ListReferrers %s: %v", ref, lerr)
			}
			if len(got) != 1 {
				t.Errorf("%s carries %d signatures; want 1 (the batch continues past a failed reference)", ref, len(got))
			}
		}
	})
}

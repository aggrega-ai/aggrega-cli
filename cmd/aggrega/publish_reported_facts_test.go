// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/publishops"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/signing/signingtest"
)

// Every expectation in this file is built from a SEPARATE call to the shared
// operation, against its own workspace and its own store, and never from the
// command's own rendering. That separation is the whole point: an expectation
// derived from the same projection under test agrees with it however wrong both
// are, which is exactly the blindness these assertions exist to remove.
//
// The LABELS are pinned as literals rather than built by calling the writer the
// operation calls. Sharing that helper between the surface and the assertion
// would move both together, so swapping two labels — the one mistake that tells a
// reader a byte count is a media type — would pass. The facts come from the
// operation; only the words are pinned here.

const reportedFactsComponent = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: core/api-server
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      type: object
`

const reportedFactsFeature = `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: core/ingress
  version: 1.0.0
  labels:
    component.aggrega.ai/type: gateway
spec:
  schema:
    values:
      type: object
      properties:
        host:
          type: string
`

// pushEntityFixture writes body into a fresh per-version directory, chdirs into
// its root, and publishes it with pushRun against a fresh store, returning the
// captured output. Each call gets its own root and store so no row can observe
// another's registry state.
func pushEntityFixture(t *testing.T, body string, dryRun bool) (out string, republished string) {
	t.Helper()
	root := t.TempDir()
	t.Chdir(root)
	entityPath := filepath.Join(root, "1.0.0", "entity.yaml")
	writeEntityFixture(t, entityPath, body)

	store := memory.New()
	var buf bytes.Buffer
	if err := pushRun(t.Context(), pushConfig{
		Workspace:  resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		EntityPath: entityPath,
		Registry:   "example.com",
		DryRun:     dryRun,
		PushTarget: store,
		Out:        &buf,
	}); err != nil {
		t.Fatalf("pushRun: %v", err)
	}
	if dryRun {
		return buf.String(), ""
	}
	var again bytes.Buffer
	if err := pushRun(t.Context(), pushConfig{
		Workspace:  resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		EntityPath: entityPath,
		Registry:   "example.com",
		PushTarget: store,
		Out:        &again,
	}); err != nil {
		t.Fatalf("pushRun (re-publish): %v", err)
	}
	return buf.String(), again.String()
}

// pushEntityResult runs the shared operation directly, in its own workspace and
// against its own store, purely to obtain the facts the assertion expects.
func pushEntityResult(t *testing.T, body string) publishops.PushResult {
	t.Helper()
	root := t.TempDir()
	entityPath := filepath.Join(root, "1.0.0", "entity.yaml")
	writeEntityFixture(t, entityPath, body)
	res, err := publishops.Push(t.Context(), publishops.PushConfig{
		Workspace:  resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		EntityPath: entityPath,
		Registry:   "example.com",
		PushTarget: memory.New(),
		Out:        &bytes.Buffer{},
	})
	if err != nil {
		t.Fatalf("publishops.Push (expectation source): %v", err)
	}
	return res
}

// TestPushReportsTheFactsItsResultCarries pins that a real publication names the
// kind, the media type and the byte count — the three facts a publication
// computes that the reference, tag and digest on its own line do not carry — on
// the publication path AND on the idempotent re-publication path, which lands
// nothing and must still say what is there.
//
// Two rows carrying different values in all three facts, so a renderer printing
// constants fails the second row rather than passing on the first.
func TestPushReportsTheFactsItsResultCarries(t *testing.T) {
	rows := []struct {
		name string
		body string
	}{
		{name: "Component", body: reportedFactsComponent},
		{name: "Feature", body: reportedFactsFeature},
	}
	// The rows must actually differ, or the table proves nothing about whether the
	// facts are read. Asserted rather than assumed.
	first := pushEntityResult(t, rows[0].body)
	second := pushEntityResult(t, rows[1].body)
	if first.Kind == second.Kind || first.MediaType == second.MediaType || first.Size == second.Size {
		t.Fatalf("table precondition: the two rows must differ in kind, media type and size; got %+v and %+v", first, second)
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			want := pushEntityResult(t, row.body)
			published, republished := pushEntityFixture(t, row.body, false)

			for _, stage := range []struct{ label, got string }{
				{"published", published},
				{"already published", republished},
			} {
				for _, line := range []string{
					fmt.Sprintf("  kind: %s\n", want.Kind),
					fmt.Sprintf("  mediatype: %s\n", want.MediaType),
					fmt.Sprintf("  size: %d bytes\n", want.Size),
				} {
					if !strings.Contains(stage.got, line) {
						t.Errorf("the %s account does not carry %q, which the publication computed; it read:\n%s",
							stage.label, line, stage.got)
					}
				}
				// A count reported as the other kind's would satisfy the label check
				// above only if the label moved too, so the byte count is also held
				// against the OTHER row's value being absent.
				other := first
				if want.Size == first.Size {
					other = second
				}
				if strings.Contains(stage.got, fmt.Sprintf("  size: %d bytes\n", other.Size)) {
					t.Errorf("the %s account also carries the other row's byte count %d; it read:\n%s",
						stage.label, other.Size, stage.got)
				}
			}
		})
	}
}

// TestPushDryRunAccountIsUnchangedByTheSharedWriter pins the control: routing the
// dry-run account through the same writer the publication uses leaves its bytes
// exactly as they were, so the three facts are stated identically whether a
// publication was simulated or performed.
func TestPushDryRunAccountIsUnchangedByTheSharedWriter(t *testing.T) {
	want := pushEntityResult(t, reportedFactsComponent)
	got, _ := pushEntityFixture(t, reportedFactsComponent, true)
	expected := fmt.Sprintf("aggrega push (dry-run): example.com/component/core/api-server:1.0.0\n  kind: %s\n  mediatype: %s\n  size: %d bytes\n",
		want.Kind, want.MediaType, want.Size)
	if got != expected {
		t.Errorf("dry-run account moved:\n got: %q\nwant: %q", got, expected)
	}
}

// TestPushArtifactReportsTheFactsItsResultCarries pins the media type and byte
// count on both real paths for a standalone data artifact, whose result carries
// no kind at all — so the account must name two facts and not a blank third.
//
// Two rows differing in both the media type and the byte count.
func TestPushArtifactReportsTheFactsItsResultCarries(t *testing.T) {
	rows := []struct {
		name      string
		content   string
		mediaType string
	}{
		{name: "documentation bundle default", content: "documentation-bundle-bytes", mediaType: ""},
		{name: "explicit media type", content: "a rather longer artifact body than the other row carries", mediaType: "application/vnd.example.thing+tar"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			root := t.TempDir()
			bundle := filepath.Join(root, "artifact.bin")
			if err := os.WriteFile(bundle, []byte(row.content), 0o644); err != nil {
				t.Fatalf("write artifact: %v", err)
			}

			// The expectation's own call, against its own store.
			want, err := publishops.PushArtifact(t.Context(), publishops.PushArtifactConfig{
				Workspace: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
				FilePath:  bundle, Name: "probe", Tag: "1.0.0",
				MediaType: row.mediaType, Registry: "example.com",
				PushTarget: memory.New(), Out: &bytes.Buffer{},
			})
			if err != nil {
				t.Fatalf("publishops.PushArtifact (expectation source): %v", err)
			}

			store := memory.New()
			var published, republished bytes.Buffer
			for _, sink := range []*bytes.Buffer{&published, &republished} {
				if err := pushArtifactRun(t.Context(), pushArtifactConfig{
					Workspace: resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
					FilePath:  bundle, Name: "probe", Tag: "1.0.0",
					MediaType: row.mediaType, Registry: "example.com",
					PushTarget: store, Out: sink,
				}); err != nil {
					t.Fatalf("pushArtifactRun: %v", err)
				}
			}

			for _, stage := range []struct{ label, got string }{
				{"published", published.String()},
				{"already published", republished.String()},
			} {
				for _, line := range []string{
					fmt.Sprintf("  mediatype: %s\n", want.MediaType),
					fmt.Sprintf("  size: %d bytes\n", want.Size),
				} {
					if !strings.Contains(stage.got, line) {
						t.Errorf("the %s account does not carry %q, which the publication computed; it read:\n%s",
							stage.label, line, stage.got)
					}
				}
				// A data artifact has no entity kind, so naming one would be a blank
				// fact rather than a fact.
				if strings.Contains(stage.got, "  kind:") {
					t.Errorf("the %s account names a kind, which this result does not carry; it read:\n%s",
						stage.label, stage.got)
				}
			}
		})
	}
}

// TestPushPluginReportsBothByteCounts pins the two-count branch: a Plugin lands
// its entity and its executable inside one manifest, so a single size would
// describe neither, and the two counts are named the way the simulated
// publication names them.
func TestPushPluginReportsBothByteCounts(t *testing.T) {
	wsRoot, entityPath, pluginPath, _ := writeAgreeingPlugin(t)

	// The expectation's own call, against its own store.
	want, err := publishops.Push(t.Context(), publishops.PushConfig{
		Workspace:  resolveTempWorkspace(t, tempWorkspaceOptions{Root: wsRoot}),
		EntityPath: entityPath, PluginPath: pluginPath,
		Registry: "hub.aggrega.ai", PushTarget: memory.New(), Out: &bytes.Buffer{},
	})
	if err != nil {
		t.Fatalf("publishops.Push (expectation source): %v", err)
	}
	if want.Size == 0 || want.PluginSize == 0 || want.Size == want.PluginSize {
		t.Fatalf("precondition: a Plugin publication must report two distinct non-zero counts; got entity=%d wasm=%d",
			want.Size, want.PluginSize)
	}

	store := memory.New()
	var published, republished bytes.Buffer
	for _, sink := range []*bytes.Buffer{&published, &republished} {
		if err := pushRun(t.Context(), pushConfig{
			Workspace:  resolveTempWorkspace(t, tempWorkspaceOptions{Root: wsRoot}),
			EntityPath: entityPath, PluginPath: pluginPath,
			Registry: "hub.aggrega.ai", PushTarget: store, Out: sink,
		}); err != nil {
			t.Fatalf("pushRun: %v", err)
		}
	}

	for _, stage := range []struct{ label, got string }{
		{"published", published.String()},
		{"already published", republished.String()},
	} {
		for _, line := range []string{
			fmt.Sprintf("  kind: %s\n", want.Kind),
			fmt.Sprintf("  entity mediatype: %s\n", want.MediaType),
			fmt.Sprintf("  entity size: %d bytes\n", want.Size),
			fmt.Sprintf("  wasm size: %d bytes\n", want.PluginSize),
		} {
			if !strings.Contains(stage.got, line) {
				t.Errorf("the %s account does not carry %q, which the publication computed; it read:\n%s",
					stage.label, line, stage.got)
			}
		}
		// One count standing in for both is the failure this branch exists to
		// prevent, so the undifferentiated label must be absent.
		if strings.Contains(stage.got, "  size:") {
			t.Errorf("the %s account reports an undifferentiated size for a two-part manifest; it read:\n%s",
				stage.label, stage.got)
		}
	}
}

// TestDeprecateReportsTheStampItAttached pins the timestamp the attachment read
// off the clock. It is the one fact of the marker the caller did not supply and
// cannot recompute, and where two markers sit on one artifact it is what decides
// which of them is authoritative.
//
// Two rows with different clocks and different severities, so a line printing a
// constant stamp fails the second.
func TestDeprecateReportsTheStampItAttached(t *testing.T) {
	rows := []struct {
		name     string
		severity string
		at       time.Time
	}{
		{name: "warning", severity: "warning", at: time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC)},
		{name: "retracted", severity: "retracted", at: time.Date(2031, 11, 12, 13, 14, 15, 0, time.UTC)},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ctx := context.Background()

			attach := func(sink *bytes.Buffer) publishops.DeprecateResult {
				store := memory.New()
				if _, err := oci.Push(ctx, store, "1.0.0", oci.MediaTypeComponent, []byte("subject"), nil); err != nil {
					t.Fatalf("Push subject: %v", err)
				}
				res, err := publishops.Deprecate(t.Context(), publishops.DeprecateConfig{
					Workspace:  resolveTempWorkspace(t, tempWorkspaceOptions{}),
					Reference:  "reg.test/component/core/legacy:1.0.0",
					Severity:   row.severity,
					Reason:     "superseded",
					Target:     store,
					SubjectRef: "1.0.0",
					Signer:     keyedTestSigner(t),
					Now:        func() time.Time { return row.at },
					Out:        sink,
					IsTTY:      func() bool { return false },
				})
				if err != nil {
					t.Fatalf("publishops.Deprecate: %v", err)
				}
				return res
			}

			// The expectation's own call, whose output is thrown away.
			want := attach(&bytes.Buffer{})
			if len(want.Marked) != 1 {
				t.Fatalf("precondition: one marker expected, got %d", len(want.Marked))
			}
			if want.Marked[0].IssuedOn == "" {
				t.Fatal("precondition: the attachment must stamp a moment for this assertion to mean anything")
			}

			// The command's own run, whose output is what is asserted.
			var got bytes.Buffer
			if err := deprecateRun(t.Context(), deprecateConfig{
				Workspace:  resolveTempWorkspace(t, tempWorkspaceOptions{}),
				Reference:  "reg.test/component/core/legacy:1.0.0",
				Severity:   row.severity,
				Reason:     "superseded",
				Target:     freshDeprecateSubject(t),
				SubjectRef: "1.0.0",
				Signer:     keyedTestSigner(t),
				Now:        func() time.Time { return row.at },
				Out:        &got,
				IsTTY:      func() bool { return false },
			}); err != nil {
				t.Fatalf("deprecateRun: %v", err)
			}

			line := fmt.Sprintf("  issued: %s\n", want.Marked[0].IssuedOn)
			if !strings.Contains(got.String(), line) {
				t.Errorf("the account does not carry %q, which the attachment stamped; it read:\n%s", line, got.String())
			}
			for _, otherRow := range rows {
				if otherRow.name == row.name {
					continue
				}
				stamp := otherRow.at.UTC().Format(time.RFC3339)
				if strings.Contains(got.String(), stamp) {
					t.Errorf("the account also carries the other row's stamp %s; it read:\n%s", stamp, got.String())
				}
			}
		})
	}
}

// freshDeprecateSubject publishes a subject artifact into its own store, so each
// attachment in a row runs against registry state no other call has touched.
func freshDeprecateSubject(t *testing.T) *memory.Store {
	t.Helper()
	store := memory.New()
	if _, err := oci.Push(context.Background(), store, "1.0.0", oci.MediaTypeComponent, []byte("subject"), nil); err != nil {
		t.Fatalf("Push subject: %v", err)
	}
	return store
}

// TestSignReportsEveryReferenceItSigned pins the ground the signing command's
// discard rests on: there is a status line for every reference the batch recorded
// as signed, carrying the same digest recorded for it. If that stopped holding,
// the command would be dropping facts rather than declining to repeat them.
//
// Two rows, one reference and two, because a batch is where a per-reference
// account can silently collapse to a single line.
func TestSignReportsEveryReferenceItSigned(t *testing.T) {
	rows := []struct {
		name string
		tags []string
	}{
		{name: "one reference", tags: []string{"1.0.0"}},
		{name: "two references", tags: []string{"1.0.0", "2.0.0"}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			ctx := context.Background()

			build := func() (*memory.Store, []string, map[string]string) {
				store := memory.New()
				var refs []string
				subjects := map[string]string{}
				for i, tag := range row.tags {
					body := fmt.Sprintf("published content %d", i)
					if _, err := oci.Push(ctx, store, tag, oci.MediaTypeComponent, []byte(body), nil); err != nil {
						t.Fatalf("Push subject %s: %v", tag, err)
					}
					ref := fmt.Sprintf("hub.aggrega.ai/component/core/thing-%d:%s", i, tag)
					refs = append(refs, ref)
					subjects[ref] = tag
				}
				return store, refs, subjects
			}

			// The expectation's own call, against its own store.
			wantStore, wantRefs, wantSubjects := build()
			want, err := publishops.Sign(t.Context(), publishops.SignConfig{
				Workspace:   resolveTempWorkspace(t, tempWorkspaceOptions{}),
				References:  wantRefs,
				SubjectRefs: wantSubjects,
				Target:      wantStore,
				Signer:      &signingtest.FakeSigner{},
				IDToken:     "supplied-token",
				EnvLookup:   func(string) string { return "" },
				Out:         &bytes.Buffer{},
			})
			if err != nil {
				t.Fatalf("publishops.Sign (expectation source): %v", err)
			}
			if len(want.Signed) != len(row.tags) {
				t.Fatalf("precondition: %d signed artifacts expected, got %d", len(row.tags), len(want.Signed))
			}

			// The command's own run, whose output is what is asserted.
			gotStore, gotRefs, gotSubjects := build()
			var got bytes.Buffer
			if err := signRun(t.Context(), signConfig{
				Workspace:   resolveTempWorkspace(t, tempWorkspaceOptions{}),
				References:  gotRefs,
				SubjectRefs: gotSubjects,
				Target:      gotStore,
				Signer:      &signingtest.FakeSigner{},
				IDToken:     "supplied-token",
				EnvLookup:   func(string) string { return "" },
				Out:         &got,
			}); err != nil {
				t.Fatalf("signRun: %v", err)
			}

			for _, artifact := range want.Signed {
				line := fmt.Sprintf("Signature attached: %s (%s)\n", artifact.Reference, artifact.Digest)
				if !strings.Contains(got.String(), line) {
					t.Errorf("the account does not carry %q, which the batch recorded as signed; it read:\n%s",
						line, got.String())
				}
			}
			if n := strings.Count(got.String(), "Signature attached:"); n != len(want.Signed) {
				t.Errorf("the account carries %d signature lines for %d recorded artifacts; it read:\n%s",
					n, len(want.Signed), got.String())
			}
		})
	}
}

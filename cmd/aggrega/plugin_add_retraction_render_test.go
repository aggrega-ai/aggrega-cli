// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/pluginops"
	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/deprecation"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The substitution this command puts in front of a reader, spelled out rather than
// assembled the way the command assembles it. An expectation built from the
// renderer's own pieces agrees with the renderer whatever it does; these are the
// bytes a person reads.
const (
	screenedIndexNotice = "  retraction: " + screenedIndexHost + "/" + screenedIndexRepo +
		":2.0.0 is retracted and was not selected: withdrawn in favour of a successor\n" +
		"  retraction: " + screenedIndexHost + "/" + screenedIndexRepo +
		" resolved to 1.0.0 instead (recorded in the local registry index as of " +
		screenedIndexBuiltAt + "; run `aggrega registry update` to refresh it)\n"

	screenedLiveNotice = "  retraction: " + screenedIndexHost + "/" + screenedIndexRepo +
		":2.0.0 is retracted and was not selected: withdrawn in favour of a successor\n" +
		"  retraction: " + screenedIndexHost + "/" + screenedIndexRepo +
		" resolved to 1.0.0 instead (read from the registry just now)\n"

	screenedWithdrawnReason = "withdrawn in favour of a successor"
)

// addedGovernanceLine is the sentence the command closes a governance attachment
// with, for the workspace file the attachment landed in.
func addedGovernanceLine(rw *workspaceconfig.ResolvedWorkspace, version string) string {
	return "Added governance/tag-injector:" + version + " as a governance plugin in " +
		filepath.Join(rw.Root(), workspaceconfig.ConfigFilename) + " (this project only)\n"
}

// screenedIndexWorkspace prepares a workspace whose local index carries the plugin
// repository at two published versions, the newer withdrawn, and fences the live
// tag lister so a pass cannot come from a fallback.
func screenedIndexWorkspace(t *testing.T, withdrawn map[string]string) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	withSearchTarget(t, fakeRepoLister{repos: []string{screenedIndexRepo}}, nil)
	rw := resolvedTestWorkspace(t)
	writeScreenedPluginIndex(t, rw, []string{"1.0.0", "2.0.0"}, withdrawn)
	prev := pluginops.RepoTagLister
	pluginops.RepoTagLister = func(context.Context, workspaceconfig.Config, string, string) ([]string, error) {
		t.Fatalf("live tag lister called; the local index must answer")
		return nil, nil
	}
	t.Cleanup(func() { pluginops.RepoTagLister = prev })
	return rw
}

// TestPluginAddRunStatesTheSubstitutionFromTheResult is the invocation-level proof
// that this command states a withdrawal substitution, and that it states it from
// the conclusion on the result rather than from prose the operation wrote to a
// stream handed to it.
//
// The discriminating arm is the first one, and it is what makes the rest evidence.
// The same options the command builds are handed to the operation directly, and the
// operation writes nothing about the substitution — so the line the command
// produces over the identical fixture exists only because the command rendered the
// value. An assertion that the output "contains a retraction line" would pass
// identically on the prose this replaces; only the difference between those two
// runs separates the two.
func TestPluginAddRunStatesTheSubstitutionFromTheResult(t *testing.T) {
	// Not parallel: these arms mutate process-global registry seams.
	t.Run("the operation states it nowhere, so the line below is the command's own", func(t *testing.T) {
		rw := screenedIndexWorkspace(t, map[string]string{"2.0.0": screenedWithdrawnReason})

		var notices bytes.Buffer
		res, err := pluginops.Add(context.Background(), rw, pluginops.AddOptions{
			Socket:            "governance",
			Role:              "label-transform",
			Query:             "tag-injector",
			Notices:           &notices,
			RetractionNotices: io.Discard,
		})
		if err != nil {
			t.Fatalf("Add as this command calls it: %v", err)
		}
		if strings.Contains(notices.String(), "retraction:") {
			t.Fatalf("the operation announced the substitution itself, so the arms below cannot attribute it to the command; it wrote:\n%s",
				notices.String())
		}
		if res.Retraction == nil {
			t.Fatal("the operation concluded no substitution, so there is nothing for the command to have rendered")
		}
	})

	t.Run("a writing delivery states it above the sentence that reports the write", func(t *testing.T) {
		rw := screenedIndexWorkspace(t, map[string]string{"2.0.0": screenedWithdrawnReason})

		var out, errOut bytes.Buffer
		if err := pluginAddRun(context.Background(), pluginAddConfig{
			Socket: "governance", Role: "label-transform", Query: "tag-injector",
			ws: rw, Out: &out, ErrOut: &errOut,
		}); err != nil {
			t.Fatalf("pluginAddRun: %v", err)
		}
		// Whole-stream equality rather than containment: a line stated twice, a line
		// lost, or two lines swapped are all invisible to a containment check and all
		// visible here.
		want := screenedIndexNotice + addedGovernanceLine(rw, "1.0.0")
		if out.String() != want {
			t.Errorf("rendered:\n%s\nwant:\n%s", out.String(), want)
		}
		if errOut.Len() != 0 {
			t.Errorf("a writing delivery put %q on the other writer; its commentary belongs with its answer", errOut.String())
		}
	})

	t.Run("a reporting delivery leaves the document alone and states it beside", func(t *testing.T) {
		rw := screenedIndexWorkspace(t, map[string]string{"2.0.0": screenedWithdrawnReason})

		var out, errOut bytes.Buffer
		if err := pluginAddRun(context.Background(), pluginAddConfig{
			Socket: "governance", Role: "label-transform", Query: "tag-injector", DryRun: true,
			ws: rw, Out: &out, ErrOut: &errOut,
		}); err != nil {
			t.Fatalf("pluginAddRun: %v", err)
		}
		if errOut.String() != screenedIndexNotice {
			t.Errorf("the substitution rendered:\n%s\nwant:\n%s", errOut.String(), screenedIndexNotice)
		}
		// The reason the substitution goes to the other writer at all: what stands on
		// this one is one parseable document, which a sentence beside it would break.
		if strings.Contains(out.String(), "retraction:") {
			t.Errorf("the substitution landed in the document's own stream, which breaks its shape:\n%s", out.String())
		}
		if !strings.HasPrefix(out.String(), "documents:") {
			t.Errorf("the answer is no longer one document:\n%s", out.String())
		}
	})

	t.Run("nothing withdrawn states nothing", func(t *testing.T) {
		// The control. Every assertion above passes on a command that states a
		// substitution unconditionally; this is what fails such a command.
		rw := screenedIndexWorkspace(t, nil)

		var out, errOut bytes.Buffer
		if err := pluginAddRun(context.Background(), pluginAddConfig{
			Socket: "governance", Role: "label-transform", Query: "tag-injector",
			ws: rw, Out: &out, ErrOut: &errOut,
		}); err != nil {
			t.Fatalf("pluginAddRun: %v", err)
		}
		want := addedGovernanceLine(rw, "2.0.0")
		if out.String() != want {
			t.Errorf("rendered:\n%s\nwant:\n%s", out.String(), want)
		}
		if errOut.Len() != 0 {
			t.Errorf("an ordinary attachment stated %q beside its answer", errOut.String())
		}
	})
}

// TestPluginAddRunKeepsTheMarkerAccountAndStatesTheSubstitutionOnce is the live
// path's proof, where the withdrawal markers are read during the command and the
// read reports on the way past.
//
// Two facts are held together because either alone would pass while the pair was
// wrong. The marker account still reaches the reader, which is what a caller
// silencing the whole notices channel to take the substitution as values would have
// destroyed. And the substitution is stated exactly once and after that account,
// which is what a caller rendering it on top of prose already written would have
// broken.
func TestPluginAddRunKeepsTheMarkerAccountAndStatesTheSubstitutionOnce(t *testing.T) {
	// Not parallel: mutates process-global registry seams.
	withSearchTarget(t, fakeRepoLister{repos: []string{screenedIndexRepo}}, nil)
	rw := resolvedTestWorkspace(t)
	withTagLister(t, []string{"1.0.0", "2.0.0"})

	// The production read reports each marker it examined on the writer it is
	// handed. The fake behind withTagLister does not, so the account is planted here
	// — otherwise there would be nothing for the assertion to find and the arm would
	// pass over a channel that had been silenced.
	const account = "  deprecation: " + screenedIndexRepo + ":2.0.0: marker present and honored; recorded as retracted\n"
	prev := pluginops.RepoWithdrawalLister
	pluginops.RepoWithdrawalLister = func(_ context.Context, _ workspaceconfig.Config, _, _ string, _ []string, w io.Writer) ([]registryindex.VersionDeprecation, error) {
		if w != nil {
			_, _ = io.WriteString(w, account)
		}
		return screenedWithdrawals(map[string]string{"2.0.0": screenedWithdrawnReason}), nil
	}
	t.Cleanup(func() { pluginops.RepoWithdrawalLister = prev })

	var out, errOut bytes.Buffer
	if err := pluginAddRun(context.Background(), pluginAddConfig{
		Socket: "governance", Role: "label-transform", Query: "tag-injector",
		ws: rw, Out: &out, ErrOut: &errOut,
	}); err != nil {
		t.Fatalf("pluginAddRun: %v", err)
	}

	want := account + screenedLiveNotice + addedGovernanceLine(rw, "1.0.0")
	if out.String() != want {
		t.Errorf("rendered:\n%s\nwant:\n%s", out.String(), want)
	}
	// Stated separately from the equality above, because a duplicate is the failure
	// this split is most likely to introduce and a reader of a failing equality
	// should not have to spot it.
	if got := strings.Count(out.String(), "resolved to 1.0.0 instead"); got != 1 {
		t.Errorf("the substitution is stated %d times; want exactly 1:\n%s", got, out.String())
	}
}

// TestPluginAddRunStatesAnUnscreenedSelection pins the other condition, which is
// the one that would otherwise be silence: a failed withdrawal read leaves
// selection unscreened, which withdraws nothing at all and is therefore
// indistinguishable from a clean screening to anything that only reads exclusions.
//
// It names the repository by the reference selection ran over, which is the
// reference the conclusion carries and the one the two lines above also name — so
// one command addresses one repository one way whichever condition it met.
func TestPluginAddRunStatesAnUnscreenedSelection(t *testing.T) {
	// Not parallel: mutates process-global registry seams.
	withSearchTarget(t, fakeRepoLister{repos: []string{screenedIndexRepo}}, nil)
	rw := resolvedTestWorkspace(t)
	withTagLister(t, []string{"1.0.0", "2.0.0"})
	prev := pluginops.RepoWithdrawalLister
	pluginops.RepoWithdrawalLister = func(_ context.Context, _ workspaceconfig.Config, _, _ string, _ []string, _ io.Writer) ([]registryindex.VersionDeprecation, error) {
		return nil, errors.New("registry unreachable")
	}
	t.Cleanup(func() { pluginops.RepoWithdrawalLister = prev })

	var out, errOut bytes.Buffer
	if err := pluginAddRun(context.Background(), pluginAddConfig{
		Socket: "governance", Role: "label-transform", Query: "tag-injector",
		ws: rw, Out: &out, ErrOut: &errOut,
	}); err != nil {
		t.Fatalf("a failed withdrawal read degrades rather than halting; got: %v", err)
	}

	want := "  retraction: " + screenedIndexHost + "/" + screenedIndexRepo +
		": markers could not be read (registry unreachable); published versions were not screened for retraction\n" +
		addedGovernanceLine(rw, "2.0.0")
	if out.String() != want {
		t.Errorf("rendered:\n%s\nwant:\n%s", out.String(), want)
	}
}

// TestPluginAddRunSaysANamedPinIsRetractedOnItsResultLine holds the result line to
// the result's withdrawn value, beside the notice the version's resolution prints.
//
// Whole-stream equality on every arm, so the planted defects it catches are each a
// visible diff: the clause dropped from the result line on a named retracted pin
// (the first arm); the clause printed on an ordinary named pin, or on every line
// (the second arm); and the clause reaching a reporting delivery's document, which
// would stop that answer being one parseable document (the third arm). The first
// arm also pins that the resolution's own notice — the one carrying the recorded
// reason and the snapshot's age — still precedes the result line exactly once.
func TestPluginAddRunSaysANamedPinIsRetractedOnItsResultLine(t *testing.T) {
	// Not parallel: these arms mutate process-global registry seams.
	const pinnedNotice = "  retraction: " + screenedIndexHost + "/" + screenedIndexRepo +
		":2.0.0 is retracted (" + screenedWithdrawnReason + "); resolving it because the reference names it explicitly" +
		" (recorded in the local registry index as of " + screenedIndexBuiltAt + "; run `aggrega registry update` to refresh it)\n"
	const clause = "; the version named is retracted and was pinned because it was named"

	t.Run("a named retracted version", func(t *testing.T) {
		rw := screenedIndexWorkspace(t, map[string]string{"2.0.0": screenedWithdrawnReason})
		var out, errOut bytes.Buffer
		if err := pluginAddRun(context.Background(), pluginAddConfig{
			Socket: "governance", Role: "label-transform", Query: "tag-injector:2.0.0",
			ws: rw, Out: &out, ErrOut: &errOut,
		}); err != nil {
			t.Fatalf("pluginAddRun: %v", err)
		}
		added := strings.TrimSuffix(addedGovernanceLine(rw, "2.0.0"), "\n") + clause + "\n"
		if want := pinnedNotice + added; out.String() != want {
			t.Errorf("rendered:\n%s\nwant:\n%s", out.String(), want)
		}
		if errOut.Len() != 0 {
			t.Errorf("a writing delivery put %q on the other writer", errOut.String())
		}
	})

	t.Run("a named version nothing withdrew", func(t *testing.T) {
		rw := screenedIndexWorkspace(t, map[string]string{"2.0.0": screenedWithdrawnReason})
		var out, errOut bytes.Buffer
		if err := pluginAddRun(context.Background(), pluginAddConfig{
			Socket: "governance", Role: "label-transform", Query: "tag-injector:1.0.0",
			ws: rw, Out: &out, ErrOut: &errOut,
		}); err != nil {
			t.Fatalf("pluginAddRun: %v", err)
		}
		if want := addedGovernanceLine(rw, "1.0.0"); out.String() != want {
			t.Errorf("rendered:\n%s\nwant:\n%s", out.String(), want)
		}
	})

	t.Run("a reporting delivery's document carries no clause", func(t *testing.T) {
		rw := screenedIndexWorkspace(t, map[string]string{"2.0.0": screenedWithdrawnReason})
		var out, errOut bytes.Buffer
		if err := pluginAddRun(context.Background(), pluginAddConfig{
			Socket: "governance", Role: "label-transform", Query: "tag-injector:2.0.0", DryRun: true,
			ws: rw, Out: &out, ErrOut: &errOut,
		}); err != nil {
			t.Fatalf("pluginAddRun: %v", err)
		}
		if strings.Contains(out.String(), clause) || !strings.HasPrefix(out.String(), "documents:") {
			t.Errorf("the reporting delivery's answer is not the bare document:\n%s", out.String())
		}
		if errOut.String() != pinnedNotice {
			t.Errorf("beside the document:\n%s\nwant the resolution's notice alone:\n%s", errOut.String(), pinnedNotice)
		}
	})
}

// screenedWithdrawals builds the verified withdrawal records a marker read answers
// with, keyed by version. Each carries a signer, because only a signature-verified
// record screens a version.
func screenedWithdrawals(withdrawn map[string]string) []registryindex.VersionDeprecation {
	var deps []registryindex.VersionDeprecation
	for _, v := range oci.SortedSemverTags(keysOf(withdrawn)) {
		deps = append(deps, registryindex.VersionDeprecation{
			Version: v,
			Marker:  deprecation.Marker{Severity: deprecation.SeverityRetracted, Reason: withdrawn[v]},
			Signer:  &lockfile.VerifiedSignature{Identity: "publisher", Mode: "keyed"},
		})
	}
	return deps
}

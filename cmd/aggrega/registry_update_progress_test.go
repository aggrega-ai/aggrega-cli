// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// progressHosts is the two-registry configuration the progress tests walk, in
// the order the walk visits them.
const progressHostsConfig = "workspace: {}\ndefault_registry: first.example.com\n" +
	"registries:\n" +
	"  - host: first.example.com\n    name: first\n" +
	"  - host: second.example.com\n    name: second\n"

// progressSources serves first.example.com with two readable repositories and
// second.example.com with one, all in memory.
func progressSources(t *testing.T) func(workspaceconfig.Config, string) (registryIndexSource, error) {
	t.Helper()
	meta := oci.SearchableMetadata{Description: "d", Category: "compute", Kind: "Component"}
	first := fakeIndexSource{
		repos: []string{"component/core/alpha", "component/core/beta"},
		targets: map[string]oras.ReadOnlyTarget{
			"component/core/alpha": seedComponentRepo(t, []string{"1.0.0"}, meta),
			"component/core/beta":  seedComponentRepo(t, []string{"1.0.0"}, meta),
		},
	}
	second := fakeIndexSource{
		repos: []string{"component/core/gamma"},
		targets: map[string]oras.ReadOnlyTarget{
			"component/core/gamma": seedComponentRepo(t, []string{"1.0.0"}, meta),
		},
	}
	return func(_ workspaceconfig.Config, host string) (registryIndexSource, error) {
		switch host {
		case "first.example.com":
			return first, nil
		case "second.example.com":
			return second, nil
		}
		return nil, fmt.Errorf("no source for %s", host)
	}
}

// runProgressUpdate runs `registry update` over the two-registry fixture with the
// given display choices, and returns what reached standard output — with the
// run's own temporary root replaced by ROOT, so two runs compare — and what
// reached the progress stream.
func runProgressUpdate(t *testing.T, diag buildDiagOptions) (stdout, progress string) {
	t.Helper()
	withIndexTargetFunc(t, progressSources(t))
	root := t.TempDir()
	rw := resolveWorkspaceWithConfig(t, root, progressHostsConfig)
	var out, diagOut bytes.Buffer
	if err := registryUpdateRun(t.Context(), registryUpdateConfig{ws: rw, Out: &out, DiagOut: &diagOut, diag: diag, now: frozenClock()}); err != nil {
		t.Fatalf("registryUpdateRun: %v\nstdout:\n%s", err, out.String())
	}
	return strings.ReplaceAll(out.String(), root, "ROOT"), diagOut.String()
}

// maskElapsed replaces every elapsed time a progress row carries with T.Ts, the
// one byte range of the progress output that legitimately varies between runs.
func maskElapsed(s string) string {
	return regexp.MustCompile(`\d+\.\ds`).ReplaceAllString(s, "T.Ts")
}

// progressRowLayout writes a progress row's layout out literally rather than
// composing it with the production helpers, so a change to the layout fails here
// instead of being followed: the body, padding to column 30 — or one space where
// the body is longer — and the masked elapsed right-aligned in six columns, dim
// unless plain.
func progressRowLayout(body string, plain bool) string {
	pad := 30 - utf8.RuneCountInString(body)
	if pad < 1 {
		pad = 1
	}
	if plain {
		return body + strings.Repeat(" ", pad) + "  T.Ts\n"
	}
	return body + strings.Repeat(" ", pad) + "\x1b[2m  T.Ts\x1b[0m\x1b[K\n"
}

// TestRegistryUpdateProgress_TerminalDrawsTheChecklist drives the command on a
// terminal that redraws in place and holds what it draws: each registry's row
// spins with no count while its listing is read, then counts its repositories up
// in repository order, and the finished block ticks every registry with the count
// it ended on and the whole-walk bar at 100%.
func TestRegistryUpdateProgress_TerminalDrawsTheChecklist(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal, spinnerTickInterval
	// and registryops.IndexTarget seams other tests read.
	withTTY(t, true)
	orig := spinnerTickInterval
	spinnerTickInterval = time.Hour // keep the async ticker from redrawing mid-test
	t.Cleanup(func() { spinnerTickInterval = orig })

	_, progress := runProgressUpdate(t, buildDiagOptions{})

	if !strings.HasPrefix(progress, "\r[|] indexing first.example.com ") {
		t.Fatalf("the first draw must spin the first registry's row before its listing is known; got %q", progress)
	}
	if first := progress[:strings.IndexByte(progress, '\n')]; strings.Contains(first, "/2") {
		t.Errorf("the first registry's row must carry no count before its listing is read; got %q", first)
	}

	// Each count appears, and appears in ascending order.
	last := -1
	for _, step := range []string{
		"indexing first.example.com 1/2",
		"indexing first.example.com 2/2",
		"indexing second.example.com 1/1",
	} {
		at := strings.Index(progress, step)
		if at < 0 {
			t.Fatalf("the checklist never drew %q; got %q", step, progress)
		}
		if at < last {
			t.Errorf("%q was drawn before the count that precedes it in repository order; got %q", step, progress)
		}
		last = at
	}

	got := maskElapsed(progress)
	finalBlock := got[strings.LastIndex(got, "\x1b[3A"):]
	want := "\x1b[3A\r" +
		progressRowLayout("[✓] indexing first.example.com 2/2", false) +
		progressRowLayout("[✓] indexing second.example.com 1/1", false) +
		"Progress ██████████████████ 100%\x1b[K\n"
	if finalBlock != want {
		t.Errorf("finished checklist block differs from the drawn layout:\n got  %q\n want %q", finalBlock, want)
	}
}

// TestRegistryUpdateProgress_PlainWritesOneLinePerRegistry drives the command
// under --plain on a terminal: no escape byte, no redraw, and one line per
// registry, in the order the walk visits them, each naming the registry with the
// count its walk ended on and the time it took.
func TestRegistryUpdateProgress_PlainWritesOneLinePerRegistry(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal and
	// registryops.IndexTarget seams other tests read.
	withTTY(t, true)

	_, progress := runProgressUpdate(t, buildDiagOptions{plain: true, noColor: true})

	if i := strings.IndexByte(progress, 0x1b); i >= 0 {
		t.Errorf("plain progress carries an escape byte at offset %d; got %q", i, progress)
	}
	if strings.ContainsRune(progress, '\r') {
		t.Errorf("plain progress must never redraw (no carriage return); got %q", progress)
	}
	want := progressRowLayout("indexing first.example.com 2/2", true) +
		progressRowLayout("indexing second.example.com 1/1", true)
	if got := maskElapsed(progress); got != want {
		t.Errorf("plain progress differs:\n got  %q\n want %q", got, want)
	}
}

// TestRegistryUpdateProgress_NotATerminalShowsNothing drives the command with
// its progress stream redirected: nothing is written there. It also holds what
// the command prints on standard output to be the same bytes whichever form the
// progress took — drawn, plain, or none — so the progress changes none of the
// running commentary and none of the closing summary.
func TestRegistryUpdateProgress_NotATerminalShowsNothing(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal, spinnerTickInterval
	// and registryops.IndexTarget seams other tests read.
	orig := spinnerTickInterval
	spinnerTickInterval = time.Hour
	t.Cleanup(func() { spinnerTickInterval = orig })

	withTTY(t, false)
	silentOut, progress := runProgressUpdate(t, buildDiagOptions{})
	if progress != "" {
		t.Errorf("a progress stream that is not a terminal must receive nothing; got %q", progress)
	}
	for _, want := range []string{
		"Indexed 2 entities from first.example.com\n",
		"Indexed 1 entities from second.example.com\n",
		"Indexed 2 registries.\n",
		"  first.example.com: 2 entities, 0 reads retried\n",
		"  second.example.com: 1 entity, 0 reads retried\n",
		"Index written to ROOT",
	} {
		if !strings.Contains(silentOut, want) {
			t.Fatalf("standard output lacks %q, so the comparison below is not over the lines it means to hold:\n%s", want, silentOut)
		}
	}

	withTTY(t, true)
	drawnOut, _ := runProgressUpdate(t, buildDiagOptions{})
	plainOut, _ := runProgressUpdate(t, buildDiagOptions{plain: true, noColor: true})
	for name, out := range map[string]string{"drawn": drawnOut, "plain": plainOut} {
		if out != silentOut {
			t.Errorf("standard output under the %s progress differs from the run that showed none:\n%s:\n%s\nnone:\n%s", name, name, out, silentOut)
		}
		if i := strings.IndexByte(out, 0x1b); i >= 0 {
			t.Errorf("standard output under the %s progress carries an escape byte at offset %d", name, i)
		}
	}
}

// TestRegistryUpdateProgress_CommentaryLandsAboveTheChecklist drives the command
// with its standard output and its progress on one terminal, as a person running
// it sees them, and a repository the walk cannot read: the line naming it is
// written after the drawn block is erased, and the block is drawn again beneath
// it, so no later redraw climbs over the line.
func TestRegistryUpdateProgress_CommentaryLandsAboveTheChecklist(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal, spinnerTickInterval
	// and registryops.IndexTarget seams other tests read.
	withTTY(t, true)
	orig := spinnerTickInterval
	spinnerTickInterval = time.Hour
	t.Cleanup(func() { spinnerTickInterval = orig })

	meta := oci.SearchableMetadata{Description: "d", Category: "compute", Kind: "Component"}
	withIndexTarget(t, fakeIndexSource{
		// component/core/broken has no target, so its tag listing fails and the walk
		// names it as a repository it could not read.
		repos:   []string{"component/core/alpha", "component/core/broken"},
		targets: map[string]oras.ReadOnlyTarget{"component/core/alpha": seedComponentRepo(t, []string{"1.0.0"}, meta)},
	})
	rw := resolveWorkspaceWithConfig(t, t.TempDir(), updateTestConfig)

	var screen bytes.Buffer
	if err := registryUpdateRun(t.Context(), registryUpdateConfig{ws: rw, Out: &screen, DiagOut: &screen, now: frozenClock()}); err != nil {
		t.Fatalf("registryUpdateRun: %v\n%q", err, screen.String())
	}
	got := screen.String()

	const line = "  skipped component/core/broken: "
	at := strings.Index(got, line)
	if at < 0 {
		t.Fatalf("the walk never named the repository it could not read; got %q", got)
	}
	if before := got[:at]; !strings.HasSuffix(before, "\x1b[2A\r\x1b[J") {
		t.Errorf("the commentary line must follow the erased block (cursor up over its two rows, then clear below); got %q", before[max(0, len(before)-40):])
	}
	end := at + strings.IndexByte(got[at:], '\n') + 1
	redraw := regexp.MustCompile(`^\r\[[|/\-\\]\] indexing hub\.aggrega\.ai `)
	if after := got[end:]; !redraw.MatchString(after) {
		t.Errorf("the block must be drawn again beneath the commentary line, from the line after it; got %q", after[:min(len(after), 60)])
	}
}

// stoppingIndexSource is an in-memory index source whose first repository's read
// stops the walk, and whose every other read waits for that stop: a walk over it
// is withdrawn after its first repository, with the rest of what it lists never
// finished.
type stoppingIndexSource struct {
	fakeIndexSource
	ctx  context.Context
	stop context.CancelFunc
}

func (s stoppingIndexSource) Repository(repo string) (oras.ReadOnlyTarget, error) {
	if repo == s.repos[0] {
		s.stop()
		return s.fakeIndexSource.Repository(repo)
	}
	<-s.ctx.Done()
	return nil, s.ctx.Err()
}

// TestRegistryUpdateProgress_StopMidWalkEndsTheChecklistCleanly stops a walk part
// way and holds what the checklist does about it: its last block stands with the
// count the walk stopped at — fewer than it listed — under the pending marker
// rather than a tick, and a bar short of full; it ends on a complete line, and is
// never drawn again, however long the spinner's clock keeps running.
func TestRegistryUpdateProgress_StopMidWalkEndsTheChecklistCleanly(t *testing.T) {
	// Not parallel: mutates the process-global fileTerminal, spinnerTickInterval
	// and registryops.IndexTarget seams other tests read.
	withTTY(t, true)
	orig := spinnerTickInterval
	spinnerTickInterval = 5 * time.Millisecond // a live ticker would redraw within a few of these
	t.Cleanup(func() { spinnerTickInterval = orig })

	const listed = 40
	meta := oci.SearchableMetadata{Description: "d", Category: "compute", Kind: "Component"}
	repos := make([]string, listed)
	for i := range repos {
		repos[i] = fmt.Sprintf("component/core/r%02d", i)
	}
	ctx, stop := context.WithCancel(t.Context())
	t.Cleanup(stop)
	withIndexTarget(t, stoppingIndexSource{
		fakeIndexSource: fakeIndexSource{
			repos:   repos,
			targets: map[string]oras.ReadOnlyTarget{repos[0]: seedComponentRepo(t, []string{"1.0.0"}, meta)},
		},
		ctx:  ctx,
		stop: stop,
	})
	rw := resolveWorkspaceWithConfig(t, t.TempDir(), updateTestConfig)

	var out, diagOut bytes.Buffer
	err := registryUpdateRun(ctx, registryUpdateConfig{ws: rw, Out: &out, DiagOut: &diagOut, now: frozenClock()})
	if err == nil {
		t.Fatalf("a walk stopped part way must return the stop; got nil\nstdout:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Not indexed: hub.aggrega.ai") {
		t.Errorf("the closing summary must name the registry the stop left unindexed; got:\n%s", out.String())
	}

	drawn := diagOut.Len()
	time.Sleep(20 * spinnerTickInterval)
	if diagOut.Len() != drawn {
		t.Errorf("the checklist was drawn again after the walk ended: %d bytes became %d", drawn, diagOut.Len())
	}

	got := diagOut.String()
	finalBlock := got[strings.LastIndex(got, "\x1b[2A"):]
	if strings.Contains(finalBlock, "[✓]") {
		t.Errorf("the stopped walk's row must not be ticked, since its count says it did not finish; got %q", finalBlock)
	}
	m := regexp.MustCompile(`^\x1b\[2A\r\[ \] indexing hub\.aggrega\.ai (\d+)/40 +\x1b\[2m +\d+\.\ds\x1b\[0m\x1b\[K\nProgress [█░]{18} (\d+)%\x1b\[K\n$`).FindStringSubmatch(finalBlock)
	if m == nil {
		t.Fatalf("the last block is not a finished checklist, its stopped row pending, ending on a complete line; got %q", finalBlock)
	}
	done, _ := strconv.Atoi(m[1])
	pct, _ := strconv.Atoi(m[2])
	if done < 1 || done >= listed {
		t.Errorf("the stopped walk's row reads %d/%d; want at least the first repository and fewer than all %d", done, listed, listed)
	}
	if pct >= 100 {
		t.Errorf("the stopped walk's bar reads %d%%; a walk that did not finish must not show a full bar", pct)
	}
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"

	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/internal/registryops"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// owedFact is one fact the index build computed and the closing summary is
// therefore obliged to state, carrying the name of the field it came from so a
// failure says which fact went missing rather than only that a string was absent.
type owedFact struct {
	field string
	about string
	want  string
}

// factsOwedByRegistryUpdateSummary derives, from the index build's own result,
// every fact its closing summary must state.
//
// 🔴 The expectation is taken from the shared result and never from the summary's
// own rendering. A check that read the surface's projection to decide what the
// surface owes would agree with the surface whatever it dropped, which is exactly
// the blindness this pins: the whole defect being repaired here is a surface that
// was handed a result and stated none of it.
//
// A count is asserted as the number followed by the first letters of its noun,
// so the position of the number is pinned without this deriving the plural form
// the summary uses. Re-deriving that form would share a rule with the code under
// test, and a shared rule cannot disagree.
func factsOwedByRegistryUpdateSummary(res registryops.UpdateResult) []owedFact {
	indexed := 0
	for _, host := range res.Hosts {
		if host.Err == "" {
			indexed++
		}
	}

	var owed []owedFact
	if indexed == len(res.Hosts) {
		owed = append(owed, owedFact{
			field: "Hosts",
			about: "how many registries were indexed",
			want:  "Indexed " + strconv.Itoa(indexed) + " registr",
		})
	} else {
		owed = append(owed, owedFact{
			field: "Hosts",
			about: "how many of the selected registries were indexed",
			want:  fmt.Sprintf("Indexed %d of %d registr", indexed, len(res.Hosts)),
		})
		owed = append(owed, owedFact{
			field: "Hosts[].Err",
			about: "that some registries were not indexed",
			want:  "Not indexed:",
		})
	}
	if indexed > 0 {
		owed = append(owed, owedFact{
			field: "IndexRoot",
			about: "the directory the index was written to",
			want:  res.IndexRoot,
		})
	}
	for _, host := range res.Hosts {
		owed = append(owed, owedFact{
			field: "Hosts[].Host",
			about: "the registry " + host.Host,
			want:  host.Host,
		})
	}
	return owed
}

// registryUpdateSummaryRow is the summary's row for one registry, or the empty
// string when it states none.
func registryUpdateSummaryRow(summary, host string) string {
	for _, line := range strings.Split(summary, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), host+":") {
			return line
		}
	}
	return ""
}

// TestRegistryUpdateSummaryCarriesEveryFactTheWalkComputed proves the command
// line states the whole outcome of an index build, on a walk that reaches every
// registry and on one that does not.
//
// The facts asserted are derived from the build's own result, so the assertion
// cannot be satisfied by a summary that agrees with itself. What it pins is the
// property the running commentary alone does not have: the commentary is
// per-event and says each thing at the moment it happens, so how many
// repositories a registry could not read, which registries were not indexed, and
// where the index was written are stated nowhere in it.
func TestRegistryUpdateSummaryCarriesEveryFactTheWalkComputed(t *testing.T) {
	// Not parallel: mutates the process-global index-target seam other tests read.
	const reachedHost = "first.example.com"
	const secondHost = "second.example.com"
	const unreachableHost = "unreachable.example.com"

	meta := oci.SearchableMetadata{Description: "d", Category: "compute", Kind: "Component"}

	// A repository the enumeration reports and the source cannot serve is the
	// unreadable-repository case, and it is reached without a network: the walk
	// asks the in-memory source for its tags and the source has no target for it.
	firstSource := func(t *testing.T) fakeIndexSource {
		t.Helper()
		return fakeIndexSource{
			repos: []string{
				"component/core/api-server",
				"component/core/worker",
				"component/core/absent",
			},
			targets: map[string]oras.ReadOnlyTarget{
				"component/core/api-server": seedComponentRepo(t, []string{"1.0.0", "1.2.0"}, meta),
				"component/core/worker":     seedComponentRepo(t, []string{"2.0.0"}, meta),
			},
		}
	}
	secondSource := func(t *testing.T) fakeIndexSource {
		t.Helper()
		return fakeIndexSource{
			repos:   []string{"component/core/gateway"},
			targets: map[string]oras.ReadOnlyTarget{"component/core/gateway": seedComponentRepo(t, []string{"3.0.0"}, meta)},
		}
	}

	for _, tc := range []struct {
		name string
		// config is the workspace configuration, and its registry order is the
		// walk order.
		config string
		// resolve stands in for the index source of each host, so a host fails
		// without anything being asked of a network.
		resolve func(t *testing.T) func(workspaceconfig.Config, string) (registryIndexSource, error)
		// wantRows is the per-registry row content, keyed by host: the entity
		// count and, where the walk met one, the unreadable-repository count.
		wantRow map[string]bool
	}{
		{
			name: "every registry reached",
			config: "workspace: {}\ndefault_registry: " + reachedHost + "\nregistries:\n" +
				"  - host: " + reachedHost + "\n" +
				"  - host: " + secondHost + "\n",
			resolve: func(t *testing.T) func(workspaceconfig.Config, string) (registryIndexSource, error) {
				first, second := firstSource(t), secondSource(t)
				return func(_ workspaceconfig.Config, host string) (registryIndexSource, error) {
					if host == reachedHost {
						return first, nil
					}
					return second, nil
				}
			},
			wantRow: map[string]bool{reachedHost: true, secondHost: true},
		},
		{
			// The unreachable registry is walked first, so the reachable one is
			// only reached if the walk continues past a failure.
			name: "one registry not reached",
			config: "workspace: {}\ndefault_registry: " + secondHost + "\nregistries:\n" +
				"  - host: " + unreachableHost + "\n" +
				"  - host: " + secondHost + "\n",
			resolve: func(t *testing.T) func(workspaceconfig.Config, string) (registryIndexSource, error) {
				second := secondSource(t)
				return func(_ workspaceconfig.Config, host string) (registryIndexSource, error) {
					if host == secondHost {
						return second, nil
					}
					return nil, fmt.Errorf("no route to %s", host)
				}
			},
			wantRow: map[string]bool{secondHost: true},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withIndexTargetFunc(t, tc.resolve(t))
			rw := resolveWorkspaceWithConfig(t, t.TempDir(), tc.config)
			frozen := frozenClock()

			// One walk, read twice: the shared result is what the facts are
			// derived from, and the summary is what this surface made of it.
			shared, _ := registryops.Update(t.Context(), rw, registryops.UpdateOptions{Now: frozen}, io.Discard)
			var rendered bytes.Buffer
			reportRegistryUpdate(&rendered, shared)
			summary := rendered.String()
			t.Logf("closing summary:\n%s", summary)

			for _, owed := range factsOwedByRegistryUpdateSummary(shared) {
				if owed.want == "" {
					t.Fatalf("the walk computed no value for %s, so this assertion would pass on any output", owed.field)
				}
				if !strings.Contains(summary, owed.want) {
					t.Errorf("the closing summary does not state %s — the walk computed it as %s = %q and this surface must carry it.\nSummary:\n%s",
						owed.about, owed.field, owed.want, summary)
				}
			}

			for _, host := range shared.Hosts {
				row := registryUpdateSummaryRow(summary, host.Host)
				if host.Err != "" {
					if row != "" {
						t.Errorf("registry %s was not indexed and the summary gives it a row anyway: %q", host.Host, row)
					}
					continue
				}
				if !tc.wantRow[host.Host] {
					t.Fatalf("registry %s was indexed but this case expects no row for it, so the fixture and the walk disagree", host.Host)
				}
				if row == "" {
					t.Errorf("the closing summary states no row for the indexed registry %s.\nSummary:\n%s", host.Host, summary)
					continue
				}
				if want := strconv.Itoa(host.Indexed) + " entit"; !strings.Contains(row, want) {
					t.Errorf("the row for %s does not state the %d entities the walk indexed (looked for %q): %q",
						host.Host, host.Indexed, want, row)
				}
				if unread := len(host.Skipped); unread > 0 {
					if want := strconv.Itoa(unread) + " repositor"; !strings.Contains(row, want) {
						t.Errorf("the row for %s does not state the %d repositories the walk could not read (looked for %q): %q",
							host.Host, unread, want, row)
					}
				}
			}

			// The reporter is reached from the command's own entry point, so the
			// facts above are on the surface a person sees rather than only in a
			// function a test can call.
			withIndexTargetFunc(t, tc.resolve(t))
			commandRoot := t.TempDir()
			commandWorkspace := resolveWorkspaceWithConfig(t, commandRoot, tc.config)
			var commandOut bytes.Buffer
			walkErr := registryUpdateRun(t.Context(), registryUpdateConfig{ws: commandWorkspace, Out: &commandOut, now: frozen})
			t.Logf("aggrega registry update — full command output (error: %v):\n%s", walkErr, commandOut.String())
			for _, want := range []string{"Indexed ", "Index written to "} {
				if !strings.Contains(commandOut.String(), want) {
					t.Errorf("the command's own output carries no %q, so the summary is not reached from the command.\nOutput:\n%s",
						want, commandOut.String())
				}
			}
		})
	}
}

// TestRegistryUpdateSummaryStaysSilentWhenNothingIsConfigured proves the closing
// summary says nothing when no registry is configured. The walk itself states
// that, and there is no index directory to name — the build resolves one only
// once it has a registry to walk, so a summary here would name an empty location.
func TestRegistryUpdateSummaryStaysSilentWhenNothingIsConfigured(t *testing.T) {
	t.Parallel()

	var rendered bytes.Buffer
	reportRegistryUpdate(&rendered, registryops.UpdateResult{NoRegistriesConfigured: true})
	if rendered.Len() != 0 {
		t.Errorf("the closing summary wrote %q for a workspace configuring no registry; want nothing", rendered.String())
	}
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginops

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/registryops"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// fakeLister is an in-memory repository lister: it hands the enumeration
// primitive a fixed repository set in one page, so an addition runs without a
// network.
type fakeLister struct {
	repos []string
}

func (f fakeLister) Repositories(_ context.Context, _ string, fn func(repos []string) error) error {
	return fn(f.repos)
}

// resolveScratch resolves a workspace over a temporary directory carrying the
// supplied configuration text, so every test here runs against a real resolution
// rather than a hand-built one — the write-target policy reads the resolution's
// own root and its own view of the environment, and a fabricated resolution would
// exercise neither.
func resolveScratch(t *testing.T, config string) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	root := t.TempDir()
	if err := writeFileAtomic(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(config), 0o644); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	// The environment is frozen rather than read, which is the seam production
	// resolves through: it points the machine-wide layer at a path inside the
	// scratch tree, so a run that resolves the machine-wide target cannot reach the
	// developer's own file, and it keeps resolution deterministic under parallel
	// tests. Resolving against the live environment would make these assertions
	// depend on the machine they run on.
	globalPath := filepath.Join(root, "scratch-global.yaml")
	// The file has to exist: resolution refuses a named global config it cannot
	// stat rather than treating the pointer as unset, which is the right posture —
	// a caller that named a file and got the defaults would never learn it.
	if err := writeFileAtomic(globalPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("seed scratch global config: %v", err)
	}
	look := func(name string) string {
		if name == "AGGREGA_CONFIG" {
			return globalPath
		}
		return ""
	}
	rw, err := workspaceconfig.Resolve(root, "", look)
	if err != nil {
		t.Fatalf("resolve scratch workspace: %v", err)
	}
	if got := filepath.Join(rw.Root(), workspaceconfig.ConfigFilename); got != filepath.Join(root, workspaceconfig.ConfigFilename) {
		t.Fatalf("the scratch resolution anchored at %s rather than %s; the fixture is wrong, not the code", rw.Root(), root)
	}
	return rw
}

const scratchConfig = `workspace:
  catalog_dirs:
    - catalog
plugins:
  governance:
    - uri: governance/tag-injector:9.9.9
      role: label-transform
`

// TestANilWorkspaceIsRefusedRatherThanResolved pins the refusal every operation
// owes, because the alternative is worse than an error: resolving here from the
// current directory would make the answer depend on a directory no caller named,
// and the operation would then write to a configuration file nobody chose.
//
// Both entry points that take a workspace are driven, so neither can lose the
// refusal on its own.
func TestANilWorkspaceIsRefusedRatherThanResolved(t *testing.T) {
	if _, err := Add(context.Background(), nil, AddOptions{Socket: ProviderSocket, Query: "x", Capability: "c"}); err == nil {
		t.Error("Add accepted a nil workspace")
	} else if !strings.Contains(err.Error(), "no resolved workspace supplied") {
		t.Errorf("Add refused a nil workspace with the wrong reason: %v", err)
	}
	if _, err := Remove(nil, RemoveOptions{Socket: ProviderSocket, Query: "x"}); err == nil {
		t.Error("Remove accepted a nil workspace")
	} else if !strings.Contains(err.Error(), "no resolved workspace supplied") {
		t.Errorf("Remove refused a nil workspace with the wrong reason: %v", err)
	}
}

// TestAnUnknownTypeNamesTheAcceptedSet pins that a mistyped type is reported with
// the set it could have been, on both the addition and the removal, since a
// refusal that only says "unknown" leaves the caller guessing.
func TestAnUnknownTypeNamesTheAcceptedSet(t *testing.T) {
	rw := resolveScratch(t, scratchConfig)

	for _, tc := range []struct {
		name string
		run  func() error
	}{
		{"add", func() error {
			_, err := Add(context.Background(), rw, AddOptions{Socket: "nonsense", Query: "x"})
			return err
		}},
		{"remove", func() error {
			_, err := Remove(rw, RemoveOptions{Socket: "nonsense", Query: "x"})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			if err == nil {
				t.Fatal("an unknown type was accepted")
			}
			for _, want := range []string{`unknown type "nonsense"`, ProviderSocket, "cli"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not name %q: %v", want, err)
				}
			}
		})
	}
}

// TestTheCommandExtensionTypeIsRefusedWithNoInstaller drives the seam's own
// failure, because a gate whose failure mode nobody has seen fire is decoration.
//
// It is the case a caller reaches on a surface that was wired without an
// installer. Reporting success there would be the worst available answer: nothing
// is installed, and the caller is told a command now exists.
func TestTheCommandExtensionTypeIsRefusedWithNoInstaller(t *testing.T) {
	rw := resolveScratch(t, scratchConfig)

	_, err := Add(context.Background(), rw, AddOptions{Socket: "cli", Query: "anything"})
	if err == nil {
		t.Fatal("a command-extension addition ran with no installer supplied")
	}
	if !strings.Contains(err.Error(), "no installer was supplied") {
		t.Fatalf("the refusal does not name the missing installer: %v", err)
	}
	// The refusal must precede the registry enumeration, so a caller that cannot
	// install is not made to wait on a network round trip first. Nothing pointed
	// the registry seam anywhere, so a reach for it would have failed with a
	// different message than the one asserted above.
}

// TestTheInstallerReceivesThePinnedReferenceAndTheConsent pins what the seam is
// handed, since everything the installer does is decided by that request and a
// wrong field there is invisible from the operation's own result.
//
// The registry and version seams are pointed at fakes, so the addition runs with
// no network: what is under test is the composition of the reference and the
// forwarding of the consent, not the registry.
func TestTheInstallerReceivesThePinnedReferenceAndTheConsent(t *testing.T) {
	rw := resolveScratch(t, scratchConfig)

	prevTarget := RegistryTarget
	RegistryTarget = func(workspaceconfig.Config) (registryops.RepoLister, string, error) {
		return fakeLister{repos: []string{"plugin/cli/greet"}}, "registry.invalid", nil
	}
	t.Cleanup(func() { RegistryTarget = prevTarget })

	prevTags := RepoTagLister
	RepoTagLister = func(context.Context, workspaceconfig.Config, string, string) ([]string, error) {
		return []string{"1.0.0", "1.2.0"}, nil
	}
	t.Cleanup(func() { RepoTagLister = prevTags })

	var got CLIInstallRequest
	calls := 0
	res, err := Add(context.Background(), rw, AddOptions{
		Socket:             "cli",
		Query:              "greet",
		AcceptCapabilities: true,
		InstallCLIPlugin: func(_ context.Context, _ *workspaceconfig.ResolvedWorkspace, req CLIInstallRequest) error {
			calls++
			got = req
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Add (cli): %v", err)
	}
	if calls != 1 {
		t.Fatalf("the installer ran %d times; want exactly once", calls)
	}
	if want := "oci://registry.invalid/plugin/cli/greet:1.2.0"; got.Reference != want {
		t.Errorf("installer reference = %q; want %q (the highest strict-SemVer tag, pinned)", got.Reference, want)
	}
	if !got.AcceptCapabilities {
		t.Error("the consent the caller supplied did not reach the installer")
	}
	if res.Reference != got.Reference {
		t.Errorf("the result reports %q and the installer was handed %q; one answer, two values", res.Reference, got.Reference)
	}
	if res.Identity != "" || res.URI != "" {
		t.Errorf("a command-extension install filled a configuration-attachment field: %+v", res)
	}
}

// TestTheWriteTargetsReachIsReadOffTheResolvedPath pins the classification both
// ways round, because a reach reported from the flags alone would be right until
// any one of the policy's three inputs moved — and the third input is the
// operation's own nature, which no flag carries.
//
// A provider binding is build-scoped and defaults to the workspace file; a
// command extension is tool-scoped and defaults to the machine-wide one. Driving
// both is what makes this a test of the derivation rather than of one row.
func TestTheWriteTargetsReachIsReadOffTheResolvedPath(t *testing.T) {
	rw := resolveScratch(t, scratchConfig)
	workspaceFile := filepath.Join(rw.Root(), workspaceconfig.ConfigFilename)

	for _, tc := range []struct {
		name            string
		op              workspaceconfig.ConfigOperation
		global          bool
		workspace       bool
		wantMachineWide bool
	}{
		{"a provider binding defaults to the workspace file", workspaceconfig.OpProviderBinding, false, false, false},
		{"a config socket defaults to the workspace file", workspaceconfig.OpConfigSocket, false, false, false},
		{"a command extension defaults to the machine-wide file", workspaceconfig.OpCLI, false, false, true},
		{"an explicit workspace override is workspace-scoped", workspaceconfig.OpCLI, false, true, false},
		{"an explicit global override is machine-wide", workspaceconfig.OpConfigSocket, true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, machineWide, err := writeTarget(rw, tc.op, tc.global, tc.workspace)
			if err != nil {
				t.Fatalf("writeTarget: %v", err)
			}
			if machineWide != tc.wantMachineWide {
				t.Errorf("machineWide = %v for %s; want %v", machineWide, path, tc.wantMachineWide)
			}
			// The two halves must agree: a workspace-scoped answer is exactly the one
			// naming this resolution's own file, which is what makes the boolean a
			// reading of the path rather than a second opinion about it.
			if (path == workspaceFile) == machineWide {
				t.Errorf("the path %q and the reach %v disagree", path, machineWide)
			}
		})
	}
}

// TestBothOverridesTogetherAreRefused pins that the write-target policy's own
// refusal reaches a caller of this layer, rather than one of the two flags
// silently winning.
func TestBothOverridesTogetherAreRefused(t *testing.T) {
	rw := resolveScratch(t, scratchConfig)
	if _, err := Remove(rw, RemoveOptions{
		Socket: "governance", Query: "tag-injector", Global: true, Workspace: true,
	}); err == nil {
		t.Fatal("both write-target overrides were accepted together")
	}
}

// TestARemovalThatMatchesNothingHalts pins that a removal is never a silent
// no-op: a query matching no entry fails, so a typo cannot be reported as a
// plugin that stopped participating.
func TestARemovalThatMatchesNothingHalts(t *testing.T) {
	rw := resolveScratch(t, scratchConfig)

	if _, err := Remove(rw, RemoveOptions{Socket: "governance", Query: "no-such-plugin"}); err == nil {
		t.Fatal("a removal matching nothing reported success")
	} else if !strings.Contains(err.Error(), "no-such-plugin") {
		t.Errorf("the refusal does not name the query that matched nothing: %v", err)
	}

	res, err := Remove(rw, RemoveOptions{Socket: "governance", Query: "tag-injector"})
	if err != nil {
		t.Fatalf("removing the seeded attachment: %v", err)
	}
	if res.Socket != "governance" || res.Query != "tag-injector" {
		t.Errorf("the result does not describe what was removed: %+v", res)
	}
	if res.ConfigIsMachineWide {
		t.Error("a socket removal reported a machine-wide reach")
	}
	// Removing it twice is what proves the first call changed the file rather than
	// reporting a change it did not make.
	if _, err := Remove(rw, RemoveOptions{Socket: "governance", Query: "tag-injector"}); err == nil {
		t.Fatal("the entry survived a removal that reported success")
	}
}

// TestTheTypeSpecificFieldsAreRefusedOnTheWrongType pins that a field which does
// nothing on the type supplied is refused rather than ignored, because a field
// silently doing nothing reads exactly like one that worked.
func TestTheTypeSpecificFieldsAreRefusedOnTheWrongType(t *testing.T) {
	rw := resolveScratch(t, scratchConfig)

	for _, tc := range []struct {
		name string
		opts AddOptions
		want string
	}{
		{"a capability on a socket type", AddOptions{Socket: "linter", Query: "x", Capability: "c"},
			"--capability applies only to the provider type"},
		{"a role on a non-governance type", AddOptions{Socket: "linter", Query: "x", Role: "label-transform"},
			"--role applies only to the governance type"},
		{"a consent flag on a non-command-extension type", AddOptions{Socket: "linter", Query: "x", AcceptCapabilities: true},
			"apply only to the cli type"},
		{"a provider with no capability", AddOptions{Socket: ProviderSocket, Query: "x"},
			"requires --capability"},
		{"governance with no role", AddOptions{Socket: "governance", Query: "x"},
			"requires --role"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Add(context.Background(), rw, tc.opts)
			if err == nil {
				t.Fatal("the mismatched field was accepted")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refusal = %v; want it to contain %q", err, tc.want)
			}
		})
	}
}

// TestResolveSingleRepoPrefersAnExactNameOverASubstring pins the reduction's one
// non-obvious rule and both halting cases, since a query resolving to the wrong
// plugin is worse than one resolving to none.
func TestResolveSingleRepoPrefersAnExactNameOverASubstring(t *testing.T) {
	for _, tc := range []struct {
		name    string
		matches []string
		query   string
		want    string
		wantErr string
	}{
		{"one match is used directly", []string{"plugin/governance/tagger"}, "tag", "plugin/governance/tagger", ""},
		{"an exact final-segment match wins",
			[]string{"plugin/governance/tag", "plugin/governance/tag-injector"}, "tag", "plugin/governance/tag", ""},
		{"no match halts naming the query", nil, "tag", "", "no governance plugin in the registry matches"},
		{"an ambiguous query halts listing the candidates",
			[]string{"plugin/governance/tag-a", "plugin/governance/tag-b"}, "tag", "",
			"matches 2 governance plugins"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveSingleRepo(tc.matches, tc.query, "governance")
			switch {
			case tc.wantErr != "":
				if err == nil {
					t.Fatalf("want a refusal containing %q; got %q", tc.wantErr, got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("refusal = %v; want it to contain %q", err, tc.wantErr)
				}
			case err != nil:
				t.Fatalf("ResolveSingleRepo: %v", err)
			case got != tc.want:
				t.Fatalf("got %q; want %q", got, tc.want)
			}
		})
	}
}

// TestTheKindsHintNamesEverySocketTheClassifierAccepts holds the hint and the
// classifier to one vocabulary, so a socket admitted by one and unnamed by the
// other cannot ship — which is how a caller ends up told to use a type the
// operation then refuses, or refused a type nothing advertises.
func TestTheKindsHintNamesEverySocketTheClassifierAccepts(t *testing.T) {
	hint := KindsHint()
	for _, socket := range strings.Split(hint, ", ") {
		if _, _, err := classifySocket("probe", socket); err != nil {
			t.Errorf("the hint advertises %q, which the classifier refuses: %v", socket, err)
		}
	}
	if !strings.Contains(hint, ProviderSocket) {
		t.Errorf("the hint does not name the provider socket: %q", hint)
	}
	if IsConfigSocket(ProviderSocket) {
		t.Error("the provider socket is reported as a config socket; it binds a capability rather than attaching to a socket list")
	}
	if !IsConfigSocket("governance") {
		t.Error("the governance socket is not reported as a config socket")
	}
}

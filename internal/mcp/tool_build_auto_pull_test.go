// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

// The build tool's dependency fill: whether it runs, what it writes, and what it
// reports having written.
//
// Every assertion here is about the WORKSPACE rather than about the answer's
// shape. A build that decoded the argument and threw it away answers exactly like
// one that honoured it — same files, same success — so a case reading only the
// response would pass against a tool that fills nothing. What separates them is
// the lockfile on disk, which is why each case below reads it before and after and
// compares the bytes.
//
// The fixture carries NO lockfile, which is what makes the fill observable: the
// file is absent before and present after, so its appearance cannot be confused
// with a rewrite of something that was already there.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// fillWorkspace is pullWorkspace with the workspace's own auto_pull setting
// written at the key the loader reads it from, so a case can drive either side of
// that input.
//
// The setting is written at the TOP level rather than inside the workspace block,
// because that is where the loader declares it; nested under the block it parses
// as nothing, and a case asserting the configured-on rows would then be measuring
// the configured-off behaviour while reporting itself as the opposite.
//
// Offline stays on for the same reason pullWorkspace sets it: the egress gate
// refuses before any dial, so the resolution answers from the copied catalog alone
// and no case here measures a registry's availability.
func fillWorkspace(t *testing.T, autoPull bool) (s *server, root string, dirs []string, releasePath string) {
	t.Helper()
	s, root, dirs, releasePath = pullWorkspace(t)
	cfg := "workspace: {}\noffline: true\n"
	if autoPull {
		cfg += "auto_pull: true\n"
	}
	if err := os.WriteFile(filepath.Join(root, workspaceconfig.ConfigFilename), []byte(cfg), 0o644); err != nil {
		t.Fatalf("write the workspace config: %v", err)
	}
	// The setting has to reach the resolved config, not merely the file. A key the
	// loader does not read parses as nothing and leaves every configured-on row
	// below asserting against a workspace configured off.
	rw, err := workspaceconfig.Resolve(root, root, func(string) string { return "" })
	if err != nil {
		t.Fatalf("resolve the fixture workspace: %v", err)
	}
	if got := rw.Config().AutoPull; got != autoPull {
		t.Fatalf("precondition: the fixture's resolved auto_pull is %v, want %v; the setting was written to a "+
			"key the loader does not read, so no row here would measure what it claims", got, autoPull)
	}
	return s, root, dirs, releasePath
}

// lockfileBytes reads the Release's lockfile, reporting absence distinctly from
// content so a case can tell "no file" from "a file that happens to be empty".
func lockfileBytes(t *testing.T, releasePath string) (present bool, content []byte) {
	t.Helper()
	b, err := os.ReadFile(lockfile.PathFor(releasePath))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		t.Fatalf("read the lockfile: %v", err)
	}
	return true, b
}

// callBuildWith drives the build tool with an explicit fill argument.
func callBuildWith(t *testing.T, s *server, root string, dirs []string, releasePath string, autoPull bool) *callToolResult {
	t.Helper()
	return callTool(t, s, "build", buildArgs{
		Release:       releasePath,
		WorkspaceRoot: root,
		CatalogDirs:   dirs,
		AutoPull:      autoPull,
	})
}

// TestBuildFillWritesTheLockfileItWasAskedFor is the falsifiable pair the argument
// exists to produce: one workspace, two calls differing only in that argument, and
// a different workspace on disk afterwards.
//
// The off half is not decoration. On its own the on half would pass against a tool
// that filled unconditionally, which is the failure mode that would turn every
// in-memory render into a registry-reaching write. The two halves run against
// separate copies of the same fixture, so neither can leave state the other reads.
func TestBuildFillWritesTheLockfileItWasAskedFor(t *testing.T) {
	t.Run("asked for, it writes the lockfile", func(t *testing.T) {
		s, root, dirs, releasePath := fillWorkspace(t, false)

		if present, _ := lockfileBytes(t, releasePath); present {
			t.Fatal("precondition: the fixture already carries a lockfile, so its appearance below would prove " +
				"nothing about the fill")
		}

		res := callBuildWith(t, s, root, dirs, releasePath, true)
		if res.IsError {
			t.Fatalf("the build refused with the fill asked for; the resolution it runs is the one the pull tool "+
				"runs, so the same workspace resolves: %s", res.Content[0].Text)
		}

		present, content := lockfileBytes(t, releasePath)
		if !present {
			t.Fatal("the build was asked to resolve its dependencies first and wrote no lockfile, so the " +
				"argument reached nothing")
		}
		if len(content) == 0 {
			t.Fatal("the fill wrote an empty lockfile")
		}
		lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
		if err != nil {
			t.Fatalf("the fill wrote no readable lockfile: %v", err)
		}
		if lf == nil || len(lf.Entities) == 0 {
			t.Fatal("the filled lockfile pins no entity, so the resolution reached nothing")
		}

		// The render still happened, and it rendered against what the fill wrote.
		payload := unmarshalBuildPayload(t, res)
		if len(payload.Files) == 0 {
			t.Fatal("the filling build returned no rendered files")
		}
	})

	t.Run("not asked for, it writes nothing", func(t *testing.T) {
		s, root, dirs, releasePath := fillWorkspace(t, false)

		res := callBuildWith(t, s, root, dirs, releasePath, false)
		if res.IsError {
			t.Fatalf("this surface renders a Release with no lockfile from whatever pins the workspace holds; "+
				"it refused instead: %s", res.Content[0].Text)
		}
		if present, _ := lockfileBytes(t, releasePath); present {
			t.Fatal("a build that was not asked to resolve wrote a lockfile, so the fill runs on calls that " +
				"never requested it")
		}
		if payload := unmarshalBuildPayload(t, res); payload.DependencyResolution != nil {
			t.Errorf("a build that ran no fill reported one: %+v", payload.DependencyResolution)
		}
	})
}

// TestBuildFillHonoursTheWorkspaceSetting is the two-axis grid, stated as its four
// cells rather than as its margins.
//
// The axes are the workspace's own auto_pull setting and the call's argument, and
// they are ADDITIVE: either one turns the fill on, and neither can turn off a fill
// the other asked for. Naming only the margins — "the argument works", "the setting
// works" — would leave the interior unstated, and the interior is where the two
// interact: the configured-on-and-argument-absent cell is the one a surface that
// merely decoded its argument would get wrong, and the configured-on-and-argument-
// false cell is the one an implementation treating the argument as an override
// would get wrong. Both are cells, not margins.
func TestBuildFillHonoursTheWorkspaceSetting(t *testing.T) {
	for _, tc := range []struct {
		name string
		// configured is the workspace's own auto_pull setting.
		configured bool
		// argument is the call's auto_pull, nil meaning the key is absent from the
		// arguments document rather than present and false. The two are sent
		// separately because a client that never learned the argument must behave
		// exactly like one that sent it false, and marshalling a struct would
		// collapse the distinction this row rests on.
		argument *bool
		wantFill bool
	}{
		{name: "configured off, argument absent", configured: false, argument: nil, wantFill: false},
		{name: "configured off, argument false", configured: false, argument: boolPtr(false), wantFill: false},
		{name: "configured off, argument true", configured: false, argument: boolPtr(true), wantFill: true},
		{name: "configured on, argument absent", configured: true, argument: nil, wantFill: true},
		{name: "configured on, argument false", configured: true, argument: boolPtr(false), wantFill: true},
		{name: "configured on, argument true", configured: true, argument: boolPtr(true), wantFill: true},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			s, root, dirs, releasePath := fillWorkspace(t, tc.configured)

			if present, _ := lockfileBytes(t, releasePath); present {
				t.Fatal("precondition: the fixture already carries a lockfile, so this row could not tell a " +
					"fill from an untouched file")
			}

			args := map[string]any{
				"release":        releasePath,
				"workspace_root": root,
				"catalog_dirs":   dirs,
			}
			if tc.argument != nil {
				args["auto_pull"] = *tc.argument
			}
			raw, err := json.Marshal(args)
			if err != nil {
				t.Fatalf("marshal the arguments document: %v", err)
			}
			res := callTool(t, s, "build", json.RawMessage(raw))
			if res.IsError {
				t.Fatalf("the build refused: %s", res.Content[0].Text)
			}

			present, _ := lockfileBytes(t, releasePath)
			if present != tc.wantFill {
				t.Errorf("the fill ran = %v, want %v; with the workspace setting %v and the argument %s, the "+
					"two inputs are additive and either one turns the fill on",
					present, tc.wantFill, tc.configured, describeArgument(tc.argument))
			}
			// The report travels with the fill, on the same condition, so a caller
			// can tell a filled build from an unfilled one without reading the disk.
			reported := unmarshalBuildPayload(t, res).DependencyResolution != nil
			if reported != tc.wantFill {
				t.Errorf("the answer reports a resolution = %v while the fill ran = %v; the two must agree, or "+
					"a caller cannot tell whether its own build wrote a lockfile", reported, present)
			}
		})
	}
}

// boolPtr distinguishes an argument sent false from one not sent at all.
func boolPtr(b bool) *bool { return &b }

// describeArgument names an argument's position for a failure message, keeping
// "absent" distinct from "false".
func describeArgument(b *bool) string {
	if b == nil {
		return "absent"
	}
	if *b {
		return "true"
	}
	return "false"
}

// TestBuildFillArgumentDefaultsOff pins the default as a property of the DECODE
// rather than as a value someone wrote down.
//
// It is asserted here as well as behaviourally above because the two fail
// differently. A default flipped to true would still render the same workspaces
// successfully — this surface renders a Release with no lockfile rather than
// refusing one — so the behavioural rows could go on passing while every call that
// omitted the argument silently reached a registry and wrote a lockfile.
func TestBuildFillArgumentDefaultsOff(t *testing.T) {
	t.Parallel()

	var omitted, explicit buildArgs
	if err := json.Unmarshal([]byte(`{"release":"r.yaml"}`), &omitted); err != nil {
		t.Fatalf("decode the omitting document: %v", err)
	}
	if err := json.Unmarshal([]byte(`{"release":"r.yaml","auto_pull":false}`), &explicit); err != nil {
		t.Fatalf("decode the explicit document: %v", err)
	}
	if omitted.AutoPull != explicit.AutoPull {
		t.Errorf("omitting the argument decodes AutoPull to %v and sending it false to %v; a client that never "+
			"learned the argument must behave exactly like one that sent it off",
			omitted.AutoPull, explicit.AutoPull)
	}
	if omitted.AutoPull {
		t.Error("a call that omits the argument asks for a dependency fill; the command line performs none " +
			"unless asked, and this surface carries the same default")
	}
}

// TestBuildFillReportsWhatTheResolutionToolReports is the account half: what a
// build's own fill hands back is what the tool a caller already reads hands back
// for the same resolution.
//
// Parity is the assertion rather than a list of fields, because the fill and the
// pull tool are one operation projected once. A per-field checklist would pass
// against a build that projected its own report and happened to agree today; this
// fails the moment the two descriptions of one resolution diverge.
//
// Both calls run against the SAME workspace, in order: the build fills it, then
// the pull tool reconciles it. The reconciling call rewrites nothing on an
// unchanged dependency set, so it observes the same resolution rather than
// producing a second one, and the two reports are comparable field for field —
// including the lockfile path, which two separate temporary workspaces would
// necessarily disagree about for reasons having nothing to do with the subject.
//
// ⚠️ The Diagnostics leg is a NON-REGRESSION here and not a positive
// demonstration, which is recorded so nobody reads more into a green than it
// carries. This fixture is offline and resolves entirely from its copied catalog,
// so the resolution fetches nothing, verifies no signature and skips no withdrawn
// version — the three things that raise a line — and BOTH sides therefore report
// none. What makes the channel itself sound is structural rather than measured
// here: the build's fill and the pull tool run one function, so the lines reach a
// caller through one projection or through neither.
func TestBuildFillReportsWhatTheResolutionToolReports(t *testing.T) {
	s, root, dirs, releasePath := fillWorkspace(t, false)

	res := callBuildWith(t, s, root, dirs, releasePath, true)
	if res.IsError {
		t.Fatalf("the filling build refused: %s", res.Content[0].Text)
	}

	payload := unmarshalBuildPayload(t, res)
	if payload.DependencyResolution == nil {
		t.Fatal("the build ran a fill and reported no resolution, so a caller cannot find out what its own " +
			"build just pinned without sending a second call")
	}
	report := payload.DependencyResolution

	if report.Lockfile != lockfile.PathFor(releasePath) {
		t.Errorf("the report names lockfile %q, want %q", report.Lockfile, lockfile.PathFor(releasePath))
	}
	if len(report.Resolved) == 0 {
		t.Fatal("the report names nothing the resolution reached, so every comparison below would hold two " +
			"empty accounts against each other and prove nothing")
	}

	// The same resolution, through the tool a caller reads it from.
	pull := callPull(t, s, pullArgs{Release: releasePath, WorkspaceRoot: root, CatalogDirs: dirs})
	if pull.IsError {
		t.Fatalf("the reconciling resolution refused, so there is nothing to compare against: %s",
			pull.Content[0].Text)
	}
	fromPullTool := unmarshalPullPayload(t, pull)

	if !reflect.DeepEqual(report.Resolved, fromPullTool.Resolved) {
		t.Errorf("the build's fill and the pull tool describe the same resolution differently.\n fill: %+v\n pull: %+v",
			report.Resolved, fromPullTool.Resolved)
	}
	if !reflect.DeepEqual(report.Diagnostics, fromPullTool.Diagnostics) {
		t.Errorf("the build's fill and the pull tool carry different lines for the same resolution.\n fill: %q\n pull: %q",
			report.Diagnostics, fromPullTool.Diagnostics)
	}
	if report.Lockfile != fromPullTool.Lockfile {
		t.Errorf("the two name different lockfiles for one Release: fill %q, pull %q",
			report.Lockfile, fromPullTool.Lockfile)
	}
	if report.SourceArtifacts != fromPullTool.SourceArtifacts ||
		report.ExternalInputs != fromPullTool.ExternalInputs ||
		report.TrustedPlugins != fromPullTool.TrustedPlugins {
		t.Errorf("the two count the same resolution differently: fill %d/%d/%d, pull %d/%d/%d",
			report.SourceArtifacts, report.ExternalInputs, report.TrustedPlugins,
			fromPullTool.SourceArtifacts, fromPullTool.ExternalInputs, fromPullTool.TrustedPlugins)
	}

	// The text form carries the same account, so a caller choosing it is not told
	// less than one reading the structured answer.
	text := res.Content[0].Text
	if !strings.Contains(text, report.Lockfile) {
		t.Errorf("the text answer does not name the lockfile the fill wrote, so a caller reading it is not "+
			"told the call wrote one:\n%s", text)
	}
}

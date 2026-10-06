// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/opsurface"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// linkedConfigurationLayout is one workspace whose configuration file — the
// workspace's own, or the CI-owned overlay beside it — is, row by row, a file
// inside it or a link to a file outside it.
type linkedConfigurationLayout struct {
	root, outside string
	// held is the configuration file the layout makes a link leading out, relative
	// to the root, and empty where no file is one.
	held string
}

// layLinkedConfiguration lays the workspace: one Release and one Aggregate at the
// root, and a Feature in a directory outside the workspace that the configuration
// lists as a catalog directory — so a listing that read the configuration would
// carry that Feature, and one that read none does not. linkRoot makes the
// workspace's own configuration file a link leading out; linkCI makes the CI-owned
// overlay one, beside a configuration file inside. ignored has the workspace's
// .gitignore leave the held file out, so the walk reading the workspace never
// meets it and a listing names it only because the held view adds it.
func layLinkedConfiguration(t *testing.T, linkRoot, linkCI, ignored bool) linkedConfigurationLayout {
	t.Helper()
	l := linkedConfigurationLayout{root: t.TempDir(), outside: t.TempDir()}
	if ignored {
		plantFile(t, filepath.Join(l.root, ".gitignore"), workspaceconfig.ConfigFilename+"\n"+
			workspaceconfig.AppendCIConfigFilename+"\n")
	}
	writeRelease(t, l.root, "release.yaml", "release/only")
	plantFile(t, filepath.Join(l.root, "aggregate.yaml"),
		"apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: only\nspec:\n  components: {}\n")
	plantFile(t, filepath.Join(l.outside, "cat", "feature.yaml"),
		"apiVersion: aggrega.ai/v1alpha1\nkind: Feature\nmetadata:\n  name: local/listed-outside\n  version: 1.0.0\n"+
			"spec:\n  appliesTo: \"component.aggrega.ai/category=datastore\"\n")
	config := "workspace:\n  catalog_dirs:\n    - " + filepath.Join(l.outside, "cat") + "\n"
	configPath := filepath.Join(l.root, workspaceconfig.ConfigFilename)
	switch {
	case linkRoot:
		plantFile(t, filepath.Join(l.outside, "config.yaml"), config)
		plantLink(t, filepath.Join(l.outside, "config.yaml"), configPath)
		l.held = workspaceconfig.ConfigFilename
	case linkCI:
		plantFile(t, configPath, config)
		plantFile(t, filepath.Join(l.outside, "ci.yaml"), "catalog_dirs_append: []\n")
		plantLink(t, filepath.Join(l.outside, "ci.yaml"), filepath.Join(l.root, workspaceconfig.AppendCIConfigFilename))
		l.held = workspaceconfig.AppendCIConfigFilename
	default:
		plantFile(t, configPath, config)
	}
	return l
}

// heldConfigurationSession is a session over no workspace of its own, whose global
// configuration layer is an empty file of the test's own, so a workspace a
// request names is resolved by the routing path from that workspace alone and
// nothing on the machine running the test is read.
func heldConfigurationSession(t *testing.T) *session {
	t.Helper()
	sess := newTestSession(t, t.TempDir(), nil, nil)
	global := filepath.Join(t.TempDir(), "config.yaml")
	plantFile(t, global, "registries: []\n")
	sess.flags = workspaceconfig.FlagOverlay{ConfigPath: global}
	return sess
}

// TestOnlyTheListingsAreAnsweredOverAHeldConfiguration holds the routing path's
// one decision for a workspace whose configuration is held: EVERY method the
// routing index carries — read from the index, so a method added tomorrow is held
// to the rule with no edit here — is handed the held view only where its operation
// lists what a workspace holds, and is refused with the hold otherwise.
//
// It asks the decision itself rather than driving the handlers, and that is
// deliberate: a method wrongly let through would run, and several write or reach
// past the workspace; the decision is where a configuration-decided method would
// be handed nothing configured, so it is what is asserted, method by method. The
// methods the decision lets through are then driven below.
//
// The control row — the same workspace with its configuration inside — is resolved
// for every method, which is the evidence the refusals are the hold's and not the
// workspace's.
func TestOnlyTheListingsAreAnsweredOverAHeldConfiguration(t *testing.T) {
	t.Parallel()
	condition := readscope.ErrReadLeadsOutOfWorkspace()

	for _, row := range []struct {
		name             string
		linkRoot, linkCI bool
	}{
		{name: "control: the configuration inside the workspace"},
		{name: "the workspace's own configuration file a link leading out", linkRoot: true},
		{name: "the CI-owned overlay a link leading out", linkCI: true},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			l := layLinkedConfiguration(t, row.linkRoot, row.linkCI, false)
			sess := heldConfigurationSession(t)

			listings, refused := 0, 0
			for method, route := range methodIndex {
				if route.workspaceFree {
					continue
				}
				ws, err := sess.workspaceForRoute(route, l.root)
				if l.held == "" {
					if err != nil || ws == nil || ws.held != nil {
						t.Errorf("control, %s: the workspace was not resolved: %v", method, err)
					}
					continue
				}
				if opsurface.AnswersOverHeldConfiguration(route.operation) {
					listings++
					if err != nil || ws == nil || ws.held == nil {
						t.Errorf("%s lists what the workspace holds and was not handed the held view: %v", method, err)
					}
					continue
				}
				refused++
				if err == nil {
					t.Errorf("%s is decided by the configuration and was handed a workspace over the held one — it "+
						"would run on nothing configured (held view %v)", method, ws.held != nil)
					continue
				}
				if !errors.Is(err, condition) || !strings.Contains(err.Error(), "the link "+filepath.Join(l.root, l.held)+" leads to") {
					t.Errorf("%s was refused, but not with the hold naming the link: %v", method, err)
				}
			}
			if l.held != "" && (listings != 2 || refused == 0) {
				t.Errorf("walked %d listing method(s) and %d refused one(s); want the two listings and every other "+
					"method refused — the routing index or the declaration changed under this test", listings, refused)
			}
		})
	}
}

// TestAHeldConfigurationIsListedAndNamedAndEverythingElseRefused drives the
// production dispatch over the same layouts: the two listings answer, read with
// nothing configured, and name the held file among the files they did not read;
// a listing in a Release's terms, and the methods a configuration decides, are
// refused with the hold — the invalid-params code a workspace that cannot be
// configured always drew, with the condition's codes in the data and its short
// code ahead of the message. Nothing is written inside the workspace or outside.
//
// The Feature in the catalog directory the configuration lists is the measure of
// "nothing configured": the control's index carries it, and a held index that
// read any of the configuration would carry it too. The row whose .gitignore
// leaves the held file out is the one where the walk never meets it, so it is
// named there only because the held view names every held file itself.
func TestAHeldConfigurationIsListedAndNamedAndEverythingElseRefused(t *testing.T) {
	t.Parallel()
	condition := readscope.ErrReadLeadsOutOfWorkspace()
	wantCode, _ := cerrors.CodeOf(condition)
	wantShort, _ := cerrors.ShortCodeOf(condition)

	for _, row := range []struct {
		name                      string
		linkRoot, linkCI, ignored bool
	}{
		{name: "control: the configuration inside the workspace"},
		{name: "the workspace's own configuration file a link leading out", linkRoot: true},
		{name: "the CI-owned overlay a link leading out", linkCI: true},
		{name: "a held file the .gitignore leaves out is still named", linkRoot: true, ignored: true},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			l := layLinkedConfiguration(t, row.linkRoot, row.linkCI, row.ignored)
			sess := heldConfigurationSession(t)
			before := treeListing(t, l.outside)
			call := func(method string, params map[string]any) jsonRPCResponse {
				var in, out bytes.Buffer
				in.Write(framedRequest(1, method, params))
				return runOneRequestIn(t, sess, l.root, &in, &out)
			}

			for _, method := range []string{"aggrega/releaseCandidates", "aggrega/catalogIndex"} {
				resp := call(method, map[string]any{})
				if resp.Error != nil {
					t.Fatalf("%s was refused: %d %s", method, resp.Error.Code, resp.Error.Message)
				}
				var answer struct {
					Releases          []string        `json:"releases"`
					Entities          []CatalogEntity `json:"entities"`
					UnreadLinkedFiles []string        `json:"unreadLinkedFiles"`
				}
				if err := json.Unmarshal(resp.Result, &answer); err != nil {
					t.Fatalf("%s: decode the answer: %v (%s)", method, err, resp.Result)
				}
				named := slices.Contains(answer.UnreadLinkedFiles, l.held)
				if l.held != "" && !named {
					t.Errorf("%s names %q as not read; want the held file %q among them", method, answer.UnreadLinkedFiles, l.held)
				}
				if l.held == "" && len(answer.UnreadLinkedFiles) != 0 {
					t.Errorf("control, %s names %q as not read; want nothing", method, answer.UnreadLinkedFiles)
				}
				if method == "aggrega/catalogIndex" {
					listed := false
					for _, e := range answer.Entities {
						if e.Name == "local/listed-outside" {
							listed = true
						}
					}
					if listed != (l.held == "") {
						t.Errorf("%s lists the Feature of the configured catalog directory = %v; want %v — a held "+
							"listing reads no configuration, and the control reads all of it", method, listed, l.held == "")
					}
				}
			}

			for _, c := range []struct {
				method string
				params map[string]any
			}{
				{"aggrega/catalogIndex", map[string]any{"release": "release.yaml"}},
				{"aggrega/irGraph", map[string]any{"release": "release.yaml"}},
				{"aggrega/build", map[string]any{"release": "release.yaml"}},
				{"aggrega/validate", map[string]any{"file": "release.yaml", "content": releaseYAML("release/only")}},
				{"aggrega/effectiveConfiguration", map[string]any{}},
				{"aggrega/activeRelease", map[string]any{}},
				{"aggrega/setActiveRelease", map[string]any{"release": "release.yaml"}},
			} {
				resp := call(c.method, c.params)
				if l.held == "" {
					if resp.Error != nil && resp.Error.Data != nil && resp.Error.Data.Code == wantCode {
						t.Errorf("control, %s was refused with the hold: %s", c.method, resp.Error.Message)
					}
					continue
				}
				if resp.Error == nil {
					t.Errorf("%s %v answered over a held configuration — it ran on nothing configured: %s", c.method, c.params, resp.Result)
					continue
				}
				if resp.Error.Code != jsonRPCInvalidParams {
					t.Errorf("%s: code %d; want %d, the code a workspace that cannot be configured draws", c.method, resp.Error.Code, jsonRPCInvalidParams)
				}
				if d := resp.Error.Data; d == nil || d.Code != wantCode || d.ShortCode != wantShort {
					t.Errorf("%s: data %+v; want the hold's codes %q / %q", c.method, resp.Error.Data, wantCode, wantShort)
				}
				link := filepath.Join(l.root, l.held)
				if !strings.HasPrefix(resp.Error.Message, wantShort+": ") ||
					!strings.Contains(resp.Error.Message, "the link "+link+" leads to "+filepath.Join(l.outside)) {
					t.Errorf("%s: the refusal does not open with the short code and name the link and where it leads: %s",
						c.method, resp.Error.Message)
				}
			}

			if l.held != "" {
				if _, err := os.Lstat(filepath.Join(l.root, ".aggrega")); !os.IsNotExist(err) {
					t.Errorf("a held workspace had its marker directory created (lstat: %v)", err)
				}
			}
			if after := treeListing(t, l.outside); strings.Join(after, "\n") != strings.Join(before, "\n") {
				t.Errorf("the directory outside the workspace changed:\nbefore:\n  %s\nafter:\n  %s",
					strings.Join(before, "\n  "), strings.Join(after, "\n  "))
			}
		})
	}
}

// TestTheHandshakeAnswersOverAHeldConfiguration holds the handshake to answering
// for a declared workspace whose configuration is held: a client refused it could
// ask nothing, not even the listings that say why everything else is refused. It
// names the root and reports the built-in defaults as the layer in force, since no
// configuration file contributed anything the held view answers with.
func TestTheHandshakeAnswersOverAHeldConfiguration(t *testing.T) {
	t.Parallel()
	l := layLinkedConfiguration(t, true, false, false)
	sess := heldConfigurationSession(t)
	srv := sess.workspaceFree()
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/initialize", map[string]any{"workspaceRoot": l.root}))
	resp := runOneRequestIn(t, srv.session, l.root, &in, &out)
	if resp.Error != nil {
		t.Fatalf("the handshake declaring a held workspace was refused: %d %s", resp.Error.Code, resp.Error.Message)
	}
	var answer initializeResult
	if err := json.Unmarshal(resp.Result, &answer); err != nil {
		t.Fatalf("decode the handshake: %v (%s)", err, resp.Result)
	}
	if answer.WorkspaceRoot != l.root || answer.ConfigSource != workspaceconfig.SourceDefaults.String() {
		t.Errorf("the handshake answered root %q, layer %q; want %q and the built-in defaults", answer.WorkspaceRoot,
			answer.ConfigSource, l.root)
	}
}

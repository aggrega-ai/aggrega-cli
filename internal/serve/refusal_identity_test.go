// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// TestConditionRefusalIdentifiesTheConditionItsFailureNames holds the one place a
// refusal's identity is decided to its rule, row by row: the condition the cause
// names is carried in the data under both identifiers, read through the same
// accessors a diagnostic is read through, and its short code opens the message
// once; an answer whose cause names no condition, or that has no cause, is left
// exactly as it was; and the answer handed in is never changed.
func TestConditionRefusalIdentifiesTheConditionItsFailureNames(t *testing.T) {
	t.Parallel()
	condition := readscope.ErrReadLeadsOutOfWorkspace()
	code, _ := cerrors.CodeOf(condition)
	short, _ := cerrors.ShortCodeOf(condition)
	named := fmt.Errorf("walk the catalog: %w", condition)

	for _, row := range []struct {
		name        string
		in          jsonRPCError
		wantMessage string
		wantData    *errorData
	}{
		{name: "no cause: left as it is",
			in:          jsonRPCError{Code: jsonRPCInvalidParams, Message: "invalid params: release is required"},
			wantMessage: "invalid params: release is required"},
		{name: "a cause naming no condition: left as it is",
			in:          jsonRPCError{Code: jsonRPCInternalError, Message: "boom", cause: errors.New("boom")},
			wantMessage: "boom"},
		{name: "a cause naming a condition: identified, the short code ahead",
			in:          jsonRPCError{Code: jsonRPCInternalError, Message: "lint: " + named.Error(), cause: named},
			wantMessage: short + ": lint: " + named.Error(),
			wantData:    &errorData{Code: code, ShortCode: short}},
		{name: "the data it already carries is kept beside the identity",
			in: jsonRPCError{Code: errEntityNotResolvable, Message: named.Error(), cause: named,
				Data: newLockfileErrorData("/w/release.lock", "release.yaml")},
			wantMessage: short + ": " + named.Error(),
			wantData:    &errorData{Lockfile: "/w/release.lock", Release: "release.yaml", Code: code, ShortCode: short}},
		{name: "a message already opening with the short code is not given it twice",
			in:          jsonRPCError{Code: jsonRPCInvalidParams, Message: short + ": " + named.Error(), cause: named},
			wantMessage: short + ": " + named.Error(),
			wantData:    &errorData{Code: code, ShortCode: short}},
		{name: "a bracketed head carrying the short code is not given it again",
			in:          jsonRPCError{Code: errEntityNotResolvable, Message: "[CATALOG_ERROR " + short + "] doc.yaml: fault", cause: named},
			wantMessage: "[CATALOG_ERROR " + short + "] doc.yaml: fault",
			wantData:    &errorData{Code: code, ShortCode: short}},
		{name: "a bracketed head without the short code is given it",
			in:          jsonRPCError{Code: errEntityNotResolvable, Message: "[CATALOG_ERROR] doc.yaml: fault", cause: named},
			wantMessage: short + ": [CATALOG_ERROR] doc.yaml: fault",
			wantData:    &errorData{Code: code, ShortCode: short}},
		{name: "a refusal raised while rendering is identified by its first finding, not by its whole chain",
			in: jsonRPCError{Code: jsonRPCInternalError, Message: "render node app: fault",
				cause: fmt.Errorf("render node app: %w", writescope.ErrWriteLeadsOutOfWorkspace()),
				Data:  newErrorData("/w/recipe.yaml", 0, 0),
				refusal: &renderRefusalReport{message: "render node app: fault", entries: []refusalEntry{
					{file: "/w/recipe.yaml", diagnostic: Diagnostic{ConditionCodes: conditionCodesOf(named)}},
					{file: "/w/other.yaml", diagnostic: Diagnostic{ConditionCodes: conditionCodesOf(writescope.ErrWriteLeadsOutOfWorkspace())}},
				}}},
			wantMessage: short + ": render node app: fault",
			wantData:    &errorData{File: "/w/recipe.yaml", Code: code, ShortCode: short}},
		{name: "a refusal raised while rendering whose first finding names no condition: left as it is",
			in: jsonRPCError{Code: jsonRPCInternalError, Message: "render node app: fault", cause: named,
				refusal: &renderRefusalReport{message: "render node app: fault", entries: []refusalEntry{
					{file: "/w/recipe.yaml", diagnostic: Diagnostic{}},
				}}},
			wantMessage: "render node app: fault"},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			handed := row.in
			kept := handed
			got := conditionRefusal(&handed)
			if got.Code != row.in.Code {
				t.Errorf("code %d; want %d — the identity never moves the JSON-RPC code", got.Code, row.in.Code)
			}
			if got.Message != row.wantMessage {
				t.Errorf("message %q; want %q", got.Message, row.wantMessage)
			}
			switch {
			case row.wantData == nil && got.Data != nil && (got.Data.Code != "" || got.Data.ShortCode != ""):
				t.Errorf("data %+v; want no identity", *got.Data)
			case row.wantData != nil && (got.Data == nil || *got.Data != *row.wantData):
				t.Errorf("data %+v; want %+v", got.Data, *row.wantData)
			}
			if handed.Message != kept.Message || handed.Data != kept.Data {
				t.Errorf("the answer handed in was changed: %+v", handed)
			}
		})
	}
}

// TestEveryRefusalNamingAConditionCarriesItsIdentity drives one refusal of each
// kind through the production dispatch and reads, off the wire, that each keeps
// the JSON-RPC code it always answered with and carries the condition it names in
// its data — code and shortCode — with the short code ahead of its message. The
// controls are refusals naming no condition, which carry neither.
//
// The kinds are the invalid-params code a write the workspace's layout does not
// admit draws, and the one a workspace that cannot be configured draws; the
// entity-not-resolvable code the lockfile gate answers with, for the graph and for
// the preview; and the internal-error code a whole-catalog lint stopped on a link
// answers with.
func TestEveryRefusalNamingAConditionCarriesItsIdentity(t *testing.T) {
	t.Parallel()
	write, read := writescope.ErrWriteLeadsOutOfWorkspace(), readscope.ErrReadLeadsOutOfWorkspace()

	for _, row := range []struct {
		name      string
		arrange   func(t *testing.T, root, outside string)
		method    string
		params    map[string]any
		wantCode  int
		condition error // nil: a refusal naming no condition
	}{
		{name: "the active-Release marker written through a link leading out",
			arrange: func(t *testing.T, root, outside string) {
				plantDir(t, filepath.Join(outside, "aggdir"))
				plantLink(t, filepath.Join(outside, "aggdir"), filepath.Join(root, ".aggrega"))
			},
			method: "aggrega/setActiveRelease", params: map[string]any{"release": "release.yaml"},
			wantCode: jsonRPCInvalidParams, condition: write},
		{name: "the graph over a lockfile leading out",
			arrange: func(t *testing.T, root, outside string) {
				plantFile(t, filepath.Join(outside, "release.lock"), "version: 1\n")
				plantLink(t, filepath.Join(outside, "release.lock"), filepath.Join(root, "release.lock"))
			},
			method: "aggrega/irGraph", params: map[string]any{"release": "release.yaml"},
			wantCode: errEntityNotResolvable, condition: read},
		{name: "the build preview over a lockfile leading out",
			arrange: func(t *testing.T, root, outside string) {
				plantFile(t, filepath.Join(outside, "release.lock"), "version: 1\n")
				plantLink(t, filepath.Join(outside, "release.lock"), filepath.Join(root, "release.lock"))
			},
			method: "aggrega/buildPreview", params: map[string]any{"release": "release.yaml"},
			wantCode: errEntityNotResolvable, condition: read},
		{name: "the whole-catalog lint over a catalog document leading out",
			arrange: func(t *testing.T, root, outside string) {
				plantFile(t, filepath.Join(root, workspaceconfig.ConfigFilename), "workspace:\n  catalog_dirs:\n    - ./cat\n")
				plantFile(t, filepath.Join(outside, "feature.yaml"), "kind: Feature\n")
				plantDir(t, filepath.Join(root, "cat"))
				plantLink(t, filepath.Join(outside, "feature.yaml"), filepath.Join(root, "cat", "linked.yaml"))
			},
			method: "aggrega/lint", params: map[string]any{},
			wantCode: jsonRPCInternalError, condition: read},
		{name: "any configuration-decided request over a configuration file leading out",
			arrange: func(t *testing.T, root, outside string) {
				_ = os.Remove(filepath.Join(root, workspaceconfig.ConfigFilename))
				plantFile(t, filepath.Join(outside, "config.yaml"), "workspace: {}\n")
				plantLink(t, filepath.Join(outside, "config.yaml"), filepath.Join(root, workspaceconfig.ConfigFilename))
			},
			method: "aggrega/irGraph", params: map[string]any{"release": "release.yaml"},
			wantCode: jsonRPCInvalidParams, condition: read},
		{name: "control: a missing lockfile names no condition",
			method: "aggrega/irGraph", params: map[string]any{"release": "release.yaml"},
			wantCode: errLockfileAbsent},
		{name: "control: a Release climbing out of the workspace names no condition",
			method: "aggrega/irGraph", params: map[string]any{"release": "../elsewhere.yaml"},
			wantCode: jsonRPCInvalidParams},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			root, outside := t.TempDir(), t.TempDir()
			writeRelease(t, root, "release.yaml", "release/only")
			plantFile(t, filepath.Join(root, workspaceconfig.ConfigFilename), "workspace: {}\n")
			if row.arrange != nil {
				row.arrange(t, root, outside)
			}
			sess := heldConfigurationSession(t)
			var in, out bytes.Buffer
			in.Write(framedRequest(1, row.method, row.params))
			resp := runOneRequestIn(t, sess, root, &in, &out)

			if resp.Error == nil {
				t.Fatalf("%s answered; want a refusal: %s", row.method, resp.Result)
			}
			if resp.Error.Code != row.wantCode {
				t.Errorf("code %d; want %d — a refusal keeps the code it always answered with", resp.Error.Code, row.wantCode)
			}
			if row.condition == nil {
				if d := resp.Error.Data; d != nil && (d.Code != "" || d.ShortCode != "") {
					t.Errorf("a refusal naming no condition carries an identity: %+v", *d)
				}
				if strings.HasPrefix(resp.Error.Message, "E0") {
					t.Errorf("a refusal naming no condition opens with a short code: %s", resp.Error.Message)
				}
				return
			}
			code, _ := cerrors.CodeOf(row.condition)
			short, _ := cerrors.ShortCodeOf(row.condition)
			if d := resp.Error.Data; d == nil || d.Code != code || d.ShortCode != short {
				t.Errorf("data %+v; want the condition's code %q and short code %q", resp.Error.Data, code, short)
			}
			if !strings.HasPrefix(resp.Error.Message, short+": ") || strings.Count(resp.Error.Message, short) != 1 {
				t.Errorf("the message does not open with the short code, once: %s", resp.Error.Message)
			}
			if sentence, ok := cerrors.MessageOf(row.condition); ok && !strings.Contains(resp.Error.Message, sentence) {
				t.Errorf("the message does not carry the condition's own sentence %q: %s", sentence, resp.Error.Message)
			}
		})
	}
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// The documents a validation without content is driven over. Each carries at
// most one fault, so a row reading one fails on that fault and on nothing else.
const (
	contentProbeConfig           = "offline: true\n"
	contentProbeConfigUnknownKey = "offline: true\nnot_a_config_key: 1\n"
	contentProbeAggregate        = `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: probe
  version: 1.0.0
spec:
  components:
    web:
      type: core/api-server:1.0.0
`
	contentProbeAggregateUnknownKey = contentProbeAggregate + "  not_an_aggregate_key: 1\n"
)

// contentProbeWorkspace lays out a scratch workspace holding a clean workspace
// configuration at its root, a second configuration carrying one key it does not
// declare, a clean Aggregate and an Aggregate carrying one key it does not
// declare, and returns its root.
func contentProbeWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for rel, body := range map[string]string{
		".aggrega-config.yaml":        contentProbeConfig,
		"nested/.aggrega-config.yaml": contentProbeConfigUnknownKey,
		"aggregate.yaml":              contentProbeAggregate,
		"bad/aggregate.yaml":          contentProbeAggregateUnknownKey,
	} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("make the directory for %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return root
}

// validateOnAFreshDaemon sends one aggrega/validate with exactly the params given
// — a map, so a key left out of it is absent on the wire — to a daemon that has
// validated nothing yet, and returns the answer.
func validateOnAFreshDaemon(t *testing.T, root string, params map[string]any) jsonRPCResponse {
	t.Helper()
	return callDaemonMethod(t, newTestServer(t, root, nil, nil), "aggrega/validate", params)
}

// TestValidateWithoutContentAnswersAsTheFileOnDiskDoes drives aggrega/validate
// for each document twice over: once carrying the file's bytes as its content,
// and once carrying no content at all, naming the file relative to the workspace
// root and by absolute path. A request without content asks about the file as it
// is on disk, so the answers must be the same — nothing for a clean document,
// and the one finding a faulted document carries.
//
// The workspace configuration is the document this rests on, and it is
// recognized by its name rather than by anything its bytes carry; an Aggregate
// is recognized by the apiVersion and kind its bytes carry. Both are driven, so
// a validation that read the file for one way of recognizing a document and not
// the other fails here.
func TestValidateWithoutContentAnswersAsTheFileOnDiskDoes(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name string
		file string
		// fault is the one finding the document carries, as a fragment of its
		// message; empty for a clean document.
		fault string
	}{
		{name: "control: the workspace configuration, clean", file: ".aggrega-config.yaml"},
		{
			name:  "a configuration carrying a key it does not declare",
			file:  "nested/.aggrega-config.yaml",
			fault: `unexpected property: "not_a_config_key"`,
		},
		{name: "control: an Aggregate, clean", file: "aggregate.yaml"},
		{
			name:  "an Aggregate carrying a key it does not declare",
			file:  "bad/aggregate.yaml",
			fault: `unexpected property: "not_an_aggregate_key"`,
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			root := contentProbeWorkspace(t)
			onDisk, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(row.file)))
			if err != nil {
				t.Fatalf("read %s: %v", row.file, err)
			}

			var sent validateResult
			decodeDaemonResult(t, validateOnAFreshDaemon(t, root, map[string]any{
				"file": row.file, "content": string(onDisk),
			}), &sent)
			if sent.Checked != checkedDocument {
				t.Fatalf("checked = %q with the content sent; want %q", sent.Checked, checkedDocument)
			}
			switch {
			case row.fault == "" && len(sent.Diagnostics) != 0:
				t.Fatalf("a clean document drew findings with its content sent, so the rows below "+
					"compare against a fault this row does not carry: %+v", sent.Diagnostics)
			case row.fault != "" && (len(sent.Diagnostics) != 1 || !strings.Contains(sent.Diagnostics[0].Message, row.fault)):
				t.Fatalf("with the content sent, want exactly one finding carrying %q; got %+v",
					row.fault, sent.Diagnostics)
			}

			for _, spelling := range []string{row.file, filepath.Join(root, filepath.FromSlash(row.file))} {
				var absent validateResult
				decodeDaemonResult(t, validateOnAFreshDaemon(t, root, map[string]any{"file": spelling}), &absent)
				if absent.Checked != checkedDocument {
					t.Errorf("checked = %q with no content, naming %s; want %q", absent.Checked, spelling, checkedDocument)
				}
				if !reflect.DeepEqual(absent.Diagnostics, sent.Diagnostics) {
					t.Errorf("naming %s with no content answered\n  %+v\nwhere the file's own bytes sent as "+
						"content answered\n  %+v", spelling, absent.Diagnostics, sent.Diagnostics)
				}
			}
		})
	}
}

// TestValidateWithEmptyContentChecksTheEmptyBuffer holds the other half of the
// distinction: content sent empty is a buffer the client holds with nothing in
// it, not a request for the file. Each document on disk answers otherwise than
// an empty buffer does, so a validation that read the file for an empty content
// fails here.
func TestValidateWithEmptyContentChecksTheEmptyBuffer(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name string
		file string
		// want is the fragment the one finding carries; empty where an empty buffer
		// draws none.
		want string
	}{
		{
			// The file is clean, and an empty buffer of a document recognized by its
			// name is an empty configuration.
			name: "an empty buffer of the workspace configuration is reported empty",
			file: ".aggrega-config.yaml",
			want: "document is empty",
		},
		{
			// The file carries a fault, and an empty buffer carries no apiVersion
			// or kind, so it is not an Aggregate and draws nothing.
			name: "an empty buffer of an Aggregate is not read as the file",
			file: "bad/aggregate.yaml",
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			root := contentProbeWorkspace(t)
			var got validateResult
			decodeDaemonResult(t, validateOnAFreshDaemon(t, root, map[string]any{"file": row.file, "content": ""}), &got)
			if row.want == "" {
				if len(got.Diagnostics) != 0 {
					t.Fatalf("an empty buffer drew findings, so the file was read in its place: %+v", got.Diagnostics)
				}
				return
			}
			if len(got.Diagnostics) != 1 || !strings.Contains(got.Diagnostics[0].Message, row.want) {
				t.Fatalf("want exactly one finding carrying %q; got %+v", row.want, got.Diagnostics)
			}
		})
	}
}

// TestValidateWithoutContentRefusesAFileItCannotRead drives a validation without
// content at each kind of file it cannot read, and asserts each is refused with
// the invalid-params code and the sentence naming why, rather than answered as if
// an empty document had been checked. The controls hold the refusals to the read:
// a file inside the workspace is answered, and a file outside it sent WITH its
// content is answered too, since nothing is read for it.
func TestValidateWithoutContentRefusesAFileItCannotRead(t *testing.T) {
	t.Parallel()
	elsewhere := filepath.Join(t.TempDir(), "aggregate.yaml")
	if err := os.WriteFile(elsewhere, []byte(contentProbeAggregate), 0o644); err != nil {
		t.Fatalf("write the document outside the workspace: %v", err)
	}
	rows := []struct {
		name   string
		params map[string]any
		// refusal is the fragment the refusal carries; empty for a row that is
		// answered.
		refusal string
	}{
		{name: "control: a file inside the workspace is read", params: map[string]any{"file": "aggregate.yaml"}},
		{
			name:   "control: a file outside the workspace sent with its content is answered",
			params: map[string]any{"file": elsewhere, "content": contentProbeAggregate},
		},
		{
			name:    "a file that is not there",
			params:  map[string]any{"file": "missing.yaml"},
			refusal: "the document named could not be read",
		},
		{
			name:    "a file outside the workspace, named by absolute path",
			params:  map[string]any{"file": elsewhere},
			refusal: errValidateDocumentOutsideWorkspace.Error(),
		},
		{
			name:    "a file named by a path climbing out of the workspace",
			params:  map[string]any{"file": "../aggregate.yaml"},
			refusal: errValidateDocumentOutsideWorkspace.Error(),
		},
		{
			name:    "no file and no content",
			params:  map[string]any{},
			refusal: errValidateNoDocument.Error(),
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			resp := validateOnAFreshDaemon(t, contentProbeWorkspace(t), row.params)
			if row.refusal == "" {
				var got validateResult
				decodeDaemonResult(t, resp, &got)
				if len(got.Diagnostics) != 0 {
					t.Errorf("a clean document drew findings: %+v", got.Diagnostics)
				}
				return
			}
			if resp.Error == nil {
				t.Fatalf("want the request refused with %q; got result %s", row.refusal, resp.Result)
			}
			if resp.Error.Code != jsonRPCInvalidParams {
				t.Errorf("Code = %d; want %d (invalid params) — the request names no document the daemon reads",
					resp.Error.Code, jsonRPCInvalidParams)
			}
			if !strings.Contains(resp.Error.Message, row.refusal) {
				t.Errorf("the refusal reads %q; want it to carry %q", resp.Error.Message, row.refusal)
			}
		})
	}
}

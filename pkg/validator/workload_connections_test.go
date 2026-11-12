// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package validator_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// connectionWorkloadSlots is the slot set the fold tests read: the Component's
// own `token` slot, and two connections, gpt and nim, wired through one socket
// declaring a `credential` slot that admits environment delivery only and a
// `bundle` slot that admits file delivery only.
func connectionWorkloadSlots() validator.WorkloadSlots {
	own := types.OrderedMap[types.AttachmentSlot]{}
	own.Set("token", types.AttachmentSlot{Inject: []string{"env"}})
	socket := types.OrderedMap[types.AttachmentSlot]{}
	socket.Set("credential", types.AttachmentSlot{Inject: []string{"env"}, Sensitive: true})
	socket.Set("bundle", types.AttachmentSlot{Inject: []string{"file"}})
	connections := types.OrderedMap[validator.WorkloadConnection]{}
	connections.Set("gpt", validator.WorkloadConnection{Socket: "models", Slots: socket})
	connections.Set("nim", validator.WorkloadConnection{Socket: "models", Slots: socket})
	return validator.WorkloadSlots{Own: own, Connections: connections}
}

// foldEntry is one Workload entry a fold row authors: on the named container
// (or the first init container, when init is set), either an attachments entry
// keyed by key or, when envFrom is set, one envFrom entry.
type foldEntry struct {
	init    bool
	envFrom bool
	key     string
	ref     types.AttachmentRef
	source  types.EnvFromSource
}

func workloadOf(entries ...foldEntry) *types.Workload {
	w := &types.Workload{}
	main := types.ContainerSpec{Image: "app:1.0"}
	initC := types.ContainerSpec{Image: "init:1.0"}
	hasInit := false
	for _, e := range entries {
		target := &main
		if e.init {
			target = &initC
			hasInit = true
		}
		if e.envFrom {
			target.EnvFrom = append(target.EnvFrom, e.source)
			continue
		}
		target.Attachments.Set(e.key, e.ref)
	}
	w.Containers.Set("main", main)
	if hasInit {
		w.InitContainers = []types.ContainerSpec{initC}
	}
	return w
}

// TestFoldWorkloadConnections folds each entry form naming a connection onto the
// slot name the compiler composes for that connection's attachment, and refuses,
// naming what the entry named, an entry naming a connection the component does
// not draw, a slot the socket does not declare, or a connection's slot by its
// composed name.
//
// Each refusal row carries one fault, and the row naming the Component's own
// slot is the control: the fold leaves it as written and refuses nothing.
func TestFoldWorkloadConnections(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		entries []foldEntry
		// wantFrom is each folded attachments entry's slot, keyed by the
		// container path and entry key; wantSecret each folded envFrom entry's.
		wantFrom   map[string]string
		wantSecret map[string]string
		// findings are the refusal's field paths, each with the fragments its
		// constraint must name; nil means the fold refuses nothing.
		findings map[string][]string
	}{
		{
			name:     "the Component's own slot is left as written",
			entries:  []foldEntry{{key: "TOKEN", ref: types.AttachmentRef{From: "token", As: "env"}}},
			wantFrom: map[string]string{"main/TOKEN": "token"},
		},
		{
			name: "an attachments entry naming a connection folds onto the composed slot",
			entries: []foldEntry{
				{key: "MODEL_KEY_GPT", ref: types.AttachmentRef{From: "credential", As: "env", Connection: "gpt"}},
				{key: "MODEL_KEY_NIM", ref: types.AttachmentRef{From: "credential", As: "env", Connection: "nim"}},
			},
			wantFrom: map[string]string{"main/MODEL_KEY_GPT": "gpt.credential", "main/MODEL_KEY_NIM": "nim.credential"},
		},
		{
			name:     "an init container's entry folds the same way",
			entries:  []foldEntry{{init: true, key: "KEY", ref: types.AttachmentRef{From: "credential", As: "env", Connection: "nim"}}},
			wantFrom: map[string]string{"init/KEY": "nim.credential"},
		},
		{
			name:       "an envFrom entry naming a connection folds its secret onto the composed slot",
			entries:    []foldEntry{{envFrom: true, source: types.EnvFromSource{Secret: "credential", Connection: "gpt", Prefix: "GPT_"}}},
			wantSecret: map[string]string{"main/0": "gpt.credential"},
		},
		{
			name:     "a connection the component does not draw is refused naming the connection and the slot",
			entries:  []foldEntry{{key: "KEY", ref: types.AttachmentRef{From: "credential", As: "env", Connection: "claude"}}},
			findings: map[string][]string{".containers.main.attachments.KEY.connection": {`"claude"`, `"credential"`, "gpt, nim"}},
		},
		{
			name:     "a slot the connection's socket does not declare is refused naming the slot, the connection and the socket",
			entries:  []foldEntry{{key: "KEY", ref: types.AttachmentRef{From: "token", As: "env", Connection: "gpt"}}},
			findings: map[string][]string{".containers.main.attachments.KEY.from": {`"token"`, `"gpt"`, `"models"`, "credential, bundle"}},
		},
		{
			name:     "an envFrom entry naming a connection the component does not draw is refused",
			entries:  []foldEntry{{envFrom: true, source: types.EnvFromSource{Secret: "credential", Connection: "claude"}}},
			findings: map[string][]string{".containers.main.envFrom[0].connection": {`"claude"`, `"credential"`}},
		},
		{
			name:     "an envFrom entry naming a slot its socket does not declare is refused",
			entries:  []foldEntry{{envFrom: true, source: types.EnvFromSource{Secret: "token", Connection: "nim"}}},
			findings: map[string][]string{".containers.main.envFrom[0].secret": {`"token"`, `"nim"`, `"models"`}},
		},
		{
			name:     "a connection's slot named by its composed name is refused naming the connection and the slot",
			entries:  []foldEntry{{key: "KEY", ref: types.AttachmentRef{From: "gpt.credential", As: "env"}}},
			findings: map[string][]string{".containers.main.attachments.KEY.from": {`"gpt.credential"`, `"credential"`, `"gpt"`}},
		},
		{
			name:     "an envFrom entry naming a connection's slot by its composed name is refused",
			entries:  []foldEntry{{envFrom: true, source: types.EnvFromSource{Secret: "nim.credential"}}},
			findings: map[string][]string{".containers.main.envFrom[0].secret": {`"nim.credential"`, `"credential"`, `"nim"`}},
		},
		{
			name: "every refused entry is reported in one grouped refusal",
			entries: []foldEntry{
				{key: "A", ref: types.AttachmentRef{From: "credential", As: "env", Connection: "claude"}},
				{key: "B", ref: types.AttachmentRef{From: "token", As: "env", Connection: "gpt"}},
				{key: "C", ref: types.AttachmentRef{From: "token", As: "env"}},
			},
			findings: map[string][]string{
				".containers.main.attachments.A.connection": {`"claude"`},
				".containers.main.attachments.B.from":       {`"token"`, `"gpt"`},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := workloadOf(tc.entries...)
			checked, err := validator.FoldWorkloadConnections(w, connectionWorkloadSlots(), "recipe.yaml", "router", "1.0.0")
			if tc.findings == nil {
				if err != nil {
					t.Fatalf("the fold refused an entry it must accept: %v", err)
				}
			} else {
				if !errors.Is(err, validator.ErrWorkloadFeaturesSchema()) {
					t.Fatalf("the fold must refuse with the slot-declaration sentinel; got %v", err)
				}
				var wve *validator.WorkloadValidationError
				if !errors.As(err, &wve) {
					t.Fatalf("the refusal must be a grouped workload report; got %T", err)
				}
				got := map[string]string{}
				for _, f := range wve.Findings {
					got[f.FieldPath] = f.Constraint + " — " + f.Suggestion
				}
				if len(got) != len(tc.findings) {
					t.Fatalf("findings at %v; want exactly %v", keysOf(got), keysOf(tc.findings))
				}
				for path, fragments := range tc.findings {
					text, ok := got[path]
					if !ok {
						t.Fatalf("no finding at %s; findings at %v", path, keysOf(got))
					}
					for _, fragment := range fragments {
						if !strings.Contains(text, fragment) {
							t.Errorf("finding at %s does not name %s: %s", path, fragment, text)
						}
					}
				}
				return
			}

			for where, want := range tc.wantFrom {
				container, key, _ := strings.Cut(where, "/")
				spec := w.InitContainers
				var c types.ContainerSpec
				if container == "init" {
					c = spec[0]
				} else {
					c, _ = w.Containers.Get(container)
				}
				ref, _ := c.Attachments.Get(key)
				if ref.From != want || ref.Connection != "" {
					t.Errorf("%s folded to from=%q connection=%q; want from=%q and no connection", where, ref.From, ref.Connection, want)
				}
			}
			for where, want := range tc.wantSecret {
				c, _ := w.Containers.Get("main")
				ef := c.EnvFrom[0]
				if ef.Secret != want || ef.Connection != "" || ef.Prefix != "GPT_" {
					t.Errorf("%s folded to %+v; want secret %q, no connection, the prefix kept", where, ef, want)
				}
			}

			// The set the folded Workload is checked against is the Component's own
			// slots, then each connection's socket slots under their composed names,
			// each carrying the socket's own declaration.
			wantChecked := []string{"token", "gpt.credential", "gpt.bundle", "nim.credential", "nim.bundle"}
			if got := checked.Keys(); !reflect.DeepEqual(got, wantChecked) {
				t.Errorf("checked slots = %v; want %v", got, wantChecked)
			}
			if slot, _ := checked.Get("gpt.bundle"); !reflect.DeepEqual(slot.Inject, []string{"file"}) {
				t.Errorf("the composed slot must carry the socket's declaration; got inject %v", slot.Inject)
			}
		})
	}
}

// TestFoldedWorkloadIsHeldToTheSocketSlotsAllowlist runs the slot check over the
// folded Workload and the set the fold returned, so an entry that named a
// connection is held to the delivery allowlist its socket's slot declares by the
// check every other entry goes through. The env row is the control.
func TestFoldedWorkloadIsHeldToTheSocketSlotsAllowlist(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		ref     types.AttachmentRef
		refused bool
	}{
		{name: "environment delivery the credential slot allowlists", ref: types.AttachmentRef{From: "credential", As: "env", Connection: "gpt"}},
		{name: "file delivery the credential slot does not allowlist", ref: types.AttachmentRef{From: "credential", As: "file", Path: "/run/key", Connection: "gpt"}, refused: true},
		{name: "environment delivery the bundle slot does not allowlist", ref: types.AttachmentRef{From: "bundle", As: "env", Connection: "nim"}, refused: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := workloadOf(foldEntry{key: "KEY", ref: tc.ref})
			checked, err := validator.FoldWorkloadConnections(w, connectionWorkloadSlots(), "", "", "")
			if err != nil {
				t.Fatalf("fold: %v", err)
			}
			err = validator.ValidateWorkloadAttachments(w, checked, "", "", "")
			if tc.refused {
				if !errors.Is(err, validator.ErrWorkloadFeaturesSchema()) || !strings.Contains(err.Error(), "is not permitted by slot") {
					t.Fatalf("the socket slot's allowlist must refuse this delivery; got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("the socket slot's allowlist must admit this delivery; got %v", err)
			}
		})
	}
}

// TestFoldWorkloadConnections_NoWorkloadReturnsTheSet pins that a Recipe
// declaring no Workload still answers the slot set, with nothing refused.
func TestFoldWorkloadConnections_NoWorkloadReturnsTheSet(t *testing.T) {
	t.Parallel()
	checked, err := validator.FoldWorkloadConnections(nil, connectionWorkloadSlots(), "", "", "")
	if err != nil {
		t.Fatalf("a nil Workload refuses nothing; got %v", err)
	}
	if checked.Len() != 5 {
		t.Errorf("checked slots = %v; want the own slot and both connections' two slots", checked.Keys())
	}
}

func keysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

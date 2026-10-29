// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// A service naming the components whose networks it joins.
//
// The merge places every contributing node on one network and writes that
// network's name, which is the merge's own encoding of the node's identity
// (deriveNodeNetworkName). A service a fragment writes cannot spell those names,
// so it names components instead, under the service extension member
// x-aggrega-networks-of, and the merge looks each one up in the placement it
// already computed. The lookup reads no id's meaning: an id is a key into that
// placement, and one the placement does not hold is a reference to nothing in
// this file.
//
// This file carries no build tag: it compiles for the host and for the wasip1
// guest alike, so it imports no host-only capability.

package composecore

import (
	"fmt"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// networksOfKey is the service extension member listing the components whose
// networks the service joins. It is the one extension member the merge reads,
// and it never reaches the assembled file.
const networksOfKey = "x-aggrega-networks-of"

// networksOfFindings collects what the resolution of every service's
// x-aggrega-networks-of member found wrong, across the whole dispatch group, so
// the merge halts once naming all of it rather than at the first service.
//
// The two lists halt under different conditions, which is why they are kept
// apart: a member the merge cannot read as a list of ids is a construct it has
// no expression for, while an id naming no component of this file is a
// reference to something the file does not hold.
type networksOfFindings struct {
	// unreadable holds one entry per service whose member is not a list of
	// strings, or which also writes network_mode.
	unreadable []string

	// unresolved holds one entry per (service, id) whose id names no component
	// contributing to this file.
	unresolved []string
}

// resolveNetworksOf takes the x-aggrega-networks-of member off svc, when it
// carries one, and adds to svc.Networks the network of every component the
// member names, through networkNameByNode — the network each node contributing
// to this file is placed on, keyed by node id. The added names are deduplicated
// and sorted, and come after any network the fragment wrote for the service and
// before the service's own network own, which the caller appends last; own
// itself is never added here, so it stays last.
//
// svcName names the service in the findings; it identifies the service alone,
// because the merge refuses a service name two contributors share. A member that
// is not a list of strings, a member beside a network_mode, and an id
// networkNameByNode does not hold are recorded in f, and svc's networks are then
// left as they were; the member is taken off either way.
func resolveNetworksOf(svc *Service, svcName, own string, networkNameByNode map[string]string, f *networksOfFindings) {
	value, carried := svc.Extensions.take(networksOfKey)
	if !carried {
		return
	}
	ids, isList := stringList(value)
	if !isList {
		f.unreadable = append(f.unreadable, fmt.Sprintf(
			"service %q writes %s as something other than a list of strings; it takes a list of component ids",
			svcName, networksOfKey))
		return
	}
	if svc.NetworkMode != "" {
		f.unreadable = append(f.unreadable, fmt.Sprintf(
			"service %q writes %s beside network_mode %q, and Compose does not let a service with a network_mode join networks; remove one of the two",
			svcName, networksOfKey, svc.NetworkMode))
		return
	}

	seenID := map[string]struct{}{}
	seenName := map[string]struct{}{}
	var names []string
	missing := false
	for _, id := range ids {
		if _, dup := seenID[id]; dup {
			continue
		}
		seenID[id] = struct{}{}
		name, placed := networkNameByNode[id]
		if !placed {
			f.unresolved = append(f.unresolved, fmt.Sprintf("service %q names %q in %s", svcName, id, networksOfKey))
			missing = true
			continue
		}
		if name == own {
			continue
		}
		if _, dup := seenName[name]; dup {
			continue
		}
		seenName[name] = struct{}{}
		names = append(names, name)
	}
	if missing {
		return
	}
	sort.Strings(names)
	for _, name := range names {
		svc.Networks = appendUnique(svc.Networks, name)
	}
}

// stringList reads value as a list of strings — a sequence each of whose items
// is a string scalar — and reports whether it is one. An empty sequence is a
// list naming nothing.
func stringList(value *yaml.Node) ([]string, bool) {
	if value == nil || value.Kind != yaml.SequenceNode {
		return nil, false
	}
	out := make([]string, 0, len(value.Content))
	for _, item := range value.Content {
		if item.Kind != yaml.ScalarNode || item.Tag != "!!str" {
			return nil, false
		}
		out = append(out, item.Value)
	}
	return out, true
}

// halt returns the merge's halt for what f holds, or nil when it holds nothing.
// An unreadable member halts ahead of an unresolved id, because the ids of a
// member that could not be read were never looked up. assembledAt is the path
// the file is assembled at, which the unresolved halt names.
func (f networksOfFindings) halt(assembledAt string) error {
	if len(f.unreadable) > 0 {
		return cerrors.Wrap(plugincondition.ErrPluginCannotExpressInput(),
			cerrors.TranslationError, cerrors.SourceLocation{},
			fmt.Sprintf("compose AssembleBoundary: %s", strings.Join(f.unreadable, "; ")))
	}
	if len(f.unresolved) > 0 {
		return cerrors.Wrap(plugincondition.ErrPluginRequiredInputUnresolved(),
			cerrors.TranslationError, cerrors.SourceLocation{},
			fmt.Sprintf("compose AssembleBoundary: %s; no component by that id is assembled into the %s at %q, so it has no network in this file: name only components assembled into the same file, or remove the id",
				strings.Join(f.unresolved, "; "), AssembledOutputFile, assembledAt))
	}
	return nil
}

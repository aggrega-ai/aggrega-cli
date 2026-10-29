// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/quantity"
)

// checkEmittedQuantities checks every resource quantity the synthesis copies
// into the manifests against the quantity grammar, and halts on the first one
// outside it. The manifests carry each quantity verbatim, so a value the
// cluster's own parser refuses would otherwise build cleanly and fail only when
// the chart is installed.
//
// The walk covers exactly the quantities the translation emits, in the order
// the workload declares them:
//
//   - each container's and each init container's resources.requests and
//     resources.limits — cpu, memory, and every extended-resource value;
//   - each volume's ephemeral.sizeLimit, persistent.size and
//     persistentTemplate.size.
//
// A cpu, memory or sizeLimit left empty is omitted from the manifests rather
// than emitted, so it is not checked; an extended-resource value and a
// persistent size are emitted even when empty, so an empty one is refused. The
// check runs on the workload the plugin was given, which is already evaluated,
// so a quantity that arrived through a template is judged on the value it
// rendered to. It judges the grammar only — never whether a cluster can
// schedule the value.
//
// withhold is the input's marking over the workload as a whole: the template that
// produced it reads a value an author declared sensitive. With it set, a refusal
// carries no part of the value it refused (see [quantity.Withhold]); the field
// path still names where the value sits.
func checkEmittedQuantities(w *types.Workload, nodeID string, withhold bool) error {
	for name, c := range w.Containers.All() {
		if err := checkResourceQuantities(c.Resources, "containers."+name+".resources", nodeID, withhold); err != nil {
			return err
		}
	}
	for i, c := range w.InitContainers {
		if err := checkResourceQuantities(c.Resources, fmt.Sprintf("initContainers[%d].resources", i), nodeID, withhold); err != nil {
			return err
		}
	}
	for name, v := range w.Volumes.All() {
		path := "volumes." + name
		switch {
		case v.Ephemeral != nil && v.Ephemeral.SizeLimit != "":
			if err := checkQuantity(v.Ephemeral.SizeLimit, path+".ephemeral.sizeLimit", nodeID, withhold); err != nil {
				return err
			}
		case v.Persistent != nil:
			if err := checkQuantity(v.Persistent.Size, path+".persistent.size", nodeID, withhold); err != nil {
				return err
			}
		case v.PersistentTemplate != nil:
			if err := checkQuantity(v.PersistentTemplate.Size, path+".persistentTemplate.size", nodeID, withhold); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkResourceQuantities checks one container's requests and limits, each in
// the order translateResources emits them: cpu, memory, then the extended
// resources in author order.
func checkResourceQuantities(r *types.ResourceSpec, resourcesPath, nodeID string, withhold bool) error {
	if r == nil {
		return nil
	}
	lists := []struct {
		list *types.ResourceList
		path string
	}{
		{r.Requests, resourcesPath + ".requests"},
		{r.Limits, resourcesPath + ".limits"},
	}
	for _, l := range lists {
		if l.list == nil {
			continue
		}
		if l.list.CPU != "" {
			if err := checkQuantity(l.list.CPU, l.path+".cpu", nodeID, withhold); err != nil {
				return err
			}
		}
		if l.list.Memory != "" {
			if err := checkQuantity(l.list.Memory, l.path+".memory", nodeID, withhold); err != nil {
				return err
			}
		}
		for key, value := range l.list.Extended.All() {
			if err := checkQuantity(value, l.path+"."+key, nodeID, withhold); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkQuantity refuses a value outside the quantity grammar under the
// value-not-representable condition — the manifests' quantity fields have no
// representation for it — with a sentence naming the node, the authored field
// path and the value, and quoting the grammar. With withhold set the sentence
// carries no part of the value in its place.
func checkQuantity(value, path, nodeID string, withhold bool) error {
	if _, err := quantity.Parse(value); err != nil {
		if withhold {
			err = quantity.Withhold(err)
		}
		return cerrors.Wrap(plugincondition.ErrPluginValueNotRepresentable(),
			cerrors.TranslationError, cerrors.SourceLocation{File: nodeID},
			fmt.Sprintf("helm: node %q: %s: %v; %s", nodeID, path, err, quantity.Shape))
	}
	return nil
}

// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package composecore

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/quantity"
)

// Quantity conversion for the compose deploy.resources block. The grammar a
// quantity is read in is not decided here: it is the shared quantity package's,
// the same grammar the Kubernetes-shaped target checks its own output against,
// so the two targets agree on what is a quantity. What is decided here is only
// whether compose can hold a parsed value, and that is asked of the exact value
// rather than of the text: a value compose cannot hold exactly halts rather than
// being rounded or emitted wrong. Both refusals say which of the two the value
// failed — not a quantity at all, or a quantity compose has no exact form for —
// because the repair differs.

// nanosPerCore is the count of the finest cpus unit in one core. A compose cpus
// value is emitted to at most nine decimal places, the finest precision a
// Kubernetes quantity itself keeps, so a cpu quantity finer than that has no
// exact compose form.
const nanosPerCore = 1_000_000_000

// quantityUnconvertible closes both quantity refusals, whole in one literal, so
// one sentence matches every refusal a compose resources quantity raises
// whichever of the two failures it met.
const quantityUnconvertible = "resources quantity cannot be converted to a compose deploy.resources value"

// translateComposeResources maps a Workload container's resource requests and
// limits to a compose deploy block. Kubernetes requests become compose
// reservations (guaranteed minimum) and limits become compose limits (hard
// cap). Returns nil when the spec carries no convertible quantity.
//
// resourcesPath is the authored path to the container's resources block —
// `containers.<name>.resources` or `initContainers[<i>].resources` — and reaches
// the refusal sentences and nothing else. withhold is the input's marking over the
// workload as a whole — its template reads a value an author declared sensitive —
// and with it set neither refusal quotes the value it refused.
func translateComposeResources(r *types.ResourceSpec, resourcesPath, nodeID string, withhold bool) (*Deploy, error) {
	if r == nil {
		return nil, nil
	}
	limits, err := composeQuantities(r.Limits, resourcesPath+".limits", "limit", nodeID, withhold)
	if err != nil {
		return nil, err
	}
	reservations, err := composeQuantities(r.Requests, resourcesPath+".requests", "request", nodeID, withhold)
	if err != nil {
		return nil, err
	}
	if limits == nil && reservations == nil {
		return nil, nil
	}
	return &Deploy{Resources: &DeployResources{Limits: limits, Reservations: reservations}}, nil
}

// composeQuantities converts one Kubernetes ResourceList (cpu/memory) into the
// compose cpus/memory pair. Returns nil when both fields are empty. listPath is
// the authored path to the list and role names it for a reader — "request" or
// "limit".
func composeQuantities(rl *types.ResourceList, listPath, role, nodeID string, withhold bool) (*ResourceQuantities, error) {
	if rl == nil {
		return nil, nil
	}
	out := &ResourceQuantities{}
	if rl.CPU != "" {
		cpus, err := convertQuantity(rl.CPU, composeCPUs, "cpu", role, listPath+".cpu", nodeID,
			"a compose cpus value is a core count from 0 to 9223372036.854775807 with at most nine decimal places", withhold)
		if err != nil {
			return nil, err
		}
		out.CPUs = cpus
	}
	if rl.Memory != "" {
		mem, err := convertQuantity(rl.Memory, composeMemory, "memory", role, listPath+".memory", nodeID,
			"a compose memory value is a whole number of bytes from 0 to 9223372036854775807", withhold)
		if err != nil {
			return nil, err
		}
		out.Memory = mem
	}
	if out.CPUs == "" && out.Memory == "" {
		return nil, nil
	}
	return out, nil
}

// convertQuantity parses one authored quantity and converts it with convert,
// returning the compose form. It raises one of two refusals, both under the
// value-not-representable condition and both naming the resource, the role,
// the authored path and the value: one when the value is not a quantity at all,
// and one when it is a quantity compose has no exact form for — the sentence
// then states compose's range, which is what the author repairs against.
//
// With withhold set neither sentence quotes the value. The first carries the
// grammar's own withheld refusal ([quantity.Withhold]). The second cannot be
// asked again of the placeholder — the placeholder is not a quantity at all, so it
// would fail the other way — so it says which of the two failures this was, that
// the value is withheld, and still states compose's range.
func convertQuantity(value string, convert func(quantity.Quantity) (string, bool), resource, role, path, nodeID, composeRange string, withhold bool) (string, error) {
	q, err := quantity.Parse(value)
	if err != nil {
		if withhold {
			err = quantity.Withhold(err)
		}
		return "", cerrors.Wrap(wasm.ErrPluginValueNotRepresentable(),
			cerrors.TranslationError, cerrors.SourceLocation{File: nodeID},
			fmt.Sprintf("compose: the %s %s at %s: %v; %s: %s",
				resource, role, path, err, quantity.Shape, quantityUnconvertible))
	}
	converted, ok := convert(q)
	if !ok && withhold {
		return "", cerrors.Wrap(wasm.ErrPluginValueNotRepresentable(),
			cerrors.TranslationError, cerrors.SourceLocation{File: nodeID},
			fmt.Sprintf("compose: the %s %s at %s is a quantity compose cannot represent exactly; %s; %s: %s",
				resource, role, path, quantity.ValueWithheld, composeRange, quantityUnconvertible))
	}
	if !ok {
		return "", cerrors.Wrap(wasm.ErrPluginValueNotRepresentable(),
			cerrors.TranslationError, cerrors.SourceLocation{File: nodeID},
			fmt.Sprintf("compose: the %s %s at %s is %q, a quantity compose cannot represent exactly; %s: %s",
				resource, role, path, value, composeRange, quantityUnconvertible))
	}
	return converted, nil
}

// composeCPUs converts a parsed cpu quantity to a compose cpus value: a decimal
// core count. It reports false for a negative value and for one that is not a
// whole number of billionths of a core within an int64. A value written as a
// bare unsigned number ("2", "0.5") is already in that form and is emitted as
// written; any other form — a millicpu, a suffix, an exponent, a sign — is
// emitted as its minimal decimal (500m becomes 0.5, 2000m becomes 2).
func composeCPUs(q quantity.Quantity) (string, bool) {
	nanos, ok := q.ExactInt64(-9)
	if !ok || nanos < 0 {
		return "", false
	}
	if q.Plain() {
		return q.String(), true
	}
	return formatNanocores(nanos), true
}

// formatNanocores renders a count of billionths of a core as a minimal decimal
// core count using integer math only (no float), so output is deterministic.
func formatNanocores(nanos int64) string {
	whole := nanos / nanosPerCore
	frac := nanos % nanosPerCore
	if frac == 0 {
		return strconv.FormatInt(whole, 10)
	}
	return strings.TrimRight(fmt.Sprintf("%d.%09d", whole, frac), "0")
}

// composeMemory converts a parsed memory quantity to an exact byte count. It
// reports false for a negative value, for a value that is not a whole number of
// bytes (1500m, 0.5), and for one past the int64 range (8Ei).
func composeMemory(q quantity.Quantity) (string, bool) {
	bytes, ok := q.ExactInt64(0)
	if !ok || bytes < 0 {
		return "", false
	}
	return strconv.FormatInt(bytes, 10), true
}

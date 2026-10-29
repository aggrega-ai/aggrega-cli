// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package composecore

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
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

// nvidiaGPUResource is the one extended resource key the compose target
// translates: a container's NVIDIA GPU count, which compose reserves as a device.
// Every other extended key — another vendor's GPU key included — has no
// translation here and is dropped with a warning (extendedResourceDiagnostics).
const nvidiaGPUResource = "nvidia.com/gpu"

// The device entry a container's nvidia.com/gpu count is written as: the NVIDIA
// driver, and the gpu capability Compose requires on every device entry.
const (
	nvidiaDeviceDriver     = "nvidia"
	nvidiaDeviceCapability = "gpu"
)

// translateComposeResources maps a Workload container's resource requests and
// limits to a compose deploy block. Kubernetes requests become compose
// reservations (guaranteed minimum) and limits become compose limits (hard
// cap), and a nvidia.com/gpu count becomes a device entry on the reservations
// (nvidiaDeviceReservations). Returns nil when the spec carries nothing to
// write.
//
// resourcesPath is the authored path to the container's resources block —
// `containers.<name>.resources` or `initContainers[<i>].resources` — and reaches
// the refusal sentences and nothing else. withhold is the input's marking over the
// workload as a whole — its template reads a value an author declared sensitive —
// and with it set no refusal quotes the value it refused.
func translateComposeResources(r *types.ResourceSpec, resourcesPath, nodeID string, withhold bool) (*Deploy, error) {
	if r == nil {
		return nil, nil
	}
	limits, err := composeQuantities(r.Limits, resourcesPath+".limits", "limit", nodeID, withhold)
	if err != nil {
		return nil, err
	}
	requests, err := composeQuantities(r.Requests, resourcesPath+".requests", "request", nodeID, withhold)
	if err != nil {
		return nil, err
	}
	devices, err := nvidiaDeviceReservations(r, resourcesPath, nodeID, withhold)
	if err != nil {
		return nil, err
	}

	var reservations *ResourceReservations
	if requests != nil || len(devices) > 0 {
		reservations = &ResourceReservations{Devices: devices}
		if requests != nil {
			reservations.ResourceQuantities = *requests
		}
	}
	if limits == nil && reservations == nil {
		return nil, nil
	}
	return &Deploy{Resources: &DeployResources{Limits: limits, Reservations: reservations}}, nil
}

// nvidiaDeviceReservations translates a container's nvidia.com/gpu count into
// the compose device entry that reserves that many NVIDIA GPUs for its service.
// It returns no entry when the container asks for no GPU, which leaves the
// service exactly as a container without the key renders.
//
// Which count is read. The key may be authored under limits, requests or both,
// and the count Kubernetes schedules for an extended resource is the limit: a
// request left out takes the limit's value, and one written beside it must equal
// it. So the limit is translated where it is authored and the request otherwise,
// and a request and a limit that ask for different counts halt naming both,
// because compose reserves one count and either choice would be a guess.
//
// A count of zero asks for no GPU and writes no entry. That is not only the
// faithful reading: an entry carrying no count reserves every GPU on the host,
// so a zero must never reach the entry as an omitted count. A count that is not
// a whole number of devices — a fraction, a milli quantity, a negative amount,
// or a value that is not a quantity at all — halts naming the container's path
// and the key, rather than writing a rounded or invented count.
func nvidiaDeviceReservations(r *types.ResourceSpec, resourcesPath, nodeID string, withhold bool) ([]DeviceRequest, error) {
	limit, hasLimit := extendedQuantity(r.Limits, nvidiaGPUResource)
	request, hasRequest := extendedQuantity(r.Requests, nvidiaGPUResource)
	if !hasLimit && !hasRequest {
		return nil, nil
	}

	limitPath := resourcesPath + ".limits." + nvidiaGPUResource
	requestPath := resourcesPath + ".requests." + nvidiaGPUResource

	var count int64
	if hasLimit {
		n, err := deviceCount(limit, "limit", limitPath, nodeID, withhold)
		if err != nil {
			return nil, err
		}
		count = n
	}
	if hasRequest {
		n, err := deviceCount(request, "request", requestPath, nodeID, withhold)
		if err != nil {
			return nil, err
		}
		if hasLimit && n != count {
			return nil, deviceCountDisagreement(requestPath, limitPath, n, count, nodeID, withhold)
		}
		count = n
	}

	if count == 0 {
		return nil, nil
	}
	return []DeviceRequest{{
		Driver:       nvidiaDeviceDriver,
		Count:        count,
		Capabilities: []string{nvidiaDeviceCapability},
	}}, nil
}

// extendedQuantity returns the value an extended resource key carries on one
// resource list, and whether the list authors the key at all.
func extendedQuantity(rl *types.ResourceList, key string) (string, bool) {
	if rl == nil {
		return "", false
	}
	return rl.Extended.Get(key)
}

// deviceCount reads one authored GPU quantity as a whole device count, through
// the same quantity grammar and the same two refusals every other compose
// resources quantity meets.
func deviceCount(value, role, path, nodeID string, withhold bool) (int64, error) {
	converted, err := convertQuantity(value, composeDeviceCount, nvidiaGPUResource, role, path, nodeID,
		"a compose device count is a whole number of devices from 0 to 9223372036854775807", withhold)
	if err != nil {
		return 0, err
	}
	n, err := strconv.ParseInt(converted, 10, 64)
	if err != nil {
		// composeDeviceCount emits a base-10 int64 or refuses, so this is a broken
		// conversion rather than an authored value.
		return 0, cerrors.Wrap(plugincondition.ErrPluginValueNotRepresentable(),
			cerrors.TranslationError, cerrors.SourceLocation{File: nodeID},
			fmt.Sprintf("compose: the %s %s at %s did not convert to a device count: %v: %s",
				nvidiaGPUResource, role, path, err, quantityUnconvertible))
	}
	return n, nil
}

// deviceCountDisagreement is the halt for a container whose nvidia.com/gpu
// request and limit ask for different counts. With withhold set the two counts
// are left out, as every other compose resources refusal leaves its value out.
func deviceCountDisagreement(requestPath, limitPath string, request, limit int64, nodeID string, withhold bool) error {
	counts := fmt.Sprintf(" (%d and %d)", request, limit)
	if withhold {
		counts = "; " + quantity.ValueWithheld
	}
	return cerrors.Wrap(plugincondition.ErrPluginValueNotRepresentable(),
		cerrors.TranslationError, cerrors.SourceLocation{File: nodeID},
		fmt.Sprintf("compose: the %s request at %s and the limit at %s ask for different counts%s; compose reserves one device count for a service, so write the same count in both or only the limit: %s",
			nvidiaGPUResource, requestPath, limitPath, counts, quantityUnconvertible))
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
		return "", cerrors.Wrap(plugincondition.ErrPluginValueNotRepresentable(),
			cerrors.TranslationError, cerrors.SourceLocation{File: nodeID},
			fmt.Sprintf("compose: the %s %s at %s: %v; %s: %s",
				resource, role, path, err, quantity.Shape, quantityUnconvertible))
	}
	converted, ok := convert(q)
	if !ok && withhold {
		return "", cerrors.Wrap(plugincondition.ErrPluginValueNotRepresentable(),
			cerrors.TranslationError, cerrors.SourceLocation{File: nodeID},
			fmt.Sprintf("compose: the %s %s at %s is a quantity compose cannot represent exactly; %s; %s: %s",
				resource, role, path, quantity.ValueWithheld, composeRange, quantityUnconvertible))
	}
	if !ok {
		return "", cerrors.Wrap(plugincondition.ErrPluginValueNotRepresentable(),
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
	return exactWholeNumber(q)
}

// composeDeviceCount converts a parsed GPU quantity to a whole device count. It
// reports false for a negative value, for a value that is not a whole number of
// devices (0.5, 500m), and for one past the int64 range.
func composeDeviceCount(q quantity.Quantity) (string, bool) {
	return exactWholeNumber(q)
}

// exactWholeNumber renders a parsed quantity as the non-negative whole number it
// is exactly equal to, in base 10, and reports false when it is no such number.
// A byte count and a device count are both read this way; each keeps its own
// converter so its refusal states its own range.
func exactWholeNumber(q quantity.Quantity) (string, bool) {
	n, ok := q.ExactInt64(0)
	if !ok || n < 0 {
		return "", false
	}
	return strconv.FormatInt(n, 10), true
}

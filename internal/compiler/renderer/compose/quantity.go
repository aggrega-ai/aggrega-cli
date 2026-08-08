// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package compose

import (
	"fmt"
	"strconv"
	"strings"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// Quantity translation for the compose deploy.resources block. This is the
// single home for converting Kubernetes-shaped resource quantities into the
// compose forms; helm passes the quantities through verbatim (its target IS
// Kubernetes) and needs no translation. The translator is deliberately
// dependency-free — it does not pull the Kubernetes quantity library into the
// wasip1 guest that compiles this package — and loud-halts on any quantity it
// cannot convert exactly rather than round or emit a wrong value.

// translateComposeResources maps a Workload container's resource requests and
// limits to a compose deploy block. Kubernetes requests become compose
// reservations (guaranteed minimum) and limits become compose limits (hard
// cap). Returns nil when the spec carries no convertible quantity. nodeID
// attributes any halt to the offending node.
func translateComposeResources(r *types.ResourceSpec, nodeID string) (*Deploy, error) {
	if r == nil {
		return nil, nil
	}
	limits, err := composeQuantities(r.Limits, nodeID)
	if err != nil {
		return nil, err
	}
	reservations, err := composeQuantities(r.Requests, nodeID)
	if err != nil {
		return nil, err
	}
	if limits == nil && reservations == nil {
		return nil, nil
	}
	return &Deploy{Resources: &DeployResources{Limits: limits, Reservations: reservations}}, nil
}

// composeQuantities converts one Kubernetes ResourceList (cpu/memory) into the
// compose cpus/memory pair. Returns nil when both fields are empty.
func composeQuantities(rl *types.ResourceList, nodeID string) (*ResourceQuantities, error) {
	if rl == nil {
		return nil, nil
	}
	out := &ResourceQuantities{}
	if rl.CPU != "" {
		cpus, err := k8sCPUToComposeCPUs(rl.CPU)
		if err != nil {
			return nil, cerrors.New(cerrors.TranslationError, cerrors.SourceLocation{File: nodeID},
				fmt.Sprintf("compose: cannot convert cpu quantity %q to a deploy.resources cpus value; use a millicpu (e.g. 500m) or a plain core count (e.g. 0.5, 2)", rl.CPU),
				nil, ErrUnconvertibleComposeQuantity())
		}
		out.CPUs = cpus
	}
	if rl.Memory != "" {
		mem, err := k8sMemToComposeBytes(rl.Memory)
		if err != nil {
			return nil, cerrors.New(cerrors.TranslationError, cerrors.SourceLocation{File: nodeID},
				fmt.Sprintf("compose: cannot convert memory quantity %q to a deploy.resources byte count; use an integer quantity with a binary (Ki/Mi/Gi/Ti/Pi) or decimal (k/M/G/T/P) suffix, or a plain byte integer", rl.Memory),
				nil, ErrUnconvertibleComposeQuantity())
		}
		out.Memory = mem
	}
	if out.CPUs == "" && out.Memory == "" {
		return nil, nil
	}
	return out, nil
}

// k8sCPUToComposeCPUs converts a Kubernetes CPU quantity to a compose cpus
// fractional-core string. A millicpu quantity ("<int>m") is divided by 1000
// and formatted minimally (500m → "0.5", 1500m → "1.5", 2000m → "2"); a plain
// decimal or integer core count ("0.5", "2") passes through unchanged. Any
// other shape returns an error.
func k8sCPUToComposeCPUs(s string) (string, error) {
	if strings.HasSuffix(s, "m") {
		milli, err := strconv.Atoi(strings.TrimSuffix(s, "m"))
		if err != nil || milli < 0 {
			return "", fmt.Errorf("invalid millicpu quantity %q", s)
		}
		return formatMillicores(milli), nil
	}
	// Plain core count: accept an unsigned decimal number and pass it through.
	if !isUnsignedDecimal(s) {
		return "", fmt.Errorf("invalid cpu quantity %q", s)
	}
	return s, nil
}

// formatMillicores renders millicores as a minimal fractional-core string using
// integer math only (no float), so output is deterministic.
func formatMillicores(milli int) string {
	whole := milli / 1000
	frac := milli % 1000
	if frac == 0 {
		return strconv.Itoa(whole)
	}
	s := fmt.Sprintf("%d.%03d", whole, frac)
	return strings.TrimRight(s, "0")
}

// binarySuffixes and decimalSuffixes map Kubernetes memory suffixes to their
// byte multipliers. Binary suffixes are powers of 1024; decimal suffixes are
// powers of 1000 (Kubernetes uses a lowercase 'k' for the decimal kilo).
var binarySuffixes = []struct {
	suffix string
	mult   int64
}{
	{"Ki", 1 << 10}, {"Mi", 1 << 20}, {"Gi", 1 << 30}, {"Ti", 1 << 40}, {"Pi", 1 << 50}, {"Ei", 1 << 60},
}

var decimalSuffixes = []struct {
	suffix string
	mult   int64
}{
	{"k", 1_000}, {"M", 1_000_000}, {"G", 1_000_000_000}, {"T", 1_000_000_000_000}, {"P", 1_000_000_000_000_000}, {"E", 1_000_000_000_000_000_000},
}

// k8sMemToComposeBytes converts a Kubernetes memory quantity to an exact byte
// count string. It accepts an integer mantissa with a binary (Ki/Mi/Gi/Ti/Pi/Ei)
// or decimal (k/M/G/T/P/E) suffix, or a plain byte integer. A fractional
// mantissa, an unknown suffix, or an overflowing product returns an error so the
// caller halts rather than rounding.
func k8sMemToComposeBytes(s string) (string, error) {
	// Binary suffixes (two-character) are checked before decimal so "Mi" is not
	// mistaken for the decimal "M".
	for _, b := range binarySuffixes {
		if strings.HasSuffix(s, b.suffix) {
			return scaleBytes(strings.TrimSuffix(s, b.suffix), b.mult, s)
		}
	}
	for _, d := range decimalSuffixes {
		if strings.HasSuffix(s, d.suffix) {
			return scaleBytes(strings.TrimSuffix(s, d.suffix), d.mult, s)
		}
	}
	// No suffix: a plain byte integer.
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return "", fmt.Errorf("invalid memory quantity %q", s)
	}
	return strconv.FormatInt(n, 10), nil
}

// scaleBytes multiplies an integer mantissa by a byte multiplier, guarding
// against a non-integer mantissa and int64 overflow. original is the full
// quantity, used only for the error message.
func scaleBytes(mantissa string, mult int64, original string) (string, error) {
	n, err := strconv.ParseInt(mantissa, 10, 64)
	if err != nil || n < 0 {
		return "", fmt.Errorf("invalid memory quantity %q (mantissa must be a non-negative integer)", original)
	}
	if n != 0 && mult > (1<<63-1)/n {
		return "", fmt.Errorf("memory quantity %q overflows int64 bytes", original)
	}
	return strconv.FormatInt(n*mult, 10), nil
}

// isUnsignedDecimal reports whether s is a non-negative decimal number with at
// most one decimal point and at least one digit (e.g. "2", "0.5", ".5").
func isUnsignedDecimal(s string) bool {
	if s == "" {
		return false
	}
	dots := 0
	digits := 0
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r == '.':
			dots++
			if dots > 1 {
				return false
			}
		default:
			return false
		}
	}
	return digits > 0
}

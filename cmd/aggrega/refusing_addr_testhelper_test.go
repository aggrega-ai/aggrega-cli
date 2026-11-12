// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// refusingLoopbackAddr returns a loopback "host:port" that refuses TCP
// connections for the whole life of the test, for tests that need to prove a
// transport failure rather than an HTTP status.
//
// The address of a closed httptest server is NOT such an address, and that is
// the reason this helper exists. Closing the listener returns its port to the
// kernel's ephemeral pool while the captured address string goes on naming it,
// so a plain :0 bind anywhere else in the package — every httptest.NewServer is
// one — can be handed exactly that port. The endpoint that was supposed to be
// unreachable then answers, and the request meant for nothing at all is
// delivered into another test's handler. Both sides break: the test that wanted
// a refusal gets a response, and the test that now owns the port sees a request
// it never expected. So the address has to sit outside the ephemeral pool by
// construction. Being merely unbound at the moment it is chosen is not enough —
// that property expires the instant the kernel reissues the port.
//
// A port below the pool's floor has the property by construction: a bind to
// port 0 can only ever be handed a port from inside the pool, so no amount of
// parallelism can put a listener on one. Candidates are probed as well, so a
// machine that does happen to run something there fails loudly here instead of
// silently turning the refusal into a response somewhere downstream.
func refusingLoopbackAddr(t *testing.T) string {
	t.Helper()
	candidates := refusingPortCandidates()
	for _, port := range candidates {
		addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err != nil {
			return addr
		}
		_ = conn.Close()
	}
	t.Fatalf("no refusing loopback port available: something is listening on every candidate %v", candidates)
	return ""
}

// refusingPortCandidates lists loopback ports that a bind to port 0 cannot be
// handed, most-derived first.
func refusingPortCandidates() []int {
	if floor, ok := ephemeralPortFloor(); ok && floor > 4 {
		// Immediately below the pool: outside it by derivation from the running
		// kernel's own configuration rather than by assumption.
		return []int{floor - 1, floor - 2, floor - 3}
	}
	// With no readable pool floor, fall back to the privileged range: no
	// mainstream platform allocates ephemeral ports below 1024, and binding one
	// takes privilege a test process does not need and should not have.
	return []int{9, 1}
}

// ephemeralPortFloor reports the low end of the local ephemeral port range, when
// the platform exposes it.
func ephemeralPortFloor() (int, bool) {
	body, err := os.ReadFile("/proc/sys/net/ipv4/ip_local_port_range")
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(body))
	if len(fields) == 0 {
		return 0, false
	}
	floor, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, false
	}
	return floor, true
}

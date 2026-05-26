// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

// dispatchShutdown handles the aggrega/shutdown JSON-RPC notification.
// The notification carries no parameters and expects no response;
// the handler sets s.shuttingDown so handle's drain-state pre-check
// rejects subsequent requests with errShuttingDown and drops
// subsequent notifications.
//
// The drain TIMER is owned by the reader goroutine in dispatchLoop —
// it starts at notification ARRIVAL (when the reader parses the
// method field) rather than at OBSERVATION (when this handler runs
// in the main goroutine, possibly delayed by an in-flight prior
// handler). dispatchShutdown does not interact with the timer; its
// only job is the shuttingDown flag set.
//
// A second aggrega/shutdown notification is fully idempotent: the
// reader goroutine carries a local timerArmed flag that gates timer
// allocation, so back-to-back and spaced duplicates both skip the
// timer-creation path without leaking runtime resources. The handler
// itself simply re-sets the flag (already true) and returns.
func (s *Server) dispatchShutdown(_ *jsonRPCRequest) error {
	s.shuttingDown = true
	return nil
}

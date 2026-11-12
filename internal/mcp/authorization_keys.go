// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

// wellKnownAuthorizationServer is where an authorization server publishes what
// it is, and it is read the same way the protected-resource path is: inserted
// between the issuer's host and the issuer's own path, with a trailing slash
// trimmed first. An issuer that is a bare host answers at this path alone.
//
// Only this path is asked. An authorization server that publishes what it is
// somewhere else is not discovered, and the refusal a caller then receives says
// the keys could not be read rather than pretending the token was bad.
const wellKnownAuthorizationServer = "/.well-known/oauth-authorization-server"

// keySetReadBudget bounds reading a key set from the authorization server —
// asking it where its keys are and then fetching them, both inside this one
// budget rather than one each.
//
// It is the budget the trust-document fetch already holds, and one number
// rather than two because both answer the same question: how long a small JSON
// document published at a well-known address may take to arrive before the
// endpoint serving it counts as hung. Bounding the pair rather than each half
// is what keeps the worst case one budget long, which matters because reads are
// serialised: see keySet.
const keySetReadBudget = 15 * time.Second

// keySetBodyCap is the largest key-set or metadata body read. A key set is a
// handful of public keys; the cap keeps an endpoint that streams without end
// from filling this process's memory.
const keySetBodyCap = 1 << 20

// keySetRefreshInterval is the least time between two re-reads of the key set
// triggered by a token naming a key the held set does not carry.
//
// Nothing comparable existed to borrow, so the number is argued rather than
// inherited. What it bounds is the one thing a caller can drive from outside:
// a token names its key, so without an interval a caller sending tokens with
// invented key identifiers would make this deployment read from its
// authorization server once per request. With it, however many such tokens
// arrive, at most one read happens per interval. A minute is short enough that
// an authorization server which has rotated a key is picked up long before any
// token signed with it would otherwise be refused for a useful length of time,
// and long enough that the read is never a lever a caller can pull.
const keySetRefreshInterval = time.Minute

// verificationKeys yields the keys a token naming this key identifier may have
// been signed with.
//
// A token names one key and this returns the candidates for it: the keys the
// set carries under that identifier, or every key it carries where the token
// names none. An empty result is a token this deployment cannot verify.
type verificationKeys interface {
	keysFor(ctx context.Context, keyID string) ([]jose.JSONWebKey, error)
}

// keySet holds the keys a deployment verifies tokens against and re-reads them
// from their source when a token names one it does not carry.
//
// Reads are serialised on one mutex, which is deliberate rather than
// incidental: a burst of tokens naming a key the set does not carry produces
// one read rather than one per request, which is exactly the shape a caller
// would otherwise use to make this deployment hammer its own authorization
// server. The cost is that a read in flight holds every other verification
// behind it, which is why read is bounded by keySetReadBudget rather than left
// to whatever the client's own deadline is.
type keySet struct {
	// read produces the current key set from wherever it lives — a file on
	// disk, or the authorization server. It is the only thing that differs
	// between the two sources, so everything below is written once.
	read func(ctx context.Context) (*jose.JSONWebKeySet, error)

	// describeSource names where the keys come from, in the words a reader
	// configuring this deployment would use, for the refusal a failed read
	// produces.
	describeSource string

	mu sync.Mutex
	// held is what the last successful read produced, and nil until there has
	// been one. A deployment reading its keys from a file has one before it
	// serves anything, because resolveVerificationKeys reads the file so that a
	// listener pointed at one it cannot read is refused rather than binding;
	// one reading them from its authorization server has none until a token
	// arrives, so a listener does not wait on a third party to start.
	held *jose.JSONWebKeySet
	// refreshed is when a read triggered by a token naming a key the held set
	// did not carry last happened. Zero until one has. The read that
	// establishes the set is deliberately not one of these: it is not a
	// re-read, so it does not spend the interval that bounds the re-reads.
	refreshed time.Time
}

// keysFor returns the keys a token naming keyID may have been signed with,
// re-reading the set once when it carries none under that identifier.
//
// A token naming no key identifier is answered with every key the set carries,
// which is the only thing that can be done for it and is what an issuer
// publishing a single key expects. It never triggers a re-read: there is no
// identifier to go looking for, so a re-read would be asking the same question
// again.
func (s *keySet) keysFor(ctx context.Context, keyID string) ([]jose.JSONWebKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.held == nil {
		if err := s.load(ctx); err != nil {
			return nil, err
		}
		// The set has only just been read, so a key it does not carry is one
		// its source does not have. Reading it again in the same breath would
		// ask the same question twice.
		return pickKeys(s.held, keyID), nil
	}
	if found := pickKeys(s.held, keyID); len(found) > 0 {
		return found, nil
	}
	if keyID == "" {
		return nil, nil
	}
	if !s.refreshed.IsZero() && time.Since(s.refreshed) < keySetRefreshInterval {
		return nil, nil
	}
	if err := s.load(ctx); err != nil {
		return nil, err
	}
	s.refreshed = time.Now()
	return pickKeys(s.held, keyID), nil
}

// load replaces the held set with a freshly read one, inside the budget. The
// caller holds the lock.
func (s *keySet) load(ctx context.Context) error {
	readCtx, done := context.WithTimeout(ctx, keySetReadBudget)
	defer done()
	fresh, err := s.read(readCtx)
	if err != nil {
		return fmt.Errorf("read the keys tokens are verified against from %s: %w", s.describeSource, err)
	}
	s.held = fresh
	return nil
}

// pickKeys is the lookup keysFor states as its contract, kept here so the
// "named identifier, or every key" rule has one home rather than being spelled
// at each of the two places keysFor consults the set.
func pickKeys(set *jose.JSONWebKeySet, keyID string) []jose.JSONWebKey {
	if set == nil {
		return nil
	}
	if keyID == "" {
		return set.Keys
	}
	return set.Key(keyID)
}

// resolveVerificationKeys settles where a deployment's verification keys come
// from, and reads them once so that a deployment which cannot reach them is
// refused rather than discovering it on the first caller.
//
// The two sources are decided by one setting and nothing else. With a file
// named, the file is the only source and nothing is ever asked of the network —
// which is what makes it the answer for a workspace that permits no outbound
// request. With none, the keys come from the authorization server, and the
// client they are fetched with is taken from the caller's own gate, so the
// refusal an offline workspace produces is that gate's refusal rather than a
// second reading of the same posture here. That client is read through
// keyReadClient, which gives these reads a redirect policy of their own without
// changing the gate's client for anything else.
//
// ctx is whoever asked for the listener, and it belongs to exactly one of the
// two branches: the file branch reads before the listener answers for anything,
// and that read runs under this context rather than under a root of this
// function's own making, so it ends where its caller does wherever the source it
// reads from honours a stop. The issuer branch reads nothing here at all — its
// first read happens when a token arrives, under that request's own context.
func resolveVerificationKeys(ctx context.Context, cfg AuthorizationConfig, egress NetworkEgress) (*keySet, error) {
	if cfg.JWKSFile != "" {
		set := &keySet{
			read:           func(context.Context) (*jose.JSONWebKeySet, error) { return readKeySetFile(cfg.JWKSFile) },
			describeSource: "mcp.authorization.jwks_file",
		}
		held, err := set.read(ctx)
		if err != nil {
			return nil, fmt.Errorf("read the keys named by mcp.authorization.jwks_file: %w", err)
		}
		set.held = held
		return set, nil
	}

	issuer, err := parseDeclaredAddress(cfg.Issuer, "mcp.authorization.issuer", "https://login.example.com")
	if err != nil {
		return nil, err
	}
	if egress == nil {
		return nil, errNoKeySource
	}
	supplied, err := egress()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errNoKeySource, err)
	}
	client := keyReadClient(supplied)
	return &keySet{
		read: func(ctx context.Context) (*jose.JSONWebKeySet, error) {
			return fetchIssuerKeySet(ctx, client, issuer, cfg.Issuer)
		},
		describeSource: "the authorization server named by mcp.authorization.issuer",
	}, nil
}

// errNoKeySource reports that a listener has nowhere to read the keys it would
// verify tokens against: no file names them, and this workspace makes no
// outbound request.
//
// It names the one setting that resolves it, because that is the whole of what
// a reader meeting it has to do. The refusal it wraps, where there is one, says
// what put the workspace in that posture and which layer set it.
var errNoKeySource = errors.New(
	"a listener verifies every caller's token against the keys its authorization server publishes, and " +
		"this workspace makes no network request, so those keys have to come from a file: set " +
		"mcp.authorization.jwks_file to a key set for mcp.authorization.issuer")

// errKeysOverPlaintext reports that reading the verification keys would have
// gone over plain http to another machine, and so was not done.
//
// It is one condition reached two ways: the authorization server's metadata
// names such an address for its keys, or a read of the metadata or of the keys
// is answered with a redirect to one. Both are the same exposure — whoever sits
// on that path could answer with keys of their own — so both are refused alike,
// and in both nothing is requested from the refused address. It is a sentinel
// rather than a message so the request it refuses can be recorded under a reason
// of its own, separate from an authorization server that could not be reached.
var errKeysOverPlaintext = errors.New(
	"the verification keys would have been read over plain http from another machine, so they were not read")

// keyReadRedirectLimit is how many redirects a read of the verification keys
// follows before it gives up.
//
// It is the number net/http's own policy stops at, restated because setting a
// policy of one's own replaces that one entirely: a policy that only refused the
// plaintext hops would otherwise follow every other redirect without end.
const keyReadRedirectLimit = 10

// keyReadClient returns the client the verification keys are read with: a copy
// of the one the caller's gate supplied, carrying a redirect policy of its own.
//
// The policy refuses a hop to a plain http address on another machine before
// that hop is requested, which is what keeps a metadata or key address served
// over https from handing the read to an unprotected path one redirect later.
// Every other hop is judged as the supplied client would have judged it.
//
// It is a copy rather than the supplied client because that client is not this
// read's to change. The gate may hand back one that other requests share — the
// process's own default client, say — and a policy set on it would change how
// every one of those follows a redirect, which is a decision about that traffic
// rather than about the authorization server whose keys are being read.
func keyReadClient(supplied *http.Client) *http.Client {
	reads := *supplied
	previous := supplied.CheckRedirect
	reads.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if plaintextOffLoopback(next.URL) {
			return errKeysOverPlaintext
		}
		if previous != nil {
			return previous(next, via)
		}
		if len(via) >= keyReadRedirectLimit {
			return fmt.Errorf("stopped after %d redirects", keyReadRedirectLimit)
		}
		return nil
	}
	return &reads
}

// readKeySetFile reads a key set from disk and refuses one this deployment
// could not verify anything with.
//
// An empty set is refused rather than accepted, because a listener holding no
// key refuses every caller while looking configured, and the mistake that
// produces one — a path naming the wrong file, a file written before the keys
// were put in it — is one a reader can only find at startup.
func readKeySetFile(path string) (*jose.JSONWebKeySet, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return decodeKeySet(raw, path)
}

// decodeKeySet reads key-set bytes, whatever they were read from.
func decodeKeySet(raw []byte, from string) (*jose.JSONWebKeySet, error) {
	var set jose.JSONWebKeySet
	if err := json.Unmarshal(raw, &set); err != nil {
		return nil, fmt.Errorf("%s does not hold a key set: %w", from, err)
	}
	if len(set.Keys) == 0 {
		return nil, fmt.Errorf("%s holds a key set with no keys in it, so no caller's token could be verified", from)
	}
	return &set, nil
}

// authorizationServerMetadata is the part of what an authorization server
// publishes about itself that this deployment reads: the identifier it states
// for itself, and where it publishes the keys it signs with.
type authorizationServerMetadata struct {
	Issuer  string `json:"issuer"`
	JWKSURI string `json:"jwks_uri"`
}

// fetchIssuerKeySet asks the authorization server where its keys are and reads
// them from the address it names.
//
// The issuer the server states for itself is compared with the one this
// deployment was configured with, and a mismatch is refused. That check is what
// stops a document served at the well-known address of one authorization server
// from directing this deployment at another server's keys, which would let
// whoever controls that second server mint tokens this deployment accepts.
//
// Where the keys themselves are published is taken as the server states it,
// including on another host: an authorization server publishing its keys from a
// separate host is ordinary, and refusing that would refuse working deployments
// to guard something the issuer check already covers. The one address refused
// is a plain http one on another machine, and it is refused before it is asked:
// the issuer check cannot cover it, because the keys read from such an address
// are whatever whoever sits on the path to it chooses to answer.
func fetchIssuerKeySet(ctx context.Context, client *http.Client, issuer *url.URL,
	declaredIssuer string) (*jose.JSONWebKeySet, error) {
	discovery := *issuer
	discovery.Path = wellKnownAuthorizationServer + strings.TrimSuffix(issuer.Path, "/")
	discovery.RawPath = ""
	discovery.RawQuery = ""
	discovery.Fragment = ""
	discovery.RawFragment = ""

	var published authorizationServerMetadata
	body, err := fetchJSON(ctx, client, discovery.String())
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(body, &published); err != nil {
		return nil, fmt.Errorf("%s does not publish what it is: %w", discovery.String(), err)
	}
	if published.Issuer != declaredIssuer {
		return nil, fmt.Errorf(
			"%s states its identifier is %q and mcp.authorization.issuer declares %q, so this is not the "+
				"authorization server that was configured", discovery.String(), published.Issuer, declaredIssuer)
	}
	if published.JWKSURI == "" {
		return nil, fmt.Errorf("%s names no address for the keys it signs with", discovery.String())
	}
	// An address that does not parse is left to the request below, which refuses
	// it as an address it cannot ask; only one that parses can be read as plain
	// http on another machine.
	if keysAt, parseErr := url.Parse(published.JWKSURI); parseErr == nil && plaintextOffLoopback(keysAt) {
		return nil, fmt.Errorf("%s names %s for the keys it signs with: %w",
			discovery.String(), published.JWKSURI, errKeysOverPlaintext)
	}

	keys, err := fetchJSON(ctx, client, published.JWKSURI)
	if err != nil {
		return nil, err
	}
	return decodeKeySet(keys, published.JWKSURI)
}

// fetchJSON reads one small JSON document, bounded by the cap above so an
// endpoint that streams without end cannot fill this process's memory.
func fetchJSON(ctx context.Context, client *http.Client, address string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, fmt.Errorf("ask %s: %w", address, err)
	}
	req.Header.Set("Accept", contentTypeJSON)

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ask %s: %w", address, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("ask %s: HTTP %d", address, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, keySetBodyCap))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", address, err)
	}
	return body, nil
}

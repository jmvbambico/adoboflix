package source

import (
	"context"
	"errors"
	"time"
)

// ErrAccountInfoUnavailable is the "not available yet" answer an
// AccountInfoProvider returns when it has no cached account facts to report.
// Callers treat it as an empty answer, not a failure: they omit the account
// fields and read again later. A cold cache is the normal state before the
// library has fetched the playlist, so this is deliberately not an upstream
// error and must not be surfaced as one.
var ErrAccountInfoUnavailable = errors.New("account info is not available yet")

// AccountInfoProvider is an OPTIONAL capability. A Source implements it when it
// can describe the connected subscriber's account: the operator's own message,
// and the subscription's billing expiry when upstream reports one.
//
// It exists because AdoboFlix owns no accounts. The subscriber's only
// credential is the AdoboTV playlist code, and whatever account facts exist
// belong to AdoboTV. An adapter that talks to AdoboTV can surface them; an
// adapter that reads a local file or the development database has no account
// concept at all and does not implement this. It is deliberately not part of
// Source, so those adapters are not forced to invent a tier or a plan label.
//
// # It is best-effort and must not block
//
// Callers use AccountInfo to decorate a status view, never to gate one, so an
// implementation must be able to answer without waiting on upstream: it reports
// from an already-warm cache and returns ErrAccountInfoUnavailable when that
// cache is cold, rather than triggering a fetch. That keeps a status read from
// stalling on a slow or unreachable upstream. It also honours its context, so a
// caller that has gone away is not kept waiting.
//
// Callers must type-assert for this capability and omit the account facts
// plainly when it is absent or unavailable.
type AccountInfoProvider interface {
	// AccountInfo reports what upstream says about the connected account,
	// answering from an already-warm cache and never triggering a fetch. It
	// returns ErrAccountInfoUnavailable when there is nothing cached yet, and
	// returns the context's error (rather than doing work) when ctx is already
	// cancelled or past its deadline. It is read-only.
	AccountInfo(ctx context.Context) (AccountInfo, error)
}

// AccountInfo is what an adapter can truthfully report about the connected
// subscriber's account. Every field is optional — absent means "upstream did
// not say", which is the normal state for a non-subscription tier, so an empty
// value is not an error and must never be rendered as one.
type AccountInfo struct {
	// SubscriptionExpiresAt is the account's billing expiry, parsed from the
	// unix-seconds string AdoboTV sends. It is nil when upstream supplied no
	// expiry (non-subscription tiers) or supplied one this adapter could not
	// parse. AdoboFlix invents no expiry of its own.
	SubscriptionExpiresAt *time.Time

	// UserMessage is the operator's own message for this subscriber, empty when
	// upstream sent none.
	UserMessage string
}

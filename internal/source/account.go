package source

import "time"

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
// Callers must type-assert for this capability and omit the account facts
// plainly when it is absent.
type AccountInfoProvider interface {
	// AccountInfo reports what upstream says about the connected account. A
	// zero AccountInfo is a valid answer: it means upstream supplied neither a
	// message nor an expiry. It is read-only.
	AccountInfo() (AccountInfo, error)
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

package adobotvhttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"
)

// accountEnvelope renders a playlist envelope whose provider block carries the
// account facts under test. A nil billedTill omits the field entirely, which is
// how a non-subscription tier answers upstream.
func accountEnvelope(t *testing.T, r *http.Request, billedTill any) string {
	t.Helper()
	provider := map[string]any{
		"user_message": "Welcome to AdoboTV tester!",
		"epg":          abs(r, "/v1/epg/adobotv.xml.gz?token="+testToken),
		"vod_library":  abs(r, "/v1/vod?token="+testToken),
	}
	if billedTill != nil {
		provider["billed_till"] = billedTill
	}
	env := map[string]any{
		"provider":   provider,
		"categories": map[string]any{},
		"channels":   []any{},
	}
	body, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	return string(body)
}

// A subscription tier reports its billing expiry and the operator's message.
func TestAccountInfoReportsExpiryAndMessage(t *testing.T) {
	adapter, _ := newTestServer(t, time.Minute, func(w http.ResponseWriter, r *http.Request) {
		writeBody(w, http.StatusOK, "application/json", accountEnvelope(t, r, "1893456000"))
	})

	info, err := adapter.AccountInfo()
	if err != nil {
		t.Fatalf("AccountInfo: %v", err)
	}
	if info.UserMessage != "Welcome to AdoboTV tester!" {
		t.Errorf("UserMessage = %q, want the upstream message", info.UserMessage)
	}
	want := time.Unix(1893456000, 0).UTC()
	if info.SubscriptionExpiresAt == nil || !info.SubscriptionExpiresAt.Equal(want) {
		t.Errorf("SubscriptionExpiresAt = %v, want %v", info.SubscriptionExpiresAt, want)
	}
}

// billed_till is absent for non-subscription tiers: that is the normal case,
// not an error, and the message still comes through.
func TestAccountInfoOmitsAbsentExpiry(t *testing.T) {
	adapter, _ := newTestServer(t, time.Minute, func(w http.ResponseWriter, r *http.Request) {
		writeBody(w, http.StatusOK, "application/json", accountEnvelope(t, r, nil))
	})

	info, err := adapter.AccountInfo()
	if err != nil {
		t.Fatalf("AccountInfo: %v", err)
	}
	if info.SubscriptionExpiresAt != nil {
		t.Errorf("SubscriptionExpiresAt = %v, want nil when upstream sent none", info.SubscriptionExpiresAt)
	}
	if info.UserMessage == "" {
		t.Error("UserMessage is empty; a missing expiry must not drop the message")
	}
}

// A present-but-unparseable expiry is treated as absent rather than failing the
// whole call, so it cannot hide a perfectly good user_message.
func TestAccountInfoIgnoresUnparseableExpiry(t *testing.T) {
	adapter, _ := newTestServer(t, time.Minute, func(w http.ResponseWriter, r *http.Request) {
		writeBody(w, http.StatusOK, "application/json", accountEnvelope(t, r, "not-a-timestamp"))
	})

	info, err := adapter.AccountInfo()
	if err != nil {
		t.Fatalf("AccountInfo: %v", err)
	}
	if info.SubscriptionExpiresAt != nil {
		t.Errorf("SubscriptionExpiresAt = %v, want nil for an unparseable value", info.SubscriptionExpiresAt)
	}
	if info.UserMessage != "Welcome to AdoboTV tester!" {
		t.Errorf("UserMessage = %q, want the message preserved", info.UserMessage)
	}
}

// A refused playlist surfaces its own gate error; the caller omits the account
// facts rather than reporting a false account state.
func TestAccountInfoPropagatesFetchError(t *testing.T) {
	adapter, _ := newTestServer(t, time.Minute, func(w http.ResponseWriter, r *http.Request) {
		writeErrorEnvelope(w, http.StatusForbidden, "Invalid playlist code or account status")
	})

	info, err := adapter.AccountInfo()
	if !errors.Is(err, ErrPlaylistRejected) {
		t.Fatalf("AccountInfo error = %v, want it to wrap ErrPlaylistRejected", err)
	}
	if info.SubscriptionExpiresAt != nil || info.UserMessage != "" {
		t.Errorf("AccountInfo = %+v, want the zero value alongside the error", info)
	}
}

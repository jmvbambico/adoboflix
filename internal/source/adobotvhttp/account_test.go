package adobotvhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jmvbambico/adoboflix/internal/source"
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

// warmEnvelope fetches the envelope through the normal read path so the
// adapter's cache holds it. AccountInfo answers only from that cache.
func warmEnvelope(t *testing.T, adapter *Adapter) {
	t.Helper()
	if _, _, err := adapter.ListChannels("", 1, 0); err != nil {
		t.Fatalf("warm the envelope cache: %v", err)
	}
}

// A subscription tier reports its billing expiry and the operator's message.
func TestAccountInfoReportsExpiryAndMessage(t *testing.T) {
	adapter, _ := newTestServer(t, time.Minute, func(w http.ResponseWriter, r *http.Request) {
		writeBody(w, http.StatusOK, "application/json", accountEnvelope(t, r, "1893456000"))
	})
	warmEnvelope(t, adapter)

	info, err := adapter.AccountInfo(context.Background())
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
	warmEnvelope(t, adapter)

	info, err := adapter.AccountInfo(context.Background())
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
	warmEnvelope(t, adapter)

	info, err := adapter.AccountInfo(context.Background())
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

// billed_till "0" is upstream's sentinel for "no expiry to show for this
// account" — the live AdoboTV returns exactly this alongside a real
// user_message. The whole 1970 band is indistinguishable from it, so every value
// below the floor must be treated as absent, never rendered as 1 Jan 1970, and
// must not suppress the message. "1" and "31535999" (1970-12-31T23:59:59) are
// the edges of that band.
func TestAccountInfoRefusesExpiriesBeforeTheFloor(t *testing.T) {
	before := []string{"0", "-1", "1", "31535999", strconv.FormatInt(minBilledTill-1, 10)}
	for _, billedTill := range before {
		t.Run(billedTill, func(t *testing.T) {
			adapter, _ := newTestServer(t, time.Minute, func(w http.ResponseWriter, r *http.Request) {
				writeBody(w, http.StatusOK, "application/json", accountEnvelope(t, r, billedTill))
			})
			warmEnvelope(t, adapter)

			info, err := adapter.AccountInfo(context.Background())
			if err != nil {
				t.Fatalf("AccountInfo: %v", err)
			}
			if info.SubscriptionExpiresAt != nil {
				t.Errorf("SubscriptionExpiresAt = %v, want nil for billed_till %q", info.SubscriptionExpiresAt, billedTill)
			}
			if info.UserMessage != "Welcome to AdoboTV tester!" {
				t.Errorf("UserMessage = %q, want the message preserved beside a sentinel expiry", info.UserMessage)
			}
		})
	}
}

// The floor is not a recency test: it keeps every plausible past expiry, from
// the floor itself (2019-01-01) to a clearly real 2023 lapse.
func TestAccountInfoKeepsPastExpiriesAtOrAboveTheFloor(t *testing.T) {
	cases := []struct {
		raw  string
		want time.Time
	}{
		{strconv.FormatInt(minBilledTill, 10), time.Unix(minBilledTill, 0).UTC()},
		{"1700000000", time.Unix(1700000000, 0).UTC()},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			adapter, _ := newTestServer(t, time.Minute, func(w http.ResponseWriter, r *http.Request) {
				writeBody(w, http.StatusOK, "application/json", accountEnvelope(t, r, tc.raw))
			})
			warmEnvelope(t, adapter)

			info, err := adapter.AccountInfo(context.Background())
			if err != nil {
				t.Fatalf("AccountInfo: %v", err)
			}
			if info.SubscriptionExpiresAt == nil || !info.SubscriptionExpiresAt.Equal(tc.want) {
				t.Errorf("SubscriptionExpiresAt = %v, want %v kept", info.SubscriptionExpiresAt, tc.want)
			}
		})
	}
}

// The whole point of the cache-only contract: on a cold cache AccountInfo
// reports "not available yet" and does NOT reach upstream. This is what keeps
// /api/v1/source/status from stalling on a slow or unreachable AdoboTV.
func TestAccountInfoDoesNotFetchOnAColdCache(t *testing.T) {
	var hits atomic.Int32
	adapter, _ := newTestServer(t, time.Minute, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		writeBody(w, http.StatusOK, "application/json", accountEnvelope(t, r, "1893456000"))
	})

	// Deliberately do not warm the cache.
	start := time.Now()
	info, err := adapter.AccountInfo(context.Background())

	if !errors.Is(err, source.ErrAccountInfoUnavailable) {
		t.Fatalf("AccountInfo on a cold cache = %v, want ErrAccountInfoUnavailable", err)
	}
	if got := hits.Load(); got != 0 {
		t.Errorf("AccountInfo reached upstream %d time(s) on a cold cache; it must not fetch", got)
	}
	if info.SubscriptionExpiresAt != nil || info.UserMessage != "" {
		t.Errorf("AccountInfo = %+v, want the zero value", info)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("AccountInfo took %v on a cold cache; it must answer immediately", elapsed)
	}
}

// A caller that has gone away must not be kept waiting: a cancelled context is
// returned before any work, and never reaches upstream.
func TestAccountInfoHonoursCancelledContext(t *testing.T) {
	var hits atomic.Int32
	adapter, _ := newTestServer(t, time.Minute, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		writeBody(w, http.StatusOK, "application/json", accountEnvelope(t, r, "1893156000"))
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := adapter.AccountInfo(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("AccountInfo with a cancelled context = %v, want context.Canceled", err)
	}
	if got := hits.Load(); got != 0 {
		t.Errorf("AccountInfo reached upstream %d time(s) with a cancelled context", got)
	}
}

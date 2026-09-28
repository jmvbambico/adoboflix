package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jmvbambico/adoboflix/internal/playlistcode"
	"github.com/jmvbambico/adoboflix/internal/playlistfile"
	"github.com/jmvbambico/adoboflix/internal/source"
	"github.com/jmvbambico/adoboflix/internal/source/adobotvhttp"
	"github.com/jmvbambico/adoboflix/internal/sourcemode"
)

// newSessionHandler builds a handler whose revalidation window and clock are
// under the test's control, so the weekly re-check is exercised without waiting.
func newSessionHandler(t *testing.T, now time.Time, window time.Duration) (*SourceHandler, *PlayerHandler, *playlistcode.Store) {
	t.Helper()
	dir := t.TempDir()
	store := playlistcode.New(filepath.Join(dir, "playlist-code"))
	player := NewPlayerHandler(&codeStubSource{name: adobotvhttp.Name})
	h := NewSourceHandler(SourceHandlerOptions{
		Player:        player,
		Config:        source.Config{Name: adobotvhttp.Name},
		CodeStore:     store,
		ModeStore:     sourcemode.New(filepath.Join(dir, "source-mode")),
		FileStore:     playlistfile.New(filepath.Join(dir, "playlist")),
		SessionWindow: window,
		Clock:         func() time.Time { return now },
	})
	return h, player, store
}

// ageStoredCode writes a code and then sets the file's mtime, which is the
// session clock. It is how a test makes a stored code look a week old without
// sleeping.
func ageStoredCode(t *testing.T, store *playlistcode.Store, code string, at time.Time) {
	t.Helper()
	if err := store.Save(code); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := os.Chtimes(store.Path(), at, at); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}
}

// The property that matters, one case per outcome: a rejected code ends the
// session, every transient or gate outcome leaves the credential exactly where
// it is, and a code AdoboTV still accepts resets the window. Each case asserts
// the store contents, the file's mtime and the live source, so a change to the
// returned outcome alone cannot make it pass — and the ending case proves the
// harness would catch a wrongly-cleared credential.
func TestRevalidateSessionOutcomes(t *testing.T) {
	base := time.Now().UTC()
	const code = "STORED-SESSION-CODE"

	cases := []struct {
		name    string
		listErr error
		want    SessionOutcome
	}{
		{"a code that still works confirms", nil, SessionConfirmed},
		{"device pending confirms", fmt.Errorf("%w: splash", adobotvhttp.ErrDevicePending), SessionConfirmed},
		{"subscription inactive confirms", fmt.Errorf("%w: not active", adobotvhttp.ErrSubscriptionInactive), SessionConfirmed},
		{"playlist format m3u confirms", fmt.Errorf("%w: KODIPROP", adobotvhttp.ErrPlaylistFormatM3U), SessionConfirmed},
		{"content token rejected confirms", fmt.Errorf("%w: dead token", adobotvhttp.ErrTokenRejected), SessionConfirmed},
		{"content not found confirms", fmt.Errorf("%w: missing", adobotvhttp.ErrContentNotFound), SessionConfirmed},

		{"playlist rejected ends the session", fmt.Errorf("%w: 403", adobotvhttp.ErrPlaylistRejected), SessionEnded},

		{"user agent rejected leaves it intact", fmt.Errorf("%w: prefix", adobotvhttp.ErrUserAgentRejected), SessionUnchanged},
		{"malformed envelope leaves it intact", fmt.Errorf("%w: not json", adobotvhttp.ErrMalformedEnvelope), SessionUnchanged},
		{"malformed vod library leaves it intact", fmt.Errorf("%w: not an array", adobotvhttp.ErrMalformedVODLibrary), SessionUnchanged},
		{"upstream failure leaves it intact", fmt.Errorf("%w: HTTP 500", adobotvhttp.ErrUpstream), SessionUnchanged},
		{"a dead network leaves it intact", errors.New("dial tcp 127.0.0.1:8080: connect: connection refused"), SessionUnchanged},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, player, store := newSessionHandler(t, base, DefaultSessionWindow)
			codeAt := base.Add(-8 * 24 * time.Hour)
			ageStoredCode(t, store, code, codeAt)
			before := player.src()

			opens := 0
			firstCode := "unset"
			h.open = func(cfg source.Config) (source.Source, error) {
				opens++
				if opens == 1 {
					firstCode = cfg.PlaylistCode
				}
				return &codeStubSource{name: adobotvhttp.Name, listErr: tc.listErr}, nil
			}

			if got := h.RevalidateSession(context.Background()); got != tc.want {
				t.Fatalf("outcome = %v, want %v", got, tc.want)
			}
			if firstCode != code {
				t.Errorf("revalidation opened with code %q, want the stored one", firstCode)
			}

			stored, ok, err := store.Load()
			if tc.want == SessionEnded {
				if err != nil || ok {
					t.Errorf("store = (ok=%v, err=%v), want the rejected code cleared", ok, err)
				}
				if _, statErr := os.Stat(store.Path()); !errors.Is(statErr, os.ErrNotExist) {
					t.Errorf("a code file remains at %s, want none", store.Path())
				}
				if player.src() == before {
					t.Error("the source was not reopened after the session ended")
				}
				return
			}

			if err != nil || !ok || stored != code {
				t.Fatalf("store = (%q, ok=%v, err=%v), want the credential left intact", stored, ok, err)
			}
			at, has := store.ModTime()
			if !has {
				t.Fatal("ModTime = not ok, want the file's time")
			}
			if tc.want == SessionConfirmed {
				if !at.After(codeAt) {
					t.Errorf("mtime = %v, want the window reset forward of %v", at, codeAt)
				}
			} else if at.Sub(codeAt).Abs() > time.Second {
				t.Errorf("mtime = %v, want it left at %v for an inconclusive outcome", at, codeAt)
			}
			if player.src() != before {
				t.Error("an inconclusive or confirming outcome must not swap the live source")
			}
		})
	}
}

// A code younger than the window is not revalidated at all. Paired in one test
// with the aged case, so a harness that never revalidated anything could not
// pass both.
func TestRevalidateSessionWindowGate(t *testing.T) {
	base := time.Now().UTC()
	const code = "STORED-CODE"
	h, _, store := newSessionHandler(t, base, DefaultSessionWindow)

	openCalled := false
	h.open = func(source.Config) (source.Source, error) {
		openCalled = true
		return &codeStubSource{name: adobotvhttp.Name}, nil
	}

	ageStoredCode(t, store, code, base.Add(-24*time.Hour))
	if got := h.SessionDue(); got {
		t.Error("SessionDue for a one-day-old code = true, want false")
	}
	if got := h.RevalidateSession(context.Background()); got != SessionNotDue {
		t.Fatalf("outcome for a fresh code = %v, want not due", got)
	}
	if openCalled {
		t.Error("a code younger than the window was revalidated")
	}

	// The same harness DOES revalidate once the code ages past the window, so
	// the negative above is not vacuous.
	ageStoredCode(t, store, code, base.Add(-8*24*time.Hour))
	if !h.SessionDue() {
		t.Fatal("SessionDue for an eight-day-old code = false, want true")
	}
	if got := h.RevalidateSession(context.Background()); got != SessionConfirmed {
		t.Fatalf("outcome for an aged code = %v, want confirmed", got)
	}
	if !openCalled {
		t.Error("an aged code was not revalidated")
	}
}

// No stored code, an environment-only code, a non-code source and a disabled
// window are all "no session to re-check": nothing is opened.
func TestRevalidateSessionDoesNotRunWithoutAStoredSession(t *testing.T) {
	base := time.Now().UTC()

	cases := []struct {
		name   string
		window time.Duration
		setup  func(t *testing.T, h *SourceHandler, store *playlistcode.Store)
	}{
		{
			name:   "no stored code",
			window: DefaultSessionWindow,
			setup:  func(*testing.T, *SourceHandler, *playlistcode.Store) {},
		},
		{
			name:   "an environment-only code has no session file",
			window: DefaultSessionWindow,
			setup: func(t *testing.T, h *SourceHandler, _ *playlistcode.Store) {
				h.envCode = "ENV-ONLY-CODE"
			},
		},
		{
			name:   "a disabled window never re-checks",
			window: 0,
			setup: func(t *testing.T, _ *SourceHandler, store *playlistcode.Store) {
				ageStoredCode(t, store, "CODE", base.Add(-365*24*time.Hour))
			},
		},
		{
			name:   "an import source has no session",
			window: DefaultSessionWindow,
			setup: func(t *testing.T, h *SourceHandler, store *playlistcode.Store) {
				ageStoredCode(t, store, "CODE", base.Add(-365*24*time.Hour))
				h.player.SwapSource(&codeStubSource{name: "file"})
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, _, store := newSessionHandler(t, base, tc.window)
			tc.setup(t, h, store)

			h.open = func(source.Config) (source.Source, error) {
				t.Error("no session should have been opened for revalidation")
				return nil, nil
			}
			if got := h.RevalidateSession(context.Background()); got != SessionNotDue {
				t.Fatalf("outcome = %v, want not due", got)
			}
		})
	}
}

// --- sync ------------------------------------------------------------------

// syncStubSource is a codeStubSource that also implements the optional
// source.SyncProvider capability. Its counter covers every method a status read
// could use to fetch — the library reads GetStats and ListChannels, and Refresh
// — not just Refresh, so a status that fetched through a channel or stats read
// would be caught too. LastSyncedAt is deliberately NOT counted: it is the
// cache-only fact the status is allowed to read.
type syncStubSource struct {
	codeStubSource
	mu         sync.Mutex
	reads      int
	refreshErr error
	syncedAt   time.Time
}

func (s *syncStubSource) note() {
	s.mu.Lock()
	s.reads++
	s.mu.Unlock()
}

func (s *syncStubSource) Refresh(context.Context) error {
	s.note()
	return s.refreshErr
}

func (s *syncStubSource) GetStats() (*source.Stats, error) {
	s.note()
	return &source.Stats{}, nil
}

func (s *syncStubSource) ListChannels(string, int, int) ([]source.Channel, int, error) {
	s.note()
	return nil, 0, nil
}

func (s *syncStubSource) LastSyncedAt() (time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.syncedAt.IsZero() {
		return time.Time{}, false
	}
	return s.syncedAt, true
}

func (s *syncStubSource) readCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads
}

// ctxAwareStubSource is a codeStubSource whose validation read honours a
// caller's context, mirroring the adapter's source.ContextChannelLister
// capability. onList runs the canned behaviour; it lets a test prove that a
// cancelled boot re-check aborts rather than running to completion.
type ctxAwareStubSource struct {
	codeStubSource
	onList func(ctx context.Context) error
}

func (s *ctxAwareStubSource) ListChannelsContext(ctx context.Context, _ string, _ int, _ int) ([]source.Channel, int, error) {
	if s.onList == nil {
		return nil, 0, s.listErr
	}
	return nil, 0, s.onList(ctx)
}

// Status reports the last-sync time from the source's own cached fact and
// performs no upstream read to do it. The counter covers every source method a
// status read could use to fetch — Refresh, GetStats and ListChannels — so the
// absence is not merely "Refresh was not called".
func TestSourceStatusReportsLastSyncedAtWithoutFetching(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _, _ := newTestSourceHandler(t, source.Config{Name: adobotvhttp.Name}, "")
	at := time.Date(2026, 3, 12, 9, 30, 0, 0, time.UTC)
	stub := &syncStubSource{codeStubSource: codeStubSource{name: adobotvhttp.Name}, syncedAt: at}
	h.player.SwapSource(stub)
	if err := h.codes.Save("STORED-CODE"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	body := statusBody(t, h)
	raw, ok := body["last_synced_at"].(string)
	if !ok || raw == "" {
		t.Fatalf("last_synced_at = %v, want an RFC3339 string", body["last_synced_at"])
	}
	if raw != at.Format(time.RFC3339) {
		t.Errorf("last_synced_at = %q, want %q", raw, at.Format(time.RFC3339))
	}
	if n := stub.readCount(); n != 0 {
		t.Errorf("the status read triggered %d source reads, want 0 (a last-sync time is a local fact)", n)
	}
}

// A source that cannot sync reports no last-synced time. Paired with the case
// above, which proves the same field is present for a source that does.
func TestSourceStatusOmitsLastSyncedAtForANonSyncSource(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// An imported playlist: no SyncProvider.
	h, _, _ := newTestSourceHandler(t, source.Config{Name: "file"}, "")
	if v, present := statusBody(t, h)["last_synced_at"]; present {
		t.Errorf("last_synced_at = %v, want it absent for a non-sync source", v)
	}

	// A code-taking source whose library has not been fetched yet: the provider
	// exists but has no time, so the field is still omitted rather than zeroed.
	h2, _, _ := newTestSourceHandler(t, source.Config{Name: adobotvhttp.Name}, "")
	stub := &syncStubSource{codeStubSource: codeStubSource{name: adobotvhttp.Name}}
	h2.player.SwapSource(stub)
	if err := h2.codes.Save("STORED-CODE"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if v, present := statusBody(t, h2)["last_synced_at"]; present {
		t.Errorf("last_synced_at = %v, want it absent before any fetch", v)
	}
}

// The revalidate time appears only with a stored code and moves with the
// window, so the client can say when the next check is due. Absent without a
// stored code, never a zero time.
func TestSourceStatusReportsRevalidateAtOnlyWithStoredCode(t *testing.T) {
	gin.SetMode(gin.TestMode)
	base := time.Now().UTC()
	h, _, store := newSessionHandler(t, base, DefaultSessionWindow)

	if _, present := statusBody(t, h)["playlist_revalidate_at"]; present {
		t.Fatalf("playlist_revalidate_at present with no stored code")
	}

	storedAt := base.Add(-2 * time.Hour)
	ageStoredCode(t, store, "STORED-CODE", storedAt)

	raw, ok := statusBody(t, h)["playlist_revalidate_at"].(string)
	if !ok || raw == "" {
		t.Fatalf("playlist_revalidate_at = %v, want RFC3339", raw)
	}
	got, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	// RFC3339 drops sub-seconds, and the file's mtime may be truncated by the
	// filesystem, so compare within a second of stored+window.
	want := storedAt.Add(DefaultSessionWindow)
	if d := got.Sub(want); d > time.Second || d < -time.Second {
		t.Errorf("playlist_revalidate_at = %v, want stored+window %v (off by %v)", got, want, d)
	}
}

// playlist_revalidate_at is reported only for a code-taking source. This test
// uses a real window and a stored code, so the guards reached are the
// source-type one — not the earlier "window disabled" or "no stored code"
// guards. The same stored code DOES report the field while a code-taking source
// is active, and swapping to an import source is what makes it disappear, so
// the absence is not vacuous.
func TestSourceStatusOmitsRevalidateAtForANonCodeSource(t *testing.T) {
	gin.SetMode(gin.TestMode)
	base := time.Now().UTC()
	h, _, store := newSessionHandler(t, base, DefaultSessionWindow)
	ageStoredCode(t, store, "STORED-CODE", base.Add(-2*time.Hour))

	if _, ok := statusBody(t, h)["playlist_revalidate_at"].(string); !ok {
		t.Fatal("a code-taking source with a stored code must report playlist_revalidate_at")
	}

	h.player.SwapSource(&codeStubSource{name: "file"})
	if v, present := statusBody(t, h)["playlist_revalidate_at"]; present {
		t.Errorf("playlist_revalidate_at = %v, want it absent for an import source", v)
	}
}

// A cancelled boot re-check aborts through the context threaded into the
// validation read, and — crucially — leaves the credential exactly where it
// was: a cancellation is a transport failure, which is inconclusive and must
// never end a session.
func TestRevalidateSessionCancellationLeavesTheCredential(t *testing.T) {
	base := time.Now().UTC()
	const code = "STORED-SESSION-CODE"
	h, _, store := newSessionHandler(t, base, DefaultSessionWindow)
	ageStoredCode(t, store, code, base.Add(-8*24*time.Hour))

	entered := make(chan struct{})
	stub := &ctxAwareStubSource{
		codeStubSource: codeStubSource{name: adobotvhttp.Name},
		onList: func(ctx context.Context) error {
			close(entered)
			<-ctx.Done()
			return fmt.Errorf("%w: %v", adobotvhttp.ErrUpstream, ctx.Err())
		},
	}
	h.open = func(source.Config) (source.Source, error) { return stub, nil }

	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan SessionOutcome, 1)
	go func() { out <- h.RevalidateSession(ctx) }()

	// Bounded: if the validation read is never reached (the context is not
	// threaded) this fails crisply in two seconds instead of hanging the package
	// until the test timeout.
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("cancellation never reached the read")
	}
	cancel()

	select {
	case got := <-out:
		if got != SessionUnchanged {
			t.Fatalf("outcome = %v, want unchanged for a cancelled re-check", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the cancelled re-check did not return: cancellation did not reach the read")
	}
	if _, ok, err := store.Load(); err != nil || !ok {
		t.Fatalf("store after a cancelled re-check = (ok=%v, err=%v), want the credential intact", ok, err)
	}
}

// syncRouter wires the endpoints a sync test needs.
func syncRouter(h *SourceHandler) *gin.Engine {
	r := gin.New()
	r.GET("/api/v1/source/status", h.GetStatus)
	r.POST("/api/v1/source/sync", h.SyncSource)
	return r
}

// Sync now refreshes the active source and returns the updated status, so the
// client sees the new last-synced time from the reply itself.
func TestSyncSourceRefreshesAndReturnsStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _, _ := newTestSourceHandler(t, source.Config{Name: adobotvhttp.Name}, "")
	at := time.Date(2026, 3, 12, 10, 0, 0, 0, time.UTC)
	stub := &syncStubSource{codeStubSource: codeStubSource{name: adobotvhttp.Name}, syncedAt: at}
	h.player.SwapSource(stub)
	if err := h.codes.Save("STORED-CODE"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	w := doJSON(t, syncRouter(h), http.MethodPost, "/api/v1/source/sync", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", w.Code, w.Body.String())
	}
	if n := stub.readCount(); n != 1 {
		t.Errorf("source reads = %d, want 1 (the Refresh)", n)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["last_synced_at"] != at.Format(time.RFC3339) {
		t.Errorf("last_synced_at = %v, want %q from the reply", body["last_synced_at"], at.Format(time.RFC3339))
	}
}

// A source with nothing to sync answers with its own code rather than a silent
// success or a 500, and the client does not offer the action there anyway.
func TestSyncSourceUnsupportedForImport(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _, _ := newTestSourceHandler(t, source.Config{Name: "file"}, "")

	w := doJSON(t, syncRouter(h), http.MethodPost, "/api/v1/source/sync", "")
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501: %s", w.Code, w.Body.String())
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Code != codeSyncUnsupported {
		t.Errorf("code = %q, want %q", body.Code, codeSyncUnsupported)
	}
}

// A refresh failure on the manual path is reported with the source's own gate
// code, so the client can explain it; it is not swallowed into a success.
func TestSyncSourceReportsRefreshFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _, _ := newTestSourceHandler(t, source.Config{Name: adobotvhttp.Name}, "")
	stub := &syncStubSource{
		codeStubSource: codeStubSource{name: adobotvhttp.Name},
		refreshErr:     fmt.Errorf("%w: refused", adobotvhttp.ErrPlaylistRejected),
	}
	h.player.SwapSource(stub)

	w := doJSON(t, syncRouter(h), http.MethodPost, "/api/v1/source/sync", "")
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", w.Code, w.Body.String())
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Code != codePlaylistRejected {
		t.Errorf("code = %q, want %q", body.Code, codePlaylistRejected)
	}
	if n := stub.readCount(); n != 1 {
		t.Errorf("source reads = %d, want 1 (the failed Refresh)", n)
	}
}

// CanSync is the capability gate the scheduler consults before asking: true
// only when the active source has an upstream library to refresh.
func TestCanSyncTracksTheActiveSource(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h, _, _ := newTestSourceHandler(t, source.Config{Name: "file"}, "")
	if h.CanSync() {
		t.Error("CanSync with an imported playlist = true, want false")
	}

	h.player.SwapSource(&syncStubSource{codeStubSource: codeStubSource{name: adobotvhttp.Name}})
	if !h.CanSync() {
		t.Error("CanSync with a sync-capable source = false, want true")
	}
}

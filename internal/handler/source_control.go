package handler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jmvbambico/adoboflix/internal/playlistcode"
	"github.com/jmvbambico/adoboflix/internal/playlistfile"
	"github.com/jmvbambico/adoboflix/internal/source"
	"github.com/jmvbambico/adoboflix/internal/source/adobotvhttp"
	"github.com/jmvbambico/adoboflix/internal/source/file"
	"github.com/jmvbambico/adoboflix/internal/sourcemode"
)

// maxPlaylistImportBytes bounds an imported playlist. Parsing happens in
// memory against a service that binds loopback by default, so an unbounded
// upload is a denial of service we should not write; the cap is generous enough
// for a very large playlist (tens of thousands of entries) and small enough to
// bound the parse. It is a var so a test can lower it rather than allocating
// the full 10 MiB.
var maxPlaylistImportBytes int64 = 10 << 20 // 10 MiB

// SourceHandler serves the source-selection endpoints. They are how a user
// chooses one of the two paths in AGENTS.md while the server runs, instead of
// editing .env and restarting:
//
//   - the subscriber path, by entering a playlist code (POST/DELETE
//     /source/playlist-code), which selects the adobotv-http mode; and
//   - the no-account path, by importing a playlist (POST/DELETE
//     /source/playlist-file), which selects the file mode.
//
// The two paths are exactly the selectable adapters the source package
// registers. Which are offered — and which is active, where it came from, and
// whether it is a development harness — is reported by GetStatus, so the client
// never hardcodes adapter names.
//
// # The credential is write-only from the API's point of view
//
// The playlist-code endpoints accept a code and persist it to a local file the
// server owns. They never return it — not in a response body, not masked, not in
// an error. status exposes only booleans. The swap is done by reopening the
// adapter with the new code, so no request ever carries the credential after
// entry.
//
// # Security posture
//
// These endpoints are unauthenticated, like the rest of /api/v1. That is
// consistent with the current posture — the server binds loopback by default
// and /api/v1/proxy is unauthenticated too — but accepting a credential and
// writing files to disk is a step up in consequence. See "Stream URL exposure"
// in docs/source-adapters.md: if AdoboFlix ever becomes multi-user or
// internet-exposed, these endpoints need authentication before anything else.
type SourceHandler struct {
	player *PlayerHandler
	// cfg is the base source configuration the running process selected: the
	// database handle for a database-backed adapter. Name, PlaylistCode and
	// FilePath are set per open. The playlist code is resolved fresh on every
	// call, never cached here.
	cfg source.Config
	// codes persists the subscriber credential and owns its precedence.
	codes *playlistcode.Store
	// modes remembers the mode the user chose and owns its precedence against
	// the environment override.
	modes *sourcemode.Store
	// files persists an imported playlist.
	files *playlistfile.Store
	// envCode is the environment fallback (ADOBOFLIX_ADOBOTV_PLAYLIST_CODE). It
	// is consulted only when no code has been stored.
	envCode string
	// envFile is the environment fallback path (ADOBOFLIX_FILE_PATH). It is
	// reported as a configured file mode even when nothing has been imported.
	envFile string
	// envSource is the ADOBOFLIX_SOURCE override, "" when unset. When set it
	// pins the source and the mode-changing endpoints refuse.
	envSource string
	// open reopens an adapter from a configuration. It is a field so tests can
	// exercise the endpoints without a live AdoboTV.
	open func(source.Config) (source.Source, error)
	// login authenticates a username/password against AdoboTV and returns the
	// account's playlist code, which is then stored and swapped exactly as a
	// typed-in code is. It is a field so tests can exercise the endpoint without
	// a live AdoboTV; production wires it to the adapter's real login client.
	login func(ctx context.Context, username, password string) (string, error)
	// sessionWindow is how long a stored playlist code is trusted before it is
	// re-checked against AdoboTV. Zero or negative disables the re-check, so the
	// credential is trusted until the user disconnects it. It is a duration, not
	// an expiry: see RevalidateSession.
	sessionWindow time.Duration
	// now is the clock the session window is measured against. It is a field so
	// the window can be exercised without waiting real days.
	now func() time.Time
}

// DefaultSessionWindow is how long a stored playlist code is trusted before the
// server re-checks it against AdoboTV: one week, matching the owner's "session"
// lifetime. The re-check is silent — a code that still works is reconfirmed and
// the user never sees it, and only a definitive rejection ends the session. See
// RevalidateSession.
const DefaultSessionWindow = 7 * 24 * time.Hour

// EnvSessionWindow overrides DefaultSessionWindow. It is a Go duration string
// (e.g. "168h" for a week); "0" or a negative value disables the re-check, so
// the stored code is trusted until the user disconnects it.
const EnvSessionWindow = "ADOBOFLIX_PLAYLIST_REVALIDATE_INTERVAL"

// SourceHandlerOptions wires the source endpoints to their collaborators.
type SourceHandlerOptions struct {
	// Player owns the live source the endpoints swap.
	Player *PlayerHandler
	// Config is the base source configuration: the database handle for a
	// database-backed adapter. Name, PlaylistCode and FilePath are set per open.
	Config source.Config
	// CodeStore persists the subscriber's playlist code.
	CodeStore *playlistcode.Store
	// ModeStore remembers the mode the user chose.
	ModeStore *sourcemode.Store
	// FileStore persists an imported playlist.
	FileStore *playlistfile.Store
	// EnvPlaylistCode is the ADOBOFLIX_ADOBOTV_PLAYLIST_CODE fallback.
	EnvPlaylistCode string
	// EnvFilePath is the ADOBOFLIX_FILE_PATH fallback.
	EnvFilePath string
	// EnvSource is the ADOBOFLIX_SOURCE override, "" when unset.
	EnvSource string
	// Login authenticates a username/password against AdoboTV and returns the
	// account's playlist code. Nil builds one from the process environment
	// (ADOBOFLIX_ADOBOTV_BASE_URL), which is what production uses.
	Login func(ctx context.Context, username, password string) (string, error)
	// SessionWindow is how long a stored playlist code is trusted before it is
	// re-checked. Zero disables the re-check. See DefaultSessionWindow.
	SessionWindow time.Duration
	// Clock overrides the session window's time source. Nil means time.Now.
	Clock func() time.Time
}

// NewSourceHandler wires the endpoints to the live player (whose source is
// swapped), the stores, and the selected source configuration.
func NewSourceHandler(opts SourceHandlerOptions) *SourceHandler {
	clock := opts.Clock
	if clock == nil {
		clock = time.Now
	}
	login := opts.Login
	if login == nil {
		login = loginFromEnv
	}
	return &SourceHandler{
		player:        opts.Player,
		cfg:           opts.Config,
		codes:         opts.CodeStore,
		modes:         opts.ModeStore,
		files:         opts.FileStore,
		envCode:       opts.EnvPlaylistCode,
		envFile:       opts.EnvFilePath,
		envSource:     opts.EnvSource,
		open:          source.Open,
		login:         login,
		sessionWindow: opts.SessionWindow,
		now:           clock,
	}
}

// loginFromEnv authenticates against AdoboTV using the process environment's
// base URL and User-Agent. The client is built per call — it opens no
// connection until it is used — so no credential state is held between
// requests.
func loginFromEnv(ctx context.Context, username, password string) (string, error) {
	client, err := adobotvhttp.NewLoginClientFromEnv()
	if err != nil {
		return "", err
	}
	return client.Login(ctx, username, password)
}

// GetStatus reports how the source is currently configured. It never returns
// the playlist code. The fields:
//
//   - source — the active adapter's name, "" when none is configured.
//   - active — whether a source is configured at all.
//   - origin — "env" (pinned by ADOBOFLIX_SOURCE), "stored" (a mode the user
//     chose in the UI), or "none".
//   - dev — whether the active source is a development harness the UI must not
//     present as a normal option.
//   - needs_playlist_code, playlist_code_configured — the existing contract,
//     describing the active source.
//   - playlist_file_configured — whether an imported playlist (or
//     ADOBOFLIX_FILE_PATH) is available for the file mode.
//   - health_scan_supported — whether the ACTIVE source can enumerate its
//     library's streams for health probing (source.StreamProbeLister), so the
//     client can offer or withhold the scan entry point without calling an
//     endpoint and reading a 501. It describes only the active source.
//   - playlist_imported_at — RFC3339, the stored imported playlist's mtime.
//     Present only when this server imported the playlist itself; omitted for
//     an ADOBOFLIX_FILE_PATH playlist and when nothing is imported, so an
//     absent field never renders as a zero time.
//   - modes — one entry per registered adapter, each with name, selectable,
//     dev, active, configured and needs_playlist_code. The client renders the
//     user's choices from this and never hardcodes adapter names.
//
// Two account facts are added when — and only when — the active adapter can
// supply them AND a credential is configured to read them with: the
// subscription's billing expiry (subscription_expires_at, RFC3339) and the
// operator's own message (user_message). They are additive and best-effort:
// a source with no account concept (file, postgres-direct), a missing
// credential, or a cold cache leaves them out and the endpoint still answers
// 200 with the fields above unchanged. No tier or plan label is ever invented
// here.
//
// The account read is cache-only and context-aware (see
// source.AccountInfoProvider): it never triggers an upstream fetch, so this
// endpoint cannot stall on a slow or unreachable AdoboTV. When the cache is
// cold the fields are omitted and appear on a later poll, once the library has
// loaded the envelope — which a normal app load does anyway.
func (h *SourceHandler) GetStatus(c *gin.Context) {
	body := h.statusBody()
	_, configured := h.state()

	// Only a source that takes a code has anything to read account facts from;
	// without a configured code there is no playlist to decorate the status
	// with.
	if configured {
		if provider, ok := h.player.src().(source.AccountInfoProvider); ok {
			info, err := provider.AccountInfo(c.Request.Context())
			switch {
			case err == nil:
				if info.SubscriptionExpiresAt != nil {
					body["subscription_expires_at"] = info.SubscriptionExpiresAt.UTC().Format(time.RFC3339)
				}
				if msg := strings.TrimSpace(info.UserMessage); msg != "" {
					body["user_message"] = msg
				}
			case errors.Is(err, source.ErrAccountInfoUnavailable):
				// Nothing cached yet (or the client went away): a normal, silent
				// omission for a best-effort decoration, not a failure to log.
			case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
				// The caller cancelled mid-read; there is no one to tell.
			default:
				// A real adapter fault. The account facts are a nicety, not
				// part of the contract, so it is logged and the fields are omitted
				// rather than turning the status endpoint into an error.
				log.Printf("source status: account info unavailable for %q: %v", h.activeName(), err)
			}
		}
	}

	c.JSON(http.StatusOK, body)
}

// SetPlaylistCode accepts a playlist code, validates it against AdoboTV with a
// cheap real call before persisting, then persists it and swaps the live
// source. On any validation failure it persists nothing and swaps nothing, and
// returns the same stable gate code the rest of the API uses. The code is never
// echoed in the response or written to the log.
//
// Entering a code selects the login path (adobotv-http): it is the mode the
// credential implies, so the endpoint works whether the server is sourceless,
// already on adobotv-http, or on the file mode. When ADOBOFLIX_SOURCE pins a
// different source the mode cannot change, and the request is refused — the
// environment override is the only way to reach a development source, so it
// also means the UI must not fight it.
func (h *SourceHandler) SetPlaylistCode(c *gin.Context) {
	var body struct {
		Code string `json:"code"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expected a JSON body with a code field"})
		return
	}
	code := strings.TrimSpace(body.Code)
	if code == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing code"})
		return
	}

	if h.pinned() && h.activeName() != adobotvhttp.Name {
		c.JSON(http.StatusConflict, gin.H{
			"error": "the content source is pinned by " + source.EnvSource + "; unset it to choose a source in the UI",
			"code":  codeSourcePinnedByEnv,
		})
		return
	}

	h.applyPlaylistCode(c, code)
}

// Login connects an AdoboTV account by username and password. It authenticates
// against AdoboTV, reads the account's playlist code from the authenticated
// profile, and hands that code to the same store-and-swap path the playlist-code
// endpoint uses. Everything downstream — the weekly re-check, the daily sync,
// the gate classification, the device-pending handling — is unchanged, because
// it still operates on a playlist code; the user simply never types one.
//
// # The password
//
// The password is a credential more sensitive than the playlist code: it is
// reusable and probably shared with other services. It exists only for the
// duration of this request. It is placed in the login request body to AdoboTV
// and nowhere else: never persisted (only the playlist code read from the
// profile is), never logged (not its value; the adapter's errors never carry
// it), and never placed in a response body or an error. The reference is
// dropped as soon as the call returns.
//
// # Failure mapping
//
// AdoboTV returns one 401 for an unknown username and a wrong password alike,
// to prevent enumeration; AdoboFlix preserves that, so its response never says
// which was wrong. A 403 reCAPTCHA demand is its own code and message: the
// password may be correct, and it must not read as a credential failure. A
// transport failure persists nothing. All of these are mapped through the same
// stable vocabulary as every other source error (see source_error.go).
func (h *SourceHandler) Login(c *gin.Context) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expected a JSON body with username and password fields"})
		return
	}
	username := strings.TrimSpace(body.Username)
	if username == "" || body.Password == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "missing username or password"})
		return
	}

	if h.pinned() && h.activeName() != adobotvhttp.Name {
		c.JSON(http.StatusConflict, gin.H{
			"error": "the content source is pinned by " + source.EnvSource + "; unset it to choose a source in the UI",
			"code":  codeSourcePinnedByEnv,
		})
		return
	}

	code, err := h.login(c.Request.Context(), username, body.Password)
	// Drop the password reference as soon as the call returns, so the intent is
	// explicit even though Go's GC owns the memory from here.
	body.Password = ""
	if err != nil {
		// Every error h.login can return is password-free by construction: the
		// adapter names only a redacted URL, a status, or one of its sentinels.
		log.Printf("source login: authenticating against AdoboTV: %v", err)
		writeSourceError(c, err, "")
		return
	}

	h.applyPlaylistCode(c, code)
}

// applyPlaylistCode is the shared tail of every way a playlist code arrives —
// typed by the user (SetPlaylistCode) or read from the profile after a login
// (Login). It validates the code against AdoboTV with one cheap real call,
// persists it only when that call proves it valid, remembers the login mode, and
// swaps the live source. It writes the response itself so both callers share one
// behaviour and one set of stable codes.
//
// The code is never logged and never echoed: only its length is logged.
func (h *SourceHandler) applyPlaylistCode(c *gin.Context, code string) {
	// Validate before persisting or swapping: a wrong code must fail here, not
	// on the user's first playback attempt. Nothing is written until it passes.
	candidate, err := h.open(h.configWithCode(code))
	if err != nil {
		log.Printf("source playlist code: opening %q with the entered code: %v", adobotvhttp.Name, err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "could not open the source with that code",
			"code":  codeInternalError,
		})
		return
	}
	// Whether the code is *kept* is a separate question from "did the call
	// succeed" — it is "did AdoboTV recognise the code". See
	// playlistCodeProvenValid. The request's context is passed through, so where
	// the adapter can honour one (source.ContextChannelLister, as adobotv-http
	// does) a client that gives up cancels the validation with it.
	validationErr := validatePlaylistCode(c.Request.Context(), candidate)
	if validationErr != nil && !playlistCodeProvenValid(validationErr) {
		writeSourceError(c, validationErr, "")
		return
	}

	if err := h.codes.Save(code); err != nil {
		log.Printf("source playlist code: persisting: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "could not save the playlist code",
			"code":  codeInternalError,
		})
		return
	}
	// Remember the mode unless the environment pins it. Saving before the swap
	// keeps the durable state and the live source consistent: a store failure
	// leaves the previous source running rather than a source the next boot
	// would not reopen.
	if !h.pinned() {
		if err := h.modes.Save(adobotvhttp.Name); err != nil {
			log.Printf("source playlist code: saving mode: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "could not save the source mode",
				"code":  codeInternalError,
			})
			return
		}
	}
	h.player.SwapSource(candidate)
	// Log the length only: the code itself must never reach the log.
	log.Printf("[source] playlist code accepted for %q; source reopened (%d characters)", adobotvhttp.Name, len(code))

	if validationErr != nil {
		// The code identified the subscriber; the gate is something other than
		// the code (a device awaiting approval, a lapsed subscription). The code
		// is kept so the user does not have to retype it, and the gate is still
		// reported so the client shows the right screen. Reads fail with that
		// gate until it clears, then start working with no further action.
		writeSourceError(c, validationErr, "")
		return
	}

	c.JSON(http.StatusOK, h.statusBody())
}

// DeletePlaylistCode clears the stored code and reopens the source from what
// remains: the environment fallback if one is set, otherwise the unconfigured
// state. Clearing an already-absent code is not an error. The chosen mode is
// left alone — the user still wants the login path, they have simply removed
// its credential.
func (h *SourceHandler) DeletePlaylistCode(c *gin.Context) {
	if err := h.codes.Clear(); err != nil {
		log.Printf("source playlist code: clearing: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "could not clear the playlist code",
			"code":  codeInternalError,
		})
		return
	}

	active := h.activeName()
	if active != "" && source.NeedsPlaylistCode(active) {
		// The stored code is gone; reopen with whatever remains, so the running
		// source matches the precedence rule the store enforces.
		reopened, err := h.openMode(active)
		if err != nil {
			log.Printf("source playlist code: reopening after clear: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "could not reopen the source after clearing",
				"code":  codeInternalError,
			})
			return
		}
		h.player.SwapSource(reopened)
	}

	c.JSON(http.StatusOK, h.statusBody())
}

// SetPlaylistFile accepts a playlist and imports it as the file mode.
//
// The upload is validated by actually parsing it with the real file adapter —
// a scratch copy in the store's directory — before anything is persisted, the
// same way the playlist-code endpoint validates before it saves. Only once it
// parses does the content land in the imported-playlist location, the mode is
// remembered, and the live source swapped to the file adapter. On failure
// nothing is persisted and nothing is swapped, and the rejection names what the
// parser found wrong: the owner's condition is "if they followed the json
// format correctly", so the error has to teach.
//
// The adapter's own formats are accepted: the JSON envelope documented in
// docs/source-adapters.md, and M3U/M3U8. The format is chosen from the content's
// shape only to name the stored file's extension; the adapter still does the
// parse.
func (h *SourceHandler) SetPlaylistFile(c *gin.Context) {
	if h.pinned() {
		c.JSON(http.StatusConflict, gin.H{
			"error": "the content source is pinned by " + source.EnvSource + "; unset it to choose a source in the UI",
			"code":  codeSourcePinnedByEnv,
		})
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxPlaylistImportBytes)
	data, err := io.ReadAll(c.Request.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{
				"error": fmt.Sprintf("the playlist is too large: the limit is %d MiB", maxPlaylistImportBytes>>20),
				"code":  codePlaylistTooLarge,
			})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "could not read the request body",
			"code":  codeInvalidPlaylist,
		})
		return
	}
	if len(bytes.TrimSpace(data)) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "the request body is empty: send the playlist content (JSON or M3U)",
			"code":  codeInvalidPlaylist,
		})
		return
	}

	// Validate before persisting: parse the upload with the real adapter from a
	// scratch file. A rejected upload leaves the imported-playlist location
	// untouched.
	tmp, err := h.files.WriteTemp(data)
	if err != nil {
		log.Printf("source playlist file: staging upload: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "could not stage the playlist for validation",
			"code":  codeInternalError,
		})
		return
	}
	defer os.Remove(tmp)

	if _, err := h.open(h.configWithFile(tmp)); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": importRejectionMessage(err, tmp, data),
			"code":  codeInvalidPlaylist,
		})
		return
	}

	saved, savedFormat, err := h.files.Save(data)
	if err != nil {
		log.Printf("source playlist file: persisting: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "could not save the playlist",
			"code":  codeInternalError,
		})
		return
	}

	candidate, err := h.open(h.configWithFile(saved))
	if err != nil {
		// The same bytes parsed from the scratch file, so a failure here is the
		// final path, not the content. Remove the unopenable playlist AND release
		// any remembered file mode, or the next boot would be left pointing at a
		// playlist that is gone.
		_ = h.files.Clear()
		h.clearFileModeIfRemembered()
		// If the live source is the file adapter, it reads the store's single
		// path — the one just removed — so it must stop claiming to serve it.
		// Without this, status would report an active source whose backing file
		// is gone and the UI would keep rendering a playlist that does not exist
		// until a reload.
		if h.activeName() == file.Name && !h.pinned() {
			h.player.SwapSource(source.Unconfigured())
		}
		log.Printf("source playlist file: opening stored playlist: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "could not open the saved playlist",
			"code":  codeInternalError,
		})
		return
	}
	if err := h.modes.Save(file.Name); err != nil {
		// Leave the stored playlist in place: it is valid and self-consistent,
		// and removing it here would strand a remembered file mode on a playlist
		// that is gone. The mode simply was not updated, so the boot stays
		// coherent; a retry re-saves it.
		log.Printf("source playlist file: saving mode: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "could not save the source mode",
			"code":  codeInternalError,
		})
		return
	}

	h.player.SwapSource(candidate)
	log.Printf("[source] playlist imported (%s, %d bytes); source is now %q", savedFormat, len(data), file.Name)

	c.JSON(http.StatusOK, h.statusBody())
}

// DeletePlaylistFile clears the imported playlist. If that playlist was the
// active source, the remembered mode is cleared with it and the server returns
// to the sourceless state the UI offers its choices from — a stored mode that
// pointed at a file we just removed would otherwise fail to open on the next
// boot. An env-pinned source is left running: it reads its own path, not the
// imported one.
func (h *SourceHandler) DeletePlaylistFile(c *gin.Context) {
	// Release the remembered mode BEFORE removing the file it points at. If the
	// mode clear fails, the playlist is still there and the next boot opens it;
	// clearing the file first would let a mode-clear failure strand the boot on a
	// mode pointing at a playlist that is gone.
	wasActiveFile := h.activeName() == file.Name && !h.pinned()
	if wasActiveFile {
		if err := h.modes.Clear(); err != nil {
			log.Printf("source playlist file: clearing mode: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{
				"error": "could not clear the source mode",
				"code":  codeInternalError,
			})
			return
		}
	}

	if err := h.files.Clear(); err != nil {
		log.Printf("source playlist file: clearing: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error": "could not clear the imported playlist",
			"code":  codeInternalError,
		})
		return
	}

	if wasActiveFile {
		h.player.SwapSource(source.Unconfigured())
		log.Printf("[source] imported playlist cleared; server is sourceless until a mode is chosen")
	}

	c.JSON(http.StatusOK, h.statusBody())
}

// clearFileModeIfRemembered releases a stored mode that names the file adapter,
// so a compensation which has just removed the imported playlist cannot leave
// the next boot pointing at a playlist that is gone. It never touches an
// env-pinned mode — the environment owns that choice, not the UI — and a failure
// to read the mode is logged and left alone rather than guessed at.
func (h *SourceHandler) clearFileModeIfRemembered() {
	if h.pinned() {
		return
	}
	res, err := h.modes.Resolve(h.envSource)
	if err != nil {
		log.Printf("source playlist file: reading mode during cleanup: %v", err)
		return
	}
	if res.Mode != file.Name {
		return
	}
	if err := h.modes.Clear(); err != nil {
		log.Printf("source playlist file: clearing mode during cleanup: %v", err)
	}
}

// statusBody is the status response without the account decorations. It is
// shared by GET and by the mutating endpoints' success responses, so a client
// can refresh its view from the response it just received. It never contains
// the playlist code.
func (h *SourceHandler) statusBody() gin.H {
	needs, configured := h.state()
	active := h.activeName()
	body := gin.H{
		"source":                   active,
		"active":                   active != "",
		"origin":                   h.origin(),
		"dev":                      source.Dev(active),
		"needs_playlist_code":      needs,
		"playlist_code_configured": configured,
		"playlist_file_configured": h.fileConfigured(),
		"health_scan_supported":    h.player.HealthScanSupported(),
		"modes":                    h.modesStatus(active),
	}
	// Only a playlist the user imported here has an import time; a playlist
	// supplied through ADOBOFLIX_FILE_PATH, and the sourceless state, have none.
	// The field is omitted rather than sent as a zero time, so the client has
	// nothing to mistake for 1 Jan 1970.
	if importedAt, ok := h.files.ModTime(); ok {
		body["playlist_imported_at"] = importedAt.UTC().Format(time.RFC3339)
	}
	// last_synced_at is the last time the active source fetched its library
	// from upstream. It is read from the source's own cached timestamp, so this
	// endpoint stays cache-only and never touches the network — the same reason
	// the account facts are read from cache. A source with no upstream library
	// (an imported playlist) has no sync concept and reports nothing.
	if provider, ok := h.player.src().(source.SyncProvider); ok {
		if at, ok := provider.LastSyncedAt(); ok {
			body["last_synced_at"] = at.UTC().Format(time.RFC3339)
		}
	}
	// playlist_revalidate_at is when the stored code will next be re-checked
	// against AdoboTV. It is the session window applied to the stored file's
	// clock, reported only when a stored code exists for a code-taking source.
	// Omitted otherwise, never sent as a zero time.
	if at, ok := h.nextRevalidateAt(); ok {
		body["playlist_revalidate_at"] = at.UTC().Format(time.RFC3339)
	}
	return body
}

// storedSession returns the stored playlist code and when it was last written,
// which is the clock the revalidation window runs against. ok is false when no
// code is stored here — an environment-supplied code has no file and therefore
// no session, because it is operator configuration rather than the user's
// credential to re-check. A stored file that cannot be stat-ed is treated the
// same way, so a transient filesystem fault cannot be mistaken for a due
// re-check.
func (h *SourceHandler) storedSession() (code string, at time.Time, ok bool, err error) {
	code, stored, err := h.codes.Load()
	if err != nil || !stored {
		return "", time.Time{}, false, err
	}
	at, has := h.codes.ModTime()
	if !has {
		return "", time.Time{}, false, nil
	}
	return code, at, true, nil
}

// SessionDue reports whether the stored code is old enough to re-check. It is
// the gate RevalidateSession applies, exposed so a caller can decide whether a
// re-check is worth attempting. It is false when revalidation is disabled, when
// no code is stored, or when the active source does not take a code — an
// import user has no session to re-check.
func (h *SourceHandler) SessionDue() bool {
	if h.sessionWindow <= 0 {
		return false
	}
	if !source.NeedsPlaylistCode(h.activeName()) {
		return false
	}
	_, at, ok, err := h.storedSession()
	if err != nil || !ok {
		return false
	}
	return !h.now().Before(at.Add(h.sessionWindow))
}

// nextRevalidateAt reports when the stored code is next due for a re-check,
// for status to report. ok is false under the same conditions SessionDue
// documents, so an absent field means "no session to report", never a zero time.
func (h *SourceHandler) nextRevalidateAt() (time.Time, bool) {
	if h.sessionWindow <= 0 {
		return time.Time{}, false
	}
	if !source.NeedsPlaylistCode(h.activeName()) {
		return time.Time{}, false
	}
	_, at, ok, err := h.storedSession()
	if err != nil || !ok {
		return time.Time{}, false
	}
	return at.Add(h.sessionWindow), true
}

// SessionOutcome is what a revalidation did with the stored credential.
type SessionOutcome int

const (
	// SessionNotDue: the code is younger than the window, or there is none to
	// re-check, so nothing was attempted.
	SessionNotDue SessionOutcome = iota
	// SessionConfirmed: AdoboTV recognised the code, so the window was reset.
	SessionConfirmed
	// SessionEnded: the code was definitively rejected, so it was cleared and
	// the live source reopened from what remains.
	SessionEnded
	// SessionUnchanged: the re-check was inconclusive — a transient failure that
	// says nothing about the code — so the credential was left exactly as it was.
	SessionUnchanged
)

// String renders the outcome for a log line.
func (o SessionOutcome) String() string {
	switch o {
	case SessionConfirmed:
		return "confirmed"
	case SessionEnded:
		return "ended"
	case SessionUnchanged:
		return "unchanged (transient)"
	default:
		return "not due"
	}
}

// RevalidateSession re-checks an aged stored code against AdoboTV and decides
// what that means for the session. It is the silent weekly re-check the owner
// asked for, and it is honest about its name: nothing upstream expires, so this
// is not a session that times out — it is a re-check that the credential still
// works.
//
// The safety property is absolute: a transient failure must never log the user
// out. The set of outcomes that *prove a code invalid* is the mirror of the set
// that proves it valid (playlistCodeProvenValid), and only such a definitive
// rejection clears the credential. An unreachable AdoboTV, a timeout, a 5xx, an
// unparseable response, a pending device or a lapsed subscription all leave the
// stored code exactly where it is, because none of them says anything about
// whether the code is good. Getting this backwards would silently destroy the
// user's credential on a network blip, which is worse than any friction the
// session removes.
//
// A code that still works — or one AdoboTV accepted but gated for some other
// reason — resets the window, so the user never sees the re-check.
func (h *SourceHandler) RevalidateSession(ctx context.Context) SessionOutcome {
	if h.sessionWindow <= 0 {
		return SessionNotDue
	}
	if !source.NeedsPlaylistCode(h.activeName()) {
		return SessionNotDue
	}
	code, storedAt, ok, err := h.storedSession()
	if err != nil {
		log.Printf("source session: reading the stored code: %v", err)
		return SessionUnchanged
	}
	if !ok {
		return SessionNotDue
	}
	if h.now().Before(storedAt.Add(h.sessionWindow)) {
		return SessionNotDue
	}

	candidate, err := h.open(h.configWithCode(code))
	if err != nil {
		// The adapter could not even be built, which is a local fault, not a
		// verdict on the code. Leave the credential alone.
		log.Printf("source session: reopening %q to re-check the stored code: %v", adobotvhttp.Name, err)
		return SessionUnchanged
	}

	validationErr := validatePlaylistCode(ctx, candidate)
	switch {
	case validationErr == nil, playlistCodeProvenValid(validationErr):
		// AdoboTV recognised the code. A gate outside the code's control (a
		// device awaiting approval, a lapsed subscription) still proves the
		// credential itself good, so the window resets exactly as on a clean
		// success and the user is not asked to retype anything.
		return h.confirmSession(code)
	case playlistCodeProvenInvalid(validationErr):
		h.endSession()
		return SessionEnded
	default:
		log.Printf("source session: re-checking the stored code: %v (leaving the credential untouched)", validationErr)
		return SessionUnchanged
	}
}

// confirmSession re-Saves the stored code to reset the revalidation window's
// clock. The code is unchanged; only the file's timestamp moves, which is what
// the window is measured from. A failed re-Save leaves the previous timestamp
// in place, so the code is simply re-checked again rather than being trusted
// longer than intended.
func (h *SourceHandler) confirmSession(code string) SessionOutcome {
	if err := h.codes.Save(code); err != nil {
		log.Printf("source session: resetting the re-check window: %v", err)
		return SessionUnchanged
	}
	log.Printf("[source] the stored playlist code was re-confirmed by AdoboTV; the re-check window has been reset")
	return SessionConfirmed
}

// endSession clears a stored code that AdoboTV definitively rejected and
// reopens the live source from what remains — the environment fallback if one
// is set, otherwise the adapter with no code. It deliberately mirrors
// DeletePlaylistCode rather than clearing the remembered mode: the user still
// wants the login path, they have simply lost its credential, so the next thing
// they see is the code-entry gate rather than the two-way chooser. It is the one
// path that ends a session, and it runs only for a rejection that names the code
// itself. A failure to reopen is logged and leaves the server running whatever
// it had, rather than half-swapped.
func (h *SourceHandler) endSession() {
	if err := h.codes.Clear(); err != nil {
		log.Printf("source session: clearing the rejected code: %v", err)
		return
	}
	log.Printf("[source] AdoboTV rejected the stored playlist code; the session ended and the code was cleared")

	if h.activeName() != "" && source.NeedsPlaylistCode(h.activeName()) {
		reopened, err := h.openMode(h.activeName())
		if err != nil {
			log.Printf("source session: reopening after a rejected code: %v", err)
			return
		}
		h.player.SwapSource(reopened)
	}
}

// CanSync reports whether the active source has an upstream library to refresh.
// It is the capability check a caller makes BEFORE asking: the midnight
// scheduler uses it so an imported playlist or a sourceless server is not asked
// to sync at all, rather than being asked once a day and reported as failing.
// The manual endpoint does not need it — a user's explicit request is answered
// with ErrSyncUnsupported — but the background path must stay quiet about a
// state that is entirely normal.
func (h *SourceHandler) CanSync() bool {
	src := h.player.src()
	if src == nil {
		return false
	}
	_, ok := src.(source.SyncProvider)
	return ok
}

// SyncNow refreshes the active source's library ahead of use. It is the manual
// "sync now" path and the mechanism the midnight scheduler calls. A source with
// no upstream library returns ErrSyncUnsupported rather than a silent success,
// so the client is told plainly that an imported playlist has nothing to sync.
func (h *SourceHandler) SyncNow(ctx context.Context) error {
	src := h.player.src()
	if src == nil {
		return source.UnsupportedSyncError("")
	}
	provider, ok := src.(source.SyncProvider)
	if !ok {
		return source.UnsupportedSyncError(src.Name())
	}
	return provider.Refresh(ctx)
}

// SyncSource refreshes the active source on demand so the user can pull the
// newest library without waiting for the cache TTL or the midnight warm-up. A
// failure is a real answer to an explicit request — unlike the background
// refresh, which is logged and ignored — so it is mapped through the same
// classifier every other source error uses and its gate code reaches the client.
func (h *SourceHandler) SyncSource(c *gin.Context) {
	if err := h.SyncNow(c.Request.Context()); err != nil {
		writeSourceError(c, err, "")
		return
	}
	c.JSON(http.StatusOK, h.statusBody())
}

// sourceModeStatus is one entry in status's modes list. The client renders the
// user's choices from these flags rather than matching adapter names itself.
type sourceModeStatus struct {
	Name              string `json:"name"`
	Selectable        bool   `json:"selectable"`
	Dev               bool   `json:"dev"`
	Active            bool   `json:"active"`
	Configured        bool   `json:"configured"`
	NeedsPlaylistCode bool   `json:"needs_playlist_code"`
}

// modesStatus reports every registered adapter with the flags the UI needs to
// decide what to offer: whether it is a normal choice, whether it is a
// development harness, whether it is active, and whether the server has what it
// needs to open it.
func (h *SourceHandler) modesStatus(active string) []sourceModeStatus {
	names := source.Available()
	out := make([]sourceModeStatus, 0, len(names))
	for _, name := range names {
		out = append(out, sourceModeStatus{
			Name:              name,
			Selectable:        source.Selectable(name),
			Dev:               source.Dev(name),
			Active:            name == active,
			Configured:        h.modeConfigured(name),
			NeedsPlaylistCode: source.NeedsPlaylistCode(name),
		})
	}
	return out
}

// modeConfigured reports whether the server has what it needs to open the
// adapter registered under name: a playlist code for one that needs a code, a
// playlist file for one that reads a file. It is generic over the declared
// requirement, not a name check.
func (h *SourceHandler) modeConfigured(name string) bool {
	switch {
	case source.NeedsPlaylistCode(name):
		return h.codeConfigured()
	case source.NeedsPlaylistFile(name):
		return h.fileConfigured()
	case source.NeedsDatabase(name):
		// A database-backed adapter is configured when the server holds the
		// handle its requirement asks for, supplied at boot. Today the only such
		// adapter is the dev harness, which nothing renders; the field should
		// still describe it rather than hardcode false.
		return h.cfg.DB != nil
	default:
		return false
	}
}

// codeConfigured reports whether a playlist code is available — stored or in
// the environment. A malformed stored file is reported as not configured rather
// than an error: the status endpoint must always answer, and an unreadable
// credential is exactly the state the user re-enters one from.
func (h *SourceHandler) codeConfigured() bool {
	_, ok, err := h.codes.Resolve(h.envCode)
	if err != nil {
		log.Printf("source status: reading stored playlist code: %v", err)
		return false
	}
	return ok
}

// fileConfigured reports whether the file mode can be opened: an imported
// playlist is stored, or ADOBOFLIX_FILE_PATH supplies one.
func (h *SourceHandler) fileConfigured() bool {
	if h.files.Exists() {
		return true
	}
	return strings.TrimSpace(h.envFile) != ""
}

// origin reports where the active source came from. It answers even when the
// stored mode file is unreadable: the status endpoint must always answer, and
// the environment override is known without touching the store.
func (h *SourceHandler) origin() string {
	// With no live source nothing came from anywhere: report none even when a
	// mode is still remembered on disk. This is the state a stored mode the boot
	// could not open falls back to, and status must describe the sourceless
	// server it is actually running, not the choice that would not open.
	if h.activeName() == "" {
		return sourcemode.OriginNone
	}
	res, err := h.modes.Resolve(h.envSource)
	if err == nil {
		return res.Origin
	}
	log.Printf("source status: resolving source mode: %v", err)
	if h.pinned() {
		return sourcemode.OriginEnv
	}
	return sourcemode.OriginStored
}

// state reports whether the active source uses a playlist code and whether one
// is currently configured. Precedence lives in the store; this only asks it.
// It describes the active source, so a sourceless server reports (false, false)
// — the same shape a non-code source like file does.
func (h *SourceHandler) state() (needs bool, configured bool) {
	needs = source.NeedsPlaylistCode(h.activeName())
	if !needs {
		return false, false
	}
	return true, h.codeConfigured()
}

// pinned reports whether ADOBOFLIX_SOURCE pins the source. The mode-changing
// endpoints refuse while it does.
func (h *SourceHandler) pinned() bool { return strings.TrimSpace(h.envSource) != "" }

// activeName is the name of the live source, or "" when none is configured.
func (h *SourceHandler) activeName() string {
	src := h.player.src()
	if src == nil {
		return ""
	}
	return src.Name()
}

// openMode reopens the adapter registered under mode from the current stores,
// resolving its credential or playlist path by the same precedence the stores
// own. An empty mode opens the unconfigured source.
func (h *SourceHandler) openMode(mode string) (source.Source, error) {
	if mode == "" {
		return source.Unconfigured(), nil
	}
	cfg := h.cfg
	cfg.Name = mode
	if source.NeedsPlaylistCode(mode) {
		code, _, err := h.codes.Resolve(h.envCode)
		if err != nil {
			return nil, err
		}
		cfg.PlaylistCode = code
	}
	if source.NeedsPlaylistFile(mode) {
		if p := h.files.Path(); p != "" {
			cfg.FilePath = p
		}
	}
	return h.open(cfg)
}

// configWithCode is the base configuration with an explicit playlist code for
// the login path. The code is never held on the handler: it is supplied per
// open and lives only inside the adapter being built.
func (h *SourceHandler) configWithCode(code string) source.Config {
	cfg := h.cfg
	cfg.Name = adobotvhttp.Name
	cfg.PlaylistCode = code
	return cfg
}

// configWithFile is the base configuration pointed at an explicit playlist path
// for the file mode.
func (h *SourceHandler) configWithFile(path string) source.Config {
	cfg := h.cfg
	cfg.Name = file.Name
	cfg.FilePath = path
	return cfg
}

// importRejectionMessage turns an adapter parse failure into the message the
// importing user reads. The adapter wraps a malformed playlist as
// "file: malformed playlist file: <path>: <cause>", where <path> is a scratch
// file the user never chose; this names the format and the parser's own reason
// instead, and points at the documented format. The parser's reason is what the
// owner asked for: "if they followed the json format correctly" is only
// checkable if a wrong import says what was wrong.
//
// The named format is the one the content actually matches, not the one the
// adapter guessed. A file that matches neither shape — a bare list of URLs, say
// — is reported as matching neither rather than blamed on JSON, which would
// send the user to fix the wrong thing.
func importRejectionMessage(err error, tempPath string, data []byte) string {
	const guidance = "See the documented format in docs/source-adapters.md."

	cause := err.Error()
	switch {
	case errors.Is(err, file.ErrMalformedLibrary):
		cause = strings.TrimPrefix(cause, file.ErrMalformedLibrary.Error()+": ")
		cause = strings.TrimPrefix(cause, tempPath+": ")
		format, recognized := playlistfile.SniffFormat(data)
		if !recognized {
			return fmt.Sprintf("the playlist did not match the JSON envelope or M3U format AdoboFlix reads. %s", guidance)
		}
		label := "JSON"
		if format == playlistfile.FormatM3U {
			label = "M3U"
		}
		return fmt.Sprintf("the playlist could not be parsed as %s: %s. %s", label, cause, guidance)
	case errors.Is(err, file.ErrUnsupportedFormat):
		cause = strings.TrimPrefix(cause, file.ErrUnsupportedFormat.Error()+": ")
		return fmt.Sprintf("the playlist's format was not recognized: %s. %s", cause, guidance)
	default:
		return fmt.Sprintf("the playlist could not be read: %s %s", cause, guidance)
	}
}

// validatePlaylistCode exercises a candidate adapter with one cheap real read,
// so a rejected code fails here rather than on first playback. ListChannels is
// the cheapest Source call that forces adobotv-http to fetch the playlist
// envelope: exactly one upstream request, against the endpoint that enforces
// the code, device and User-Agent gates. Its error is returned unchanged, so
// the caller maps it to the same stable code the rest of the API uses.
//
// The context bounds that upstream request wherever the adapter supports it
// (source.ContextChannelLister), so cancelling it aborts the fetch rather than
// leaving the caller — a request handler or the boot re-check — waiting out the
// adapter's HTTP timeout. A cancelled read surfaces as a transport failure,
// which the classification reads as inconclusive: it neither confirms nor
// rejects the code, so it never ends a session.
func validatePlaylistCode(ctx context.Context, candidate source.Source) error {
	if lister, ok := candidate.(source.ContextChannelLister); ok {
		_, _, err := lister.ListChannelsContext(ctx, "", 1, 0)
		return err
	}
	_, _, err := candidate.ListChannels("", 1, 0)
	return err
}

// playlistCodeProvenValid reports whether a validation error still proves the
// submitted code itself valid. That, not "did the call succeed", is what
// decides whether the code is kept.
//
// The distinction is where in AdoboTV's gate order the failure sits. A gate
// that runs only after the code has been resolved — a device awaiting
// approval, a lapsed subscription — means AdoboTV recognised the code and
// identified the subscriber, and the gate clears later without the user
// retyping anything, so the code is worth keeping. A rejection, an
// unparseable 2xx, or a transport failure has told us nothing reliable about
// the code (or told us it is bad), so nothing is persisted. When it is
// genuinely ambiguous, the default is NOT to persist: making the user re-enter
// a code is a smaller harm than a bad code sticking and failing every later
// request.
//
// Only a few of these are reachable through the current validation call, which
// is a single playlist-envelope fetch. The rest are classified anyway so the
// rule is complete and survives a future change to what validation exercises.
func playlistCodeProvenValid(err error) bool {
	switch {
	case errors.Is(err, adobotvhttp.ErrDevicePending):
		// The device gate runs after the code is resolved: AdoboTV served the
		// splash for a code it accepted. Clears on operator approval.
		return true
	case errors.Is(err, adobotvhttp.ErrSubscriptionInactive):
		// The playlist endpoint admits inactive/expired accounts, so a refusal
		// here is the account state, not the code; the code identified the
		// subscriber.
		return true
	case errors.Is(err, adobotvhttp.ErrPlaylistFormatM3U):
		// The endpoint answered with this account's own playlist, so the code
		// was accepted; the m3u output format is an upstream account setting.
		return true
	case errors.Is(err, adobotvhttp.ErrTokenRejected):
		// The playlist was served (the code was accepted) and only a token
		// minted with it lapsed; the next fetch mints a fresh one.
		return true
	case errors.Is(err, adobotvhttp.ErrContentNotFound):
		// The playlist was served; only a specific item was missing.
		return true
	default:
		// playlist_rejected (the code itself was refused), user_agent_rejected
		// (it is unclear whether the allowlist is checked before the code
		// lookup, so it does not reliably prove the code valid — and its remedy
		// is a client-config change, not re-entry), the malformed-* cases (an
		// unparseable body proves nothing), upstream_error (an unreachable or
		// failing upstream said nothing), and anything unknown.
		return false
	}
}

// playlistCodeProvenInvalid reports whether a validation failure definitively
// proves the submitted code itself invalid — the only outcome that may end a
// session. This is the mirror of playlistCodeProvenValid during revalidation.
//
// It is deliberately NOT written as !playlistCodeProvenValid, because the
// complement of "proven valid" is "not proven valid", which is a strictly
// larger set than "proven invalid". A malformed body, a transport failure or an
// unknown error is not proven valid, but it is also not proven invalid: it says
// nothing about the code, so it must leave the credential alone. Only a
// rejection that names the code itself is a verdict. Keeping this an explicit
// allowlist rather than a negation is what stops a network blip from being
// mistaken for a bad credential — the failure mode that would silently destroy
// the user's session.
func playlistCodeProvenInvalid(err error) bool {
	// playlist_rejected is /v1/playlist/:code refusing the code outright
	// (HTTP 401/403). It is the one outcome that identifies the credential, not
	// the account state, as the problem.
	return errors.Is(err, adobotvhttp.ErrPlaylistRejected)
}

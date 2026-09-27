package handler

import (
	"net/url"
	"strings"
	"sync"
)

// credentialStore keeps stream credentials on the server instead of handing
// them to the browser.
//
// Some AdoboTV streams carry HTTP Basic credentials that the adapter re-embeds
// in the stream URL's userinfo (see injectUserInfo in
// internal/source/adobotvhttp/drm.go). Resolve used to wrap that URL verbatim
// into /api/v1/proxy?url=…, so devtools showed the subscriber "user:pass" in
// plain text. Resolve now strips the userinfo before building the proxy URL
// and stashes it here; the proxy attaches it as an Authorization header when it
// talks to the origin. The credentials never reach the page.
//
// Keyed by origin (scheme://host) rather than by full URL on purpose: a player
// derives segment URLs from the manifest client-side, so the proxy is asked for
// URLs resolve never saw. Those segments live on the manifest's origin, which
// means an origin-keyed entry authenticates them too — something the old
// userinfo-in-the-manifest-URL scheme never did.
//
// Consequence of that choice: two streams on the SAME origin with DIFFERENT
// credentials collide, and the most recently resolved one wins. AdoboFlix is
// single-subscriber (see docs/source-adapters.md), so one origin means one set
// of credentials in practice.
//
// This does NOT hide the upstream CDN URL itself, which is still visible in the
// ?url= parameter. Closing that needs a manifest-rewriting proxy; the residual
// exposure is documented in docs/source-adapters.md.
type credentialStore struct {
	mu       sync.RWMutex
	byOrigin map[string]basicAuth
}

type basicAuth struct {
	user string
	pass string
}

func newCredentialStore() *credentialStore {
	return &credentialStore{byOrigin: map[string]basicAuth{}}
}

// stash removes any userinfo from rawURL, records it under the URL's origin,
// and returns the URL safe to hand to the browser. A URL with no userinfo, or
// one that does not parse, is returned unchanged and records nothing.
func (s *credentialStore) stash(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.User == nil {
		return rawURL
	}
	user := u.User.Username()
	pass, _ := u.User.Password()
	if user == "" && pass == "" {
		// Empty userinfo ("http://@host/"): nothing to keep, but still strip
		// it so the browser is not handed a stray "@".
		u.User = nil
		return u.String()
	}

	origin := originOf(u)
	s.mu.Lock()
	s.byOrigin[origin] = basicAuth{user: user, pass: pass}
	s.mu.Unlock()

	u.User = nil
	return u.String()
}

// lookup returns the credentials recorded for rawURL's origin. Credentials
// carried by rawURL itself win: the proxy may be called directly with a URL
// resolve never produced, and that caller's userinfo is more specific than
// anything stashed.
func (s *credentialStore) lookup(rawURL string) (basicAuth, bool) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return basicAuth{}, false
	}
	if u.User != nil {
		user := u.User.Username()
		pass, _ := u.User.Password()
		if user != "" || pass != "" {
			return basicAuth{user: user, pass: pass}, true
		}
	}

	s.mu.RLock()
	creds, ok := s.byOrigin[originOf(u)]
	s.mu.RUnlock()
	return creds, ok
}

// stripUserInfo returns rawURL with any userinfo removed, so the proxy never
// re-sends credentials in the request line even when its caller supplied them
// there. An unparseable URL is returned unchanged; the request will fail on its
// own terms rather than here.
func stripUserInfo(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.User == nil {
		return rawURL
	}
	u.User = nil
	return u.String()
}

// originOf is the credential key: scheme://host, lowercased, port included.
func originOf(u *url.URL) string {
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host)
}

// Command verifyplayback answers one question: can the player actually fetch
// and play the content in the library?
//
// It samples assets across the four content shapes of the AdoboTV library
// (VOD DASH+Clearkey, VOD HLS no-DRM, VOD HLS+Clearkey, Episode HLS no-DRM),
// exercises each sample through the REAL server path — /api/v1/resolve (or
// /api/v1/resolve/episode/:id) followed by GETs against /api/v1/proxy — and
// verifies the manifest content (not just the status code) plus the first
// bytes of at least one segment. For Clearkey samples it reports whether
// drm_k and license_url came back present; it never attempts decryption.
//
// Strictly read-only: the only database statements issued are SELECTs against
// vod_assets and episodes. Nothing is ever written upstream; results go to
// stdout and a local JSON report file.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/jmoiron/sqlx"
	"github.com/joho/godotenv"
	_ "github.com/lib/pq"
)

// The four content shapes measured against the real database.
const (
	shapeVODDASHClearkey = "vod-dash-clearkey"
	shapeVODHLSClear     = "vod-hls-clear"
	shapeVODHLSClearkey  = "vod-hls-clearkey"
	shapeEpisodeHLSClear = "episode-hls-clear"
)

const (
	manifestMaxBytes = 16 << 20 // cap for manifest / playlist bodies
	segmentPeekBytes = 4 << 10  // first bytes requested for a segment
	errorSnippetLen  = 200      // error body excerpt for failure reasons
)

// shapeSpec describes one content shape and its SELECT-only sampling query.
type shapeSpec struct {
	Key        string
	Label      string
	Kind       string // "vod" or "episode"
	ExpectsDRM bool
	Query      string
}

// vodColumns is explicit (never SELECT *): the AdoboTV schema carries columns
// sqlx cannot scan into our struct. drm_type is the drm_tech ENUM, so it is
// cast to text for scanning.
const vodColumns = `id, name, drm_type::text AS drm_type,
	(drm_k IS NOT NULL) AS has_drm_k,
	(license_url IS NOT NULL) AS has_license_url, stream_url`

var shapes = []shapeSpec{
	{
		Key:        shapeVODDASHClearkey,
		Label:      "VOD DASH+Clearkey",
		Kind:       "vod",
		ExpectsDRM: true,
		Query: `SELECT ` + vodColumns + ` FROM vod_assets
			WHERE stream_url IS NOT NULL AND stream_url ~ '\.mpd'
			  AND (drm_type IS NOT NULL OR drm_k IS NOT NULL)
			ORDER BY random() LIMIT $1`,
	},
	{
		Key:   shapeVODHLSClear,
		Label: "VOD HLS no-DRM",
		Kind:  "vod",
		Query: `SELECT ` + vodColumns + ` FROM vod_assets
			WHERE stream_url IS NOT NULL AND stream_url ~ '\.m3u8'
			  AND drm_type IS NULL AND drm_k IS NULL
			ORDER BY random() LIMIT $1`,
	},
	{
		Key:        shapeVODHLSClearkey,
		Label:      "VOD HLS+Clearkey",
		Kind:       "vod",
		ExpectsDRM: true,
		Query: `SELECT ` + vodColumns + ` FROM vod_assets
			WHERE stream_url IS NOT NULL AND stream_url ~ '\.m3u8'
			  AND (drm_type IS NOT NULL OR drm_k IS NOT NULL)
			ORDER BY random() LIMIT $1`,
	},
	{
		Key:   shapeEpisodeHLSClear,
		Label: "Episode HLS no-DRM",
		Kind:  "episode",
		Query: `SELECT ` + vodColumns + ` FROM episodes
			WHERE stream_url IS NOT NULL AND stream_url ~ '\.m3u8'
			  AND drm_type IS NULL AND drm_k IS NULL
			ORDER BY random() LIMIT $1`,
	},
}

// sample is one asset picked by a sampling query.
type sample struct {
	ID         string  `db:"id"`
	Name       string  `db:"name"`
	DRMType    *string `db:"drm_type"`
	HasDRMK    bool    `db:"has_drm_k"`
	HasLicense bool    `db:"has_license_url"`
	StreamURL  string  `db:"stream_url"`
	Shape      string  `db:"-"`
	ShapeLabel string  `db:"-"`
	Kind       string  `db:"-"`
	ExpectsDRM bool    `db:"-"`
}

// resolvedStream mirrors the JSON of /api/v1/resolve.
type resolvedStream struct {
	URL        string `json:"url"`
	Provider   string `json:"provider"`
	DRMType    string `json:"drm_type"`
	DRMK       string `json:"drm_k"`
	LicenseURL string `json:"license_url"`
	UserAgent  string `json:"user_agent"`
	Referer    string `json:"referer"`
}

type drmReport struct {
	Type              string `json:"drm_type"`
	DRMKPresent       bool   `json:"drm_k_present"`
	LicenseURLPresent bool   `json:"license_url_present"`
}

type assetResult struct {
	ID                  string     `json:"id"`
	Name                string     `json:"name"`
	Shape               string     `json:"shape"`
	ShapeLabel          string     `json:"shape_label"`
	Kind                string     `json:"kind"`
	OK                  bool       `json:"ok"`
	FailureReason       string     `json:"failure_reason,omitempty"`
	ResolveStatus       int        `json:"resolve_status"`
	ManifestOK          bool       `json:"manifest_ok"`
	ManifestStatus      int        `json:"manifest_status"`
	ManifestContentType string     `json:"manifest_content_type,omitempty"`
	ManifestProxyURL    string     `json:"manifest_proxy_url,omitempty"`
	SegmentOK           bool       `json:"segment_ok"`
	SegmentStatus       int        `json:"segment_status"`
	SegmentContentType  string     `json:"segment_content_type,omitempty"`
	SegmentProxyURL     string     `json:"segment_proxy_url,omitempty"`
	DRM                 *drmReport `json:"drm"`
}

type shapeSummary struct {
	Key            string `json:"key"`
	Label          string `json:"label"`
	Sampled        int    `json:"sampled"`
	ManifestOK     int    `json:"manifest_ok"`
	SegmentOK      int    `json:"segment_ok"`
	DRMKPresent    int    `json:"drm_k_present"`
	LicenseURLPres int    `json:"license_url_present"`
	ExpectsDRM     bool   `json:"expects_drm"`
	Failures       int    `json:"failures"`
}

type report struct {
	GeneratedAt    string         `json:"generated_at"`
	Server         string         `json:"server"`
	SamplePerShape int            `json:"sample_per_shape"`
	Workers        int            `json:"workers"`
	Timeout        string         `json:"timeout"`
	AllOK          bool           `json:"all_ok"`
	Shapes         []shapeSummary `json:"shapes"`
	Assets         []assetResult  `json:"assets"`
}

// --- manifest parsing -------------------------------------------------------

var (
	hlsMapURIRe  = regexp.MustCompile(`#EXT-X-MAP:[^\r\n]*URI="([^"]+)"`)
	tmplNumberRe = regexp.MustCompile(`\$Number(%[^$]*)?\$`)
	tmplTimeRe   = regexp.MustCompile(`\$Time(%[^$]*)?\$`)
	tmplRepIDRe  = regexp.MustCompile(`\$RepresentationID(%[^$]*)?\$`)
	tmplOtherRe  = regexp.MustCompile(`\$[^$\s"']+\$`)
	fileExtRe    = regexp.MustCompile(`\.[A-Za-z0-9]{1,8}$`)
)

// errNoSegmentCoords marks a failure to derive segment coordinates from a DASH
// manifest — a limitation of this harness, not an upstream error. It is
// surfaced verbatim in the report so a segment reference this harness cannot
// evaluate is never mistaken for a genuine CDN 403/502.
var errNoSegmentCoords = errors.New("could not derive segment coordinates")

// decodeMPD decodes a DASH manifest, tolerating the encodings real MPDs
// declare. encoding/xml refuses any declared encoding but UTF-8 unless a
// CharsetReader is supplied, and MPDs in this library declare us-ascii.
func decodeMPD(body []byte) (*mpdDoc, error) {
	dec := xml.NewDecoder(bytes.NewReader(body))
	dec.CharsetReader = dashCharsetReader
	var doc mpdDoc
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	return &doc, nil
}

// dashCharsetReader supplies the missing CharsetReader. ASCII is a subset of
// UTF-8 so those labels pass through untouched; single-byte Western code pages
// are transcoded. Anything else is rejected rather than silently mis-decoded.
func dashCharsetReader(label string, input io.Reader) (io.Reader, error) {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "us-ascii", "ascii", "utf-8", "utf8":
		return input, nil
	case "iso-8859-1", "latin1", "windows-1252", "cp1252":
		raw, err := io.ReadAll(input)
		if err != nil {
			return nil, err
		}
		var sb strings.Builder
		sb.Grow(len(raw))
		for _, c := range raw {
			if c < 0x80 {
				sb.WriteByte(c)
			} else {
				sb.WriteRune(rune(c))
			}
		}
		return strings.NewReader(sb.String()), nil
	default:
		return nil, fmt.Errorf("unsupported manifest encoding %q", label)
	}
}

// Segment coordinates ($Number$, $Time$) must come from the SAME element that
// carries the template being substituted. Borrowing a startNumber or a
// SegmentTimeline time from another Representation/AdaptationSet yields a URL
// for a segment that does not exist, and makes a healthy stream look broken.
// Real XML decoding is the honest way to keep that scoping; regex-over-XML
// could not. These types model only the subset the harness needs.

type mpdDoc struct {
	BaseURLs []string    `xml:"BaseURL"`
	Periods  []mpdPeriod `xml:"Period"`
}

type mpdPeriod struct {
	BaseURLs    []string            `xml:"BaseURL"`
	Template    *mpdSegmentTemplate `xml:"SegmentTemplate"`
	SegmentList *mpdSegmentList     `xml:"SegmentList"`
	Adaptations []mpdAdaptationSet  `xml:"AdaptationSet"`
}

type mpdAdaptationSet struct {
	BaseURLs    []string            `xml:"BaseURL"`
	Template    *mpdSegmentTemplate `xml:"SegmentTemplate"`
	SegmentList *mpdSegmentList     `xml:"SegmentList"`
	Reps        []mpdRepresentation `xml:"Representation"`
}

type mpdRepresentation struct {
	ID          string              `xml:"id,attr"`
	BaseURLs    []string            `xml:"BaseURL"`
	Template    *mpdSegmentTemplate `xml:"SegmentTemplate"`
	SegmentList *mpdSegmentList     `xml:"SegmentList"`
}

type mpdSegmentTemplate struct {
	Media          string              `xml:"media,attr"`
	Initialization string              `xml:"initialization,attr"`
	StartNumber    *int64              `xml:"startNumber,attr"`
	Timeline       *mpdSegmentTimeline `xml:"SegmentTimeline"`
}

type mpdSegmentTimeline struct {
	S []mpdSegment `xml:"S"`
}

type mpdSegment struct {
	T *int64 `xml:"t,attr"`
}

type mpdSegmentList struct {
	StartNumber    *int64             `xml:"startNumber,attr"`
	Initialization *mpdInitialization `xml:"Initialization"`
	SegmentURLs    []mpdSegmentURL    `xml:"SegmentURL"`
}

type mpdInitialization struct {
	SourceURL string `xml:"sourceURL,attr"`
}

type mpdSegmentURL struct {
	Media string `xml:"media,attr"`
}

// segmentCoords are the manifest-derived values a template is filled from.
type segmentCoords struct {
	startNumber int64
	time        int64
	repID       string
}

// startNumberAttr applies DASH's default of 1 only when the attribute is
// genuinely absent.
func startNumberAttr(v *int64) int64 {
	if v == nil {
		return 1
	}
	return *v
}

// firstTime applies DASH's default of 0 only when the first <S> has no t.
func (tl *mpdSegmentTimeline) firstTime() int64 {
	if tl == nil || len(tl.S) == 0 || tl.S[0].T == nil {
		return 0
	}
	return *tl.S[0].T
}

func templateCoords(t *mpdSegmentTemplate, repID string) segmentCoords {
	return segmentCoords{startNumber: startNumberAttr(t.StartNumber), time: t.Timeline.firstTime(), repID: repID}
}

func listCoords(l *mpdSegmentList, repID string) segmentCoords {
	return segmentCoords{startNumber: startNumberAttr(l.StartNumber), repID: repID}
}

// coalesce returns the first non-nil pointer, so a Representation inherits the
// nearest ancestor's SegmentTemplate/SegmentList exactly as a player resolves it.
func coalesce[T any](vals ...*T) *T {
	for _, v := range vals {
		if v != nil {
			return v
		}
	}
	return nil
}

// segmentScope is one segment-bearing element with the coordinates scoped to it.
type segmentScope struct {
	list  *mpdSegmentList
	tmpl  *mpdSegmentTemplate
	repID string
}

// scopes returns every segment-bearing element in document order, each with its
// inherited template/list and its own coordinates.
func (d *mpdDoc) scopes() []segmentScope {
	fallback := d.firstRepresentationID()
	var out []segmentScope
	for pi := range d.Periods {
		p := &d.Periods[pi]
		out = append(out, segmentScope{list: p.SegmentList, tmpl: p.Template, repID: fallback})
		for ai := range p.Adaptations {
			as := &p.Adaptations[ai]
			out = append(out, segmentScope{
				list:  coalesce(as.SegmentList, p.SegmentList),
				tmpl:  coalesce(as.Template, p.Template),
				repID: fallback,
			})
			for ri := range as.Reps {
				rep := &as.Reps[ri]
				repID := rep.ID
				if repID == "" {
					repID = fallback
				}
				out = append(out, segmentScope{
					list:  coalesce(rep.SegmentList, as.SegmentList, p.SegmentList),
					tmpl:  coalesce(rep.Template, as.Template, p.Template),
					repID: repID,
				})
			}
		}
	}
	return out
}

func (d *mpdDoc) baseURLsInOrder() []string {
	var out []string
	for _, b := range d.BaseURLs {
		out = append(out, strings.TrimSpace(b))
	}
	for _, p := range d.Periods {
		for _, b := range p.BaseURLs {
			out = append(out, strings.TrimSpace(b))
		}
		for _, as := range p.Adaptations {
			for _, b := range as.BaseURLs {
				out = append(out, strings.TrimSpace(b))
			}
			for _, rep := range as.Reps {
				for _, b := range rep.BaseURLs {
					out = append(out, strings.TrimSpace(b))
				}
			}
		}
	}
	return out
}

func (d *mpdDoc) firstRepresentationID() string {
	for _, p := range d.Periods {
		for _, as := range p.Adaptations {
			for _, rep := range as.Reps {
				if rep.ID != "" {
					return rep.ID
				}
			}
		}
	}
	return ""
}

// substituteTemplate fills the placeholders this harness can resolve from the
// manifest, honouring DASH's printf-style modifiers ($Number%05d$). Anything it
// cannot fill ($Bandwidth$, a $RepresentationID$ with no representation, ...)
// makes the candidate unusable — it never guesses a value that was not in the
// manifest.
func substituteTemplate(s string, c segmentCoords) (string, bool) {
	var ok bool
	if s, ok = replacePlaceholder(s, tmplNumberRe, c.startNumber); !ok {
		return "", false
	}
	if s, ok = replacePlaceholder(s, tmplTimeRe, c.time); !ok {
		return "", false
	}
	if c.repID != "" {
		if s, ok = replacePlaceholder(s, tmplRepIDRe, c.repID); !ok {
			return "", false
		}
	}
	if tmplOtherRe.MatchString(s) {
		return "", false
	}
	return s, true
}

// replacePlaceholder substitutes every match with val, applying the captured
// printf verb when present. It reports false if the verb cannot render val
// (e.g. %05d against a string) so the caller rejects the candidate instead of
// emitting a mangled URL.
func replacePlaceholder(s string, re *regexp.Regexp, val any) (string, bool) {
	bad := false
	out := re.ReplaceAllStringFunc(s, func(match string) string {
		verb := ""
		if m := re.FindStringSubmatch(match); len(m) > 1 {
			verb = m[1]
		}
		if verb == "" {
			return fmt.Sprint(val)
		}
		rendered := fmt.Sprintf(verb, val)
		if strings.Contains(rendered, "%!") {
			bad = true
		}
		return rendered
	})
	return out, !bad
}

// firstURI returns the first non-comment line of an HLS playlist (a variant or
// a segment reference), or "" if there is none.
func firstURI(body []byte) string {
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		return line
	}
	return ""
}

func extXMapURI(body []byte) string {
	if m := hlsMapURIRe.FindSubmatch(body); m != nil {
		return string(m[1])
	}
	return ""
}

// dashSegmentRef picks the first fetchable segment reference out of a DASH
// manifest: SegmentURL media or SegmentTemplate media first (real media
// segments, placeholders substituted), then Initialization / SegmentTemplate
// initialization, and finally a file-like BaseURL. base (when non-empty) is a
// directory-style BaseURL the reference must be resolved against first.
//
// Placeholders are filled from the element the reference came from, so
// coordinates never leak between Representations. When no reference can be
// derived it returns errNoSegmentCoords so the report can tell a harness
// limitation apart from a genuine upstream failure.
func dashSegmentRef(body []byte) (string, string, error) {
	doc, derr := decodeMPD(body)
	if derr != nil {
		return "", "", fmt.Errorf("%w: manifest is not decodable XML: %v", errNoSegmentCoords, derr)
	}
	scopes := doc.scopes()
	bases := doc.baseURLsInOrder()
	// Only a directory-style BaseURL acts as a resolution base. File-like
	// BaseURLs are representation-level (e.g. a sidecar subtitle) and must not
	// prefix other representations' segments.
	var base string
	for _, b := range bases {
		if strings.HasSuffix(b, "/") && !strings.Contains(b, "$") {
			base = b
			break
		}
	}

	// Pass 1 — prefer a real media segment over an initialization segment: the
	// most common real failure is a manifest that loads while its media segments
	// 403, so the media segment is the one worth fetching.
	for _, sc := range scopes {
		if sc.list != nil {
			c := listCoords(sc.list, sc.repID)
			for _, su := range sc.list.SegmentURLs {
				if s, ok := substituteTemplate(su.Media, c); ok && s != "" {
					return s, base, nil
				}
			}
		}
		if sc.tmpl != nil {
			if s, ok := substituteTemplate(sc.tmpl.Media, templateCoords(sc.tmpl, sc.repID)); ok && s != "" {
				return s, base, nil
			}
		}
	}

	// Pass 2 — initialization references.
	for _, sc := range scopes {
		if sc.list != nil && sc.list.Initialization != nil {
			if s, ok := substituteTemplate(sc.list.Initialization.SourceURL, listCoords(sc.list, sc.repID)); ok && s != "" {
				return s, base, nil
			}
		}
		if sc.tmpl != nil {
			if s, ok := substituteTemplate(sc.tmpl.Initialization, templateCoords(sc.tmpl, sc.repID)); ok && s != "" {
				return s, base, nil
			}
		}
	}

	// Last resort: a file-like BaseURL fetched on its own.
	for _, b := range bases {
		if strings.HasSuffix(b, "/") || strings.Contains(b, "$") {
			continue
		}
		if last := b[strings.LastIndex(b, "/")+1:]; fileExtRe.MatchString(last) {
			return b, "", nil
		}
	}
	return "", "", fmt.Errorf("%w: no usable SegmentURL/SegmentTemplate media, Initialization or file BaseURL", errNoSegmentCoords)
}

// --- content validation -----------------------------------------------------

func trimmedBody(body []byte) []byte {
	return bytes.TrimLeft(body, "\xef\xbb\xbf\r\n\t ")
}

func looksLikeHTML(body []byte) bool {
	t := trimmedBody(body)
	if len(t) == 0 {
		return false
	}
	l := bytes.ToLower(t)
	return bytes.HasPrefix(l, []byte("<!doctype html")) || bytes.HasPrefix(l, []byte("<html"))
}

func looksLikePlaylist(body []byte) bool {
	return bytes.HasPrefix(trimmedBody(body), []byte("#EXTM3U"))
}

func rootIsMPD(body []byte) bool {
	dec := xml.NewDecoder(bytes.NewReader(body))
	for {
		tok, err := dec.Token()
		if err != nil {
			return false
		}
		if se, ok := tok.(xml.StartElement); ok {
			return se.Name.Local == "MPD"
		}
	}
}

// validateManifest is content-based: a 200 carrying an HTML error page must
// fail here regardless of the status code.
func validateManifest(kind string, body []byte, contentType string) error {
	t := trimmedBody(body)
	if len(t) == 0 {
		return errors.New("empty manifest body")
	}
	if looksLikeHTML(body) {
		return fmt.Errorf("body is an HTML page, not a manifest (content-type %q)", contentType)
	}
	if kind == "dash" {
		if rootIsMPD(t) || bytes.Contains(t, []byte("<MPD")) {
			return nil
		}
		return errors.New("body is not a DASH manifest: no <MPD> root element")
	}
	if looksLikePlaylist(t) {
		return nil
	}
	first := firstLine(t)
	if len(first) > 60 {
		first = first[:60] + "..."
	}
	return fmt.Errorf("body does not start with #EXTM3U (first line %q)", first)
}

func firstLine(b []byte) string {
	if i := bytes.IndexAny(b, "\r\n"); i >= 0 {
		return string(b[:i])
	}
	return string(b)
}

func snippet(body []byte) string {
	s := strings.Join(strings.Fields(string(body)), " ")
	if len(s) > errorSnippetLen {
		s = s[:errorSnippetLen]
	}
	if s == "" {
		return "(empty body)"
	}
	return s
}

// badContentType reports content that is certainly not media bytes.
func badContentType(ct string) bool {
	l := strings.ToLower(ct)
	return strings.Contains(l, "text/html") || strings.Contains(l, "json")
}

// --- HTTP plumbing ----------------------------------------------------------

// newClient returns an http.Client with an explicit timeout — never use
// http.DefaultClient, which has none.
func newClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
}

func doGet(ctx context.Context, client *http.Client, rawURL string, hdr http.Header, maxBytes int64) (int, string, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, "", nil, err
	}
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes))
	if err != nil {
		return resp.StatusCode, resp.Header.Get("Content-Type"), body, err
	}
	return resp.StatusCode, resp.Header.Get("Content-Type"), body, nil
}

func cloneValues(v url.Values) url.Values {
	out := make(url.Values, len(v))
	for k, vs := range v {
		out[k] = append([]string(nil), vs...)
	}
	return out
}

// proxyOriginAndParams splits a proxied URL returned by resolve into its
// origin and the per-source query parameters (source/ua/ref) that must be
// preserved when fetching other resources through the same proxy.
func proxyOriginAndParams(proxyURL, base string) (string, url.Values, *url.URL, error) {
	u, err := url.Parse(proxyURL)
	if err != nil {
		return "", nil, nil, fmt.Errorf("parse proxy url: %w", err)
	}
	if !u.IsAbs() {
		b, berr := url.Parse(base)
		if berr != nil {
			return "", nil, nil, fmt.Errorf("resolve returned a relative proxy url: %v", berr)
		}
		u = b.ResolveReference(u)
	}
	params := cloneValues(u.Query())
	upstream := params.Get("url")
	params.Del("url")
	if upstream == "" {
		return "", nil, nil, errors.New("proxy url has no url parameter")
	}
	up, err := url.Parse(upstream)
	if err != nil {
		return "", nil, nil, fmt.Errorf("parse upstream manifest url: %w", err)
	}
	origin := u.Scheme + "://" + u.Host
	return origin, params, up, nil
}

// fetchUpstream fetches absURL through the proxy at origin, preserving the
// per-source params, optionally requesting only the first bytes via Range.
func fetchUpstream(ctx context.Context, client *http.Client, origin string, params url.Values, absURL, rangeHdr string, maxBytes int64) (proxyURL string, status int, ct string, body []byte, err error) {
	q := cloneValues(params)
	q.Set("url", absURL)
	proxyURL = origin + "/api/v1/proxy?" + q.Encode()
	var hdr http.Header
	if rangeHdr != "" {
		hdr = http.Header{"Range": []string{rangeHdr}}
	}
	status, ct, body, err = doGet(ctx, client, proxyURL, hdr, maxBytes)
	return proxyURL, status, ct, body, err
}

func isHLSURL(u *url.URL) bool {
	return strings.HasSuffix(strings.ToLower(u.Path), ".m3u8")
}

// --- verification -----------------------------------------------------------

// pingServer fails fast when the server is not reachable so the harness never
// silently passes against a dead endpoint.
func pingServer(ctx context.Context, client *http.Client, base string) error {
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(pctx, http.MethodGet, base+"/api/v1/stats", nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("GET %s/api/v1/stats returned 404 — -base may not point at an AdoboFlix server", base)
	}
	return nil
}

func verifyAsset(ctx context.Context, client *http.Client, base string, s sample) assetResult {
	res := assetResult{
		ID: s.ID, Name: s.Name, Shape: s.Shape, ShapeLabel: s.ShapeLabel,
		Kind: s.Kind, DRM: &drmReport{},
	}

	// a. Resolve through the real server path.
	var resolveURL string
	if s.Kind == "episode" {
		resolveURL = base + "/api/v1/resolve/episode/" + url.PathEscape(s.ID)
	} else {
		resolveURL = base + "/api/v1/resolve?id=" + url.QueryEscape(s.ID)
	}
	status, _, body, err := doGet(ctx, client, resolveURL, nil, 1<<20)
	res.ResolveStatus = status
	if err != nil {
		res.FailureReason = "resolve: " + err.Error()
		return res
	}
	if status != http.StatusOK {
		res.FailureReason = fmt.Sprintf("resolve: HTTP %d: %s", status, snippet(body))
		return res
	}
	var rs resolvedStream
	if err := json.Unmarshal(body, &rs); err != nil {
		res.FailureReason = "resolve: invalid JSON: " + err.Error()
		return res
	}
	if rs.URL == "" {
		res.FailureReason = "resolve: response has no url"
		return res
	}
	res.ManifestProxyURL = rs.URL
	res.DRM.Type = rs.DRMType
	res.DRM.DRMKPresent = strings.TrimSpace(rs.DRMK) != ""
	res.DRM.LicenseURLPresent = strings.TrimSpace(rs.LicenseURL) != ""

	// b. Fetch the manifest and validate its CONTENT, not just the status.
	kind := "hls"
	if s.Shape == shapeVODDASHClearkey {
		kind = "dash"
	}
	mStatus, mCT, mBody, err := doGet(ctx, client, rs.URL, nil, manifestMaxBytes)
	res.ManifestStatus = mStatus
	res.ManifestContentType = mCT
	if err != nil {
		res.FailureReason = "manifest: " + err.Error()
		return res
	}
	if mStatus < 200 || mStatus > 299 {
		res.FailureReason = fmt.Sprintf("manifest: HTTP %d: %s", mStatus, snippet(mBody))
		return res
	}
	if err := validateManifest(kind, mBody, mCT); err != nil {
		res.FailureReason = "manifest: " + err.Error()
		return res
	}
	res.ManifestOK = true

	// c. Parse a segment reference, resolve it against the manifest URL, and
	// fetch its first bytes through the proxy.
	segProxy, segStatus, segCT, err := fetchOneSegment(ctx, client, base, kind, rs.URL, mBody)
	res.SegmentStatus = segStatus
	res.SegmentContentType = segCT
	res.SegmentProxyURL = segProxy
	if err != nil {
		res.FailureReason = "segment: " + err.Error()
		return res
	}
	res.SegmentOK = true
	res.OK = true
	return res
}

// fetchOneSegment picks a segment reference out of the manifest, resolves it
// against the ORIGINAL upstream manifest URL (the same way the Shaka client
// does: it is given the CDN URL and its requests are re-wrapped through the
// proxy), and fetches its first bytes through /api/v1/proxy.
func fetchOneSegment(ctx context.Context, client *http.Client, base, kind, manifestProxyURL string, manifestBody []byte) (proxyURL string, status int, ct string, err error) {
	origin, params, upstream, err := proxyOriginAndParams(manifestProxyURL, base)
	if err != nil {
		return "", 0, "", err
	}

	if kind == "dash" {
		ref, refBase, derr := dashSegmentRef(manifestBody)
		if derr != nil {
			return "", 0, "", derr
		}
		resBase := upstream
		if refBase != "" {
			bu, berr := url.Parse(refBase)
			if berr != nil {
				return "", 0, "", fmt.Errorf("invalid BaseURL %q: %w", refBase, berr)
			}
			resBase = upstream.ResolveReference(bu)
		}
		ru, rerr := url.Parse(ref)
		if rerr != nil {
			return "", 0, "", fmt.Errorf("invalid segment reference %q: %w", ref, rerr)
		}
		abs := resBase.ResolveReference(ru)
		if strings.Contains(abs.String(), "$") {
			return "", 0, "", fmt.Errorf("%w: unresolved template placeholders in segment url %q", errNoSegmentCoords, abs.String())
		}
		return fetchAndCheckSegment(ctx, client, origin, params, abs)
	}

	// HLS: the first URI may be a variant playlist; descend at most two
	// levels before requiring a media segment.
	currentUpstream := upstream
	body := manifestBody
	for depth := 0; depth < 3; depth++ {
		ref := firstURI(body)
		if ref == "" {
			ref = extXMapURI(body)
		}
		if ref == "" {
			return "", 0, "", errors.New("no segment or variant URI found in HLS manifest")
		}
		ru, rerr := url.Parse(ref)
		if rerr != nil {
			return "", 0, "", fmt.Errorf("invalid URI %q: %w", ref, rerr)
		}
		abs := currentUpstream.ResolveReference(ru)

		rangeHdr := "bytes=0-" + strconv.Itoa(segmentPeekBytes-1)
		if isHLSURL(abs) {
			rangeHdr = "" // variant playlists are fetched whole
		}
		pURL, st, c, fb, ferr := fetchUpstream(ctx, client, origin, params, abs.String(), rangeHdr, manifestMaxBytes)
		if ferr != nil {
			return pURL, st, c, ferr
		}
		if st < 200 || st > 299 {
			return pURL, st, c, fmt.Errorf("HTTP %d: %s", st, snippet(fb))
		}
		if looksLikeHTML(fb) || badContentType(c) {
			return pURL, st, c, fmt.Errorf("got HTML/JSON instead of media (content-type %q): %s", c, snippet(fb))
		}
		if looksLikePlaylist(fb) {
			if depth == 2 {
				return pURL, st, c, errors.New("no media segment found: playlist nesting deeper than 3 levels")
			}
			currentUpstream = abs
			body = fb
			continue
		}
		return pURL, st, c, nil
	}
	return "", 0, "", errors.New("no media segment found")
}

func fetchAndCheckSegment(ctx context.Context, client *http.Client, origin string, params url.Values, abs *url.URL) (string, int, string, error) {
	pURL, st, c, fb, err := fetchUpstream(ctx, client, origin, params, abs.String(), "bytes=0-"+strconv.Itoa(segmentPeekBytes-1), manifestMaxBytes)
	if err != nil {
		return pURL, st, c, err
	}
	if st != http.StatusOK && st != http.StatusPartialContent {
		return pURL, st, c, fmt.Errorf("HTTP %d: %s", st, snippet(fb))
	}
	if looksLikeHTML(fb) || badContentType(c) {
		return pURL, st, c, fmt.Errorf("got HTML/JSON instead of segment bytes (content-type %q): %s", c, snippet(fb))
	}
	return pURL, st, c, nil
}

// --- reporting --------------------------------------------------------------

func buildSummaries(samples []sample, assets []assetResult) []shapeSummary {
	byKey := make(map[string]assetResult, len(assets))
	for _, a := range assets {
		byKey[resultKeyFor(a.Shape, a.ID)] = a
	}
	out := make([]shapeSummary, 0, len(shapes))
	for _, sh := range shapes {
		s := shapeSummary{Key: sh.Key, Label: sh.Label, ExpectsDRM: sh.ExpectsDRM}
		for _, sm := range samples {
			if sm.Shape != sh.Key {
				continue
			}
			s.Sampled++
			r, ok := byKey[resultKey(sm)]
			if !ok {
				s.Failures++
				continue
			}
			if sh.ExpectsDRM && r.DRM != nil {
				if r.DRM.DRMKPresent {
					s.DRMKPresent++
				}
				if r.DRM.LicenseURLPresent {
					s.LicenseURLPres++
				}
			}
			if !r.OK {
				s.Failures++
				continue
			}
			if r.ManifestOK {
				s.ManifestOK++
			}
			if r.SegmentOK {
				s.SegmentOK++
			}
		}
		out = append(out, s)
	}
	return out
}

func resultKey(s sample) string { return s.Shape + "\x00" + s.ID }

func resultKeyFor(shape, id string) string { return shape + "\x00" + id }

func printSummary(base string, n, workers int, timeout time.Duration, summaries []shapeSummary, assets []assetResult) {
	fmt.Println("AdoboFlix playback verification")
	fmt.Printf("  server:  %s\n", base)
	fmt.Printf("  sample:  %d per shape, %d workers, %s timeout per request\n\n", n, workers, timeout)

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "SHAPE\tSAMPLED\tMANIFEST OK\tSEGMENT OK\tDRM PRESENT\tFAILURES")
	for _, s := range summaries {
		drm := "-"
		if s.ExpectsDRM {
			// DRM PRESENT = Clearkey samples whose drm_k came back non-empty
			// (the key the ClearKey CDM needs). license_url presence is
			// broken out below and per-asset in the JSON report.
			drm = fmt.Sprintf("%d/%d", s.DRMKPresent, s.Sampled)
		}
		fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%s\t%d\n", s.Label, s.Sampled, s.ManifestOK, s.SegmentOK, drm, s.Failures)
	}
	w.Flush()
	for _, s := range summaries {
		if s.ExpectsDRM {
			fmt.Printf("  drm detail: %-20s drm_k %d/%d, license_url %d/%d\n",
				s.Label, s.DRMKPresent, s.Sampled, s.LicenseURLPres, s.Sampled)
		}
	}

	var failed []assetResult
	for _, a := range assets {
		if !a.OK {
			failed = append(failed, a)
		}
	}
	if len(failed) > 0 {
		fmt.Printf("\nFailures (%d):\n", len(failed))
		for _, a := range failed {
			fmt.Printf("  [%s] %s %q: %s (resolve=%d manifest=%d segment=%d)\n",
				a.ShapeLabel, a.ID, a.Name, a.FailureReason, a.ResolveStatus, a.ManifestStatus, a.SegmentStatus)
		}
	}
}

func writeReport(path string, rep *report) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	data, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

// --- main -------------------------------------------------------------------

func main() {
	var (
		n       = flag.Int("n", 5, "assets to sample per content shape")
		base    = flag.String("base", "http://127.0.0.1:5656", "AdoboFlix server base URL")
		out     = flag.String("out", "./playback-report.json", "path for the machine-readable JSON report")
		timeout = flag.Duration("timeout", 30*time.Second, "explicit timeout applied to every HTTP request")
		workers = flag.Int("workers", 4, "bounded concurrency worker pool size")
	)
	flag.Parse()
	if *n < 1 {
		*n = 1
	}
	if *workers < 1 {
		*workers = 1
	}
	*base = strings.TrimRight(*base, "/")

	if err := godotenv.Load(); err != nil {
		fmt.Fprintln(os.Stderr, "[env] no .env found, using environment variables")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client := newClient(*timeout)

	// Fail fast if the server is down — never silently pass.
	if err := pingServer(ctx, client, *base); err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: server unreachable at %s: %v\n", *base, err)
		fmt.Fprintln(os.Stderr, "Start it first (`make run` or `go run ./cmd/server`), then re-run `make verify-playback`.")
		os.Exit(1)
	}

	dsn := os.Getenv("ADOBOFLIX_PG_URL")
	if dsn == "" {
		dsn = os.Getenv("MPDUMPY_PG_URL")
	}
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "FATAL: ADOBOFLIX_PG_URL not set (put it in .env)")
		os.Exit(1)
	}
	database, err := sqlx.Connect("postgres", dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: database connection failed: %v\n", err)
		os.Exit(1)
	}
	defer database.Close()

	// Sample each shape with a SELECT-only query.
	var samples []sample
	for _, sh := range shapes {
		var rows []sample
		if err := database.Select(&rows, sh.Query, *n); err != nil {
			fmt.Fprintf(os.Stderr, "FATAL: sampling %s failed: %v\n", sh.Key, err)
			os.Exit(1)
		}
		for i := range rows {
			rows[i].Shape = sh.Key
			rows[i].ShapeLabel = sh.Label
			rows[i].Kind = sh.Kind
			rows[i].ExpectsDRM = sh.ExpectsDRM
		}
		samples = append(samples, rows...)
	}

	// Bounded concurrency worker pool.
	jobs := make(chan sample)
	results := make(chan assetResult, len(samples))
	var wg sync.WaitGroup
	for w := 0; w < *workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for s := range jobs {
				if ctx.Err() != nil {
					results <- assetResult{ID: s.ID, Name: s.Name, Shape: s.Shape, ShapeLabel: s.ShapeLabel, Kind: s.Kind, DRM: &drmReport{}, FailureReason: "cancelled: " + ctx.Err().Error()}
					continue
				}
				results <- verifyAsset(ctx, client, *base, s)
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, s := range samples {
			select {
			case <-ctx.Done():
				return
			case jobs <- s:
			}
		}
	}()
	wg.Wait()
	close(results)

	byKey := make(map[string]assetResult, len(samples))
	for r := range results {
		byKey[resultKeyFor(r.Shape, r.ID)] = r
	}
	assets := make([]assetResult, 0, len(samples))
	for _, s := range samples {
		r, ok := byKey[resultKey(s)]
		if !ok {
			reason := "not run"
			if err := ctx.Err(); err != nil {
				reason = "cancelled: " + err.Error()
			}
			r = assetResult{ID: s.ID, Name: s.Name, Shape: s.Shape, ShapeLabel: s.ShapeLabel, Kind: s.Kind, DRM: &drmReport{}, FailureReason: reason}
		}
		assets = append(assets, r)
	}

	summaries := buildSummaries(samples, assets)
	printSummary(*base, *n, *workers, *timeout, summaries, assets)

	allOK := true
	for _, s := range summaries {
		if s.Failures > 0 || s.Sampled == 0 {
			allOK = false
		}
	}
	rep := &report{
		GeneratedAt:    time.Now().UTC().Format(time.RFC3339),
		Server:         *base,
		SamplePerShape: *n,
		Workers:        *workers,
		Timeout:        timeout.String(),
		AllOK:          allOK,
		Shapes:         summaries,
		Assets:         assets,
	}
	if err := writeReport(*out, rep); err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: writing report: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\nJSON report written to %s\n", *out)

	if ctx.Err() != nil {
		fmt.Fprintln(os.Stderr, "interrupted")
		os.Exit(1)
	}
}

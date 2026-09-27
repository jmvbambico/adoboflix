package file

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/jmvbambico/adoboflix/internal/source"
)

// The M3U parser is deliberately liberal. Real playlists in the wild are
// hand-edited, exporter-written and inconsistent: attribute order changes,
// quoting changes, directives appear that no spec defines, lines end in CRLF,
// and the `#EXTM3U` header is sometimes missing. Everything here is chosen so
// the common malformations load rather than fail, and everything this parser
// drops is counted so the drop is visible in the startup summary instead of
// being silent.
//
// It maps an M3U playlist onto the internal library by inference, not by a
// translation the format actually carries:
//
//   - An entry whose group reads as VOD (see groupIsVOD) becomes a VOD row.
//     A title with a parseable season/episode becomes an episode of a series,
//     grouped with its siblings under one VOD entry; a VOD entry with no
//     parseable season/episode is still a VOD row, filed as a movie.
//   - Every other entry becomes a live channel with a single stream.
//
// The inference is guesswork and will misfile some content. That is an accepted
// cost of mapping a format with no type system, which is why m3uSummary (logged
// at startup) reports what the classifier actually did.

// supportedFormats is the closed set of extensions this adapter parses, named
// in ErrUnsupportedFormat so a user who pointed at the wrong file is told what
// to point at instead.
const supportedFormats = ".json, .m3u, .m3u8"

// The extensions the format selector understands. The parser is chosen by
// extension and never by content: sniffing is ambiguous (a body can be valid
// twice, and a hostile or merely misnamed file can look like the other format),
// and a malformed playlist must report as malformed in the format its extension
// selected rather than silently falling back to the other parser.
const (
	formatJSON = ".json"
	formatM3U  = ".m3u"
	formatM3U8 = ".m3u8"
)

// defaultLabel is the stream label given to every synthesised stream. M3U
// carries no notion of a labelled stream, so a single default per entry is the
// honest translation, and it keeps ResolveChannelStream's default-first rule
// meaningful.
const defaultLabel = "default"

// m3uEntry is one `#EXTINF` and the URL that follows it.
type m3uEntry struct {
	attrs map[string]string
	title string
	url   string
}

// displayName is the name a user would recognise: the `#EXTINF` display title,
// else the `tvg-name` attribute, else the URL as a last resort (a titled entry
// with a URL is never dropped just because both titles are empty).
func (e m3uEntry) displayName() string {
	if t := strings.TrimSpace(e.title); t != "" {
		return t
	}
	if n := strings.TrimSpace(e.attrs["tvg-name"]); n != "" {
		return n
	}
	return e.url
}

// m3uSummary is the audit trail for the classification above. It is filled in
// as the playlist is parsed and printed once at startup, one line per category,
// so a user who cannot find a title can tell from the log where it went.
type m3uSummary struct {
	entries   int      // `#EXTINF` directives read
	dropped   int      // `#EXTINF` with no following URL
	bareURLs  int      // URL lines with no preceding `#EXTINF`
	channels  int      // rows classified as live channels
	vodRows   int      // rows classified as VOD (episodes + movies)
	series    int      // distinct series VOD entries
	movies    int      // VOD rows with no parseable season/episode
	vodGroups []string // group names treated as VOD, deduped and sorted
}

// logLines renders the summary as the few lines the server logs at startup.
func (s *m3uSummary) logLines(path string) []string {
	groups := "(none)"
	if len(s.vodGroups) > 0 {
		groups = strings.Join(s.vodGroups, ", ")
	}
	return []string{
		fmt.Sprintf("[source] m3u: read %d entries from %s (dropped %d: %d #EXTINF without a URL, %d bare URLs without #EXTINF)",
			s.entries, path, s.dropped+s.bareURLs, s.dropped, s.bareURLs),
		fmt.Sprintf("[source] m3u: %d live channels", s.channels),
		fmt.Sprintf("[source] m3u: %d VOD rows across %d series", s.vodRows, s.series),
		fmt.Sprintf("[source] m3u: %d VOD titles had no parseable season/episode (filed as movies)", s.movies),
		fmt.Sprintf("[source] m3u: VOD group names: %s", groups),
	}
}

// parseM3U turns an M3U/M3U8 playlist into an indexed library plus the summary
// of what the classification did. The library is built through buildLibrary, so
// M3U rows get the same derived-id reservation and normalisation as id-less
// JSON rows without a second, drift-prone implementation.
func parseM3U(data []byte) (*library, *m3uSummary, error) {
	entries, summary, err := parseM3UEntries(data)
	if err != nil {
		return nil, nil, err
	}
	file := buildM3ULibrary(entries, &summary)
	return buildLibrary(file), &summary, nil
}

// parseM3UEntries scans the playlist into entries, tolerating the malformations
// listed in the package comment. It returns only entries that have a URL; the
// ones it drops are counted in the summary. A non-empty file that is neither
// headed by `#EXTM3U` nor contains any `#EXTINF` is not a playlist and is an
// error, so a JSON file renamed `.m3u` reports as a malformed M3U rather than
// loading as an empty library.
func parseM3UEntries(data []byte) ([]m3uEntry, m3uSummary, error) {
	var summary m3uSummary
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF}) // tolerate a UTF-8 BOM

	scanner := bufio.NewScanner(bytes.NewReader(data))
	// Playlists carry logo URLs and tokens in their attributes, so lines can be
	// long; 4 MiB is far past anything real and turns a runaway line into a
	// clean error instead of a silent truncation.
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	var entries []m3uEntry
	var pending *m3uEntry
	sawHeader := false
	sawExtinf := false

	for scanner.Scan() {
		// ScanLines already strips a trailing CR, so CRLF files need nothing
		// special here.
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if isExtinf(line) {
			if pending != nil {
				// The previous #EXTINF never got a URL.
				summary.dropped++
			}
			e := parseExtinf(line)
			pending = &e
			summary.entries++
			sawExtinf = true
			continue
		}

		if strings.HasPrefix(line, "#") {
			// #EXTM3U, #KODIPROP, #EXT-X-* and any other directive or comment:
			// recognised as a directive, then ignored. Unknown directives are
			// dropped deliberately — do not guess at their meaning.
			if strings.HasPrefix(strings.ToUpper(line), "#EXTM3U") {
				sawHeader = true
			}
			continue
		}

		if pending != nil {
			pending.url = line
			entries = append(entries, *pending)
			pending = nil
			continue
		}
		// A URL with no #EXTINF carries no title and no group, so there is
		// nothing honest to classify it as. Dropped, and counted.
		summary.bareURLs++
	}
	if err := scanner.Err(); err != nil {
		return nil, summary, err
	}
	if pending != nil {
		summary.dropped++
	}

	if !sawHeader && !sawExtinf {
		return nil, summary, errors.New("not an M3U playlist: no #EXTM3U header and no #EXTINF entries")
	}
	return entries, summary, nil
}

// isExtinf reports whether a trimmed line is an `#EXTINF` directive, tolerating
// the spellings real files use: `#EXTINF:...`, `#EXTINF ...` and a bare
// `#EXTINF`. Case is ignored.
func isExtinf(line string) bool {
	const prefix = "#EXTINF"
	if len(line) < len(prefix) || !strings.EqualFold(line[:len(prefix)], prefix) {
		return false
	}
	if len(line) == len(prefix) {
		return true
	}
	switch line[len(prefix)] {
	case ':', ' ', '\t':
		return true
	}
	return false
}

// parseExtinf pulls the attributes and the display title out of an `#EXTINF`
// line. The title is everything after the first comma that is not inside a
// quoted attribute value, so `group-title="Movies, Action",Film` keeps the
// comma inside the quotes and still finds the separator.
func parseExtinf(line string) m3uEntry {
	rest := strings.TrimSpace(line[len("#EXTINF"):])
	rest = strings.TrimPrefix(rest, ":")
	attrsText, title := splitAttrsTitle(rest)
	return m3uEntry{attrs: parseAttributes(attrsText), title: title}
}

// splitAttrsTitle splits on the first comma outside single or double quotes.
// A line with no such comma has no display title.
func splitAttrsTitle(s string) (attrs, title string) {
	inSingle, inDouble := false, false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case ',':
			if !inSingle && !inDouble {
				return s[:i], strings.TrimSpace(s[i+1:])
			}
		}
	}
	return s, ""
}

// attrPattern matches one `key=value` attribute. The value may be
// double-quoted, single-quoted, or bare. The duration token that precedes the
// attributes (`-1`, `0`) has no `=` and so is never matched.
var attrPattern = regexp.MustCompile(`([A-Za-z0-9_.\-]+)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s]+))`)

// parseAttributes collects every attribute on an `#EXTINF` line into a map with
// lowercased keys. Unknown attributes are kept (they cost nothing) but only
// tvg-id, tvg-name, tvg-logo and group-title are read. Attribute order does not
// matter, and a repeated key keeps the last value.
func parseAttributes(text string) map[string]string {
	attrs := map[string]string{}
	for _, m := range attrPattern.FindAllStringSubmatch(text, -1) {
		value := m[2]
		if m[3] != "" {
			value = m[3]
		}
		if m[4] != "" {
			value = m[4]
		}
		attrs[strings.ToLower(strings.TrimSpace(m[1]))] = strings.TrimSpace(value)
	}
	return attrs
}

// --- classification ---------------------------------------------------------

// vodGroupPattern is the rule that decides whether a group-title reads as VOD.
//
// The list is deliberately short and English-only. A group whose normalised
// form contains one of these as a whole word is VOD; everything else is a live
// channel. It is case- and whitespace-insensitive (see normalizeGroup), and
// whole-word matching means "Showtime" is not a "show" and "Movies News" is.
//
// English-only is a stated limitation, not an accident: playlists grouped under
// another language's words (Películas, Série, Film — the German/Italian spelling
// overlaps "film", but "Série" does not) fall through to live channels, and the
// startup summary lists exactly which groups were treated as VOD so that is
// visible. Widening the list is the owner's call.
var vodGroupPattern = regexp.MustCompile(`\b(movie|movies|film|films|vod|series|show|shows)\b`)

// groupIsVOD reports whether a group title reads as VOD.
func groupIsVOD(group string) bool {
	return vodGroupPattern.MatchString(normalizeGroup(group))
}

// normalizeGroup lowercases and collapses all whitespace runs, so "  Movies "
// and "MOVIES" and "TV   Shows" compare as their canonical forms.
func normalizeGroup(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// seasonEpisodePatterns are the season/episode shapes parsed from titles, tried
// in order, case-insensitively: S01E02, 1x02, and Season 1 Episode 2 (also
// "Season 1 Ep 2"). The first match wins.
var seasonEpisodePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bs(\d{1,2})\s*e(\d{1,3})\b`),
	regexp.MustCompile(`(?i)\b(\d{1,2})\s*x\s*(\d{1,3})\b`),
	regexp.MustCompile(`(?i)\bseason\s*(\d{1,3})\s*(?:episode|ep)\s*(\d{1,3})\b`),
}

// parseSeasonEpisode extracts a season and episode number from a title, if the
// title carries one in a recognised shape.
func parseSeasonEpisode(title string) (season, episode int, ok bool) {
	for _, re := range seasonEpisodePatterns {
		m := re.FindStringSubmatch(title)
		if m == nil {
			continue
		}
		s, errS := strconv.Atoi(m[1])
		e, errE := strconv.Atoi(m[2])
		if errS == nil && errE == nil {
			return s, e, true
		}
	}
	return 0, 0, false
}

// seriesTitle is the series name inferred from a titled episode: the title with
// the season/episode marker removed and surrounding separators trimmed, e.g.
// "Breaking Bad S01E02" -> "Breaking Bad". It returns "" when the marker sat at
// the start of the title and left nothing (the common "1x02 - Pilot" shape,
// where the series name is simply not in the title), leaving the caller to fall
// back to the group name. This is the most guess-prone step in the classifier
// and will occasionally group two shows together or split one; the summary log
// is what makes that visible.
func seriesTitle(title string) string {
	for _, re := range seasonEpisodePatterns {
		loc := re.FindStringIndex(title)
		if loc == nil {
			continue
		}
		name := title[:loc[0]] + " " + title[loc[1]:]
		return strings.Trim(strings.TrimSpace(name), "-–—_.:| ")
	}
	return strings.TrimSpace(title)
}

// --- construction -----------------------------------------------------------

// buildM3ULibrary turns parsed entries into the same libraryFile the JSON
// parser produces, applying the classification rule. It assigns explicit ids to
// the parent rows it creates (channels and VOD entries) with the same idSet
// reservation the JSON path uses, so a child row can reference its parent and
// two distinct parents that derive the same id are suffixed rather than merged.
// buildLibrary then reserves those ids and derives the child ids, exactly as it
// does for a JSON file, so M3U ids are stable across reloads.
func buildM3ULibrary(entries []m3uEntry, summary *m3uSummary) libraryFile {
	var file libraryFile
	channelIDs := make(idSet)
	entryIDs := make(idSet)
	type seriesKey struct{ name, group string }
	seriesAt := map[seriesKey]int{}

	for _, e := range entries {
		group := strings.TrimSpace(e.attrs["group-title"])
		category := optional(group)

		if !groupIsVOD(group) {
			ch := source.Channel{
				Name:         e.displayName(),
				Category:     category,
				Logo:         optional(e.attrs["tvg-logo"]),
				EpgChannelID: optional(e.attrs["tvg-id"]),
				Status:       "active",
			}
			ch.ID = channelIDs.claim(derivedChannelID(ch))
			file.Channels = append(file.Channels, ch)
			file.Streams = append(file.Streams, source.Stream{
				ChannelID:  ch.ID,
				Label:      defaultLabel,
				URL:        e.url,
				SourceType: Name,
				IsDefault:  true,
				Status:     "active",
			})
			summary.channels++
			continue
		}

		summary.vodRows++

		season, episode, isEpisode := parseSeasonEpisode(e.title)
		if !isEpisode {
			name := strings.TrimSpace(e.title)
			if name == "" {
				name = e.displayName()
			}
			entry := source.Entry{
				Name:       name,
				Type:       "Movie",
				Category:   category,
				SourceType: Name,
				Status:     "active",
			}
			entry.ID = entryIDs.claim(derivedEntryID(entry))
			file.Entries = append(file.Entries, entry)
			file.VodStreams = append(file.VodStreams, source.VodStream{
				VodID:      entry.ID,
				Label:      defaultLabel,
				URL:        e.url,
				SourceType: Name,
				IsDefault:  true,
				Status:     "active",
			})
			summary.movies++
			continue
		}

		name := seriesTitle(e.title)
		if name == "" {
			name = group
		}
		// Name and group are normalised in the key so "TV Shows" / "tv shows"
		// and "Breaking Bad" / "breaking  bad" are one series, matching the
		// case/whitespace-insensitivity of the VOD rule itself. The entry keeps
		// the original casing.
		key := seriesKey{name: normalizeGroup(name), group: normalizeGroup(group)}
		idx, exists := seriesAt[key]
		if !exists {
			entry := source.Entry{
				Name:       name,
				Type:       "Series",
				Category:   category,
				SourceType: Name,
				Status:     "active",
			}
			entry.ID = entryIDs.claim(derivedEntryID(entry))
			file.Entries = append(file.Entries, entry)
			idx = len(file.Entries) - 1
			seriesAt[key] = idx
			summary.series++
		}
		streamURL := e.url
		file.Episodes = append(file.Episodes, source.Episode{
			VodID:         file.Entries[idx].ID,
			SeasonNumber:  season,
			EpisodeNumber: episode,
			Name:          strings.TrimSpace(e.title),
			StreamURL:     &streamURL,
			SourceType:    Name,
		})
	}

	summary.vodGroups = distinctValues(vodGroupNames(entries))
	return file
}

// vodGroupNames returns the group title of every entry that was treated as VOD,
// for the summary. distinctValues then dedupes and sorts them.
func vodGroupNames(entries []m3uEntry) []string {
	groups := make([]string, 0, len(entries))
	for _, e := range entries {
		if g := strings.TrimSpace(e.attrs["group-title"]); groupIsVOD(g) {
			groups = append(groups, g)
		}
	}
	return groups
}

// optional trims a string and collapses an empty result to nil, matching the
// nil-means-absent convention of the internal models.
func optional(s string) *string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return &s
}

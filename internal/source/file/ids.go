package file

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"

	"github.com/jmvbambico/adoboflix/internal/source"
)

// Opaque ids are whatever the playlist file provides. The interface treats an
// id as an adapter-owned token that is never parsed and never assumed to be a
// UUID, so a hand-authored file may use anything stable: "news-one", a slug,
// a uuid.
//
// Some files omit an id. When they do, this adapter synthesises one so a URL a
// user bookmarked still resolves after a reload. The derived id is a truncated
// SHA-256 over the item's stable identity fields, prefixed to keep the five
// kinds apart. It is deterministic, so reloading the same file yields the same
// ids, and it round-trips through GetChannel/GetEntry/GetEpisode because those
// methods rebuild the same map key.
//
// A child row (a stream, a vod_stream, an episode) references its parent by
// the parent's id *as written in the file*. A derived parent id cannot be
// referenced, because the file never spells it out; a file that wants its
// streams grouped must give the channel an id. Orphan rows are kept but are
// unreachable through that parent, which the adapter documents in
// docs/source-adapters.md.
const idHexLen = 32 // 128 bits

// The derivation inputs intentionally repeat only the fields a human would use
// to say "this is the same item": name + category for a channel or an entry,
// parent + label + url for a stream, parent + season + episode for an episode.

func derivedChannelID(ch source.Channel) string {
	return "ch-" + hashParts("channel", ch.Name, derefOr(ch.Category, ""))
}

func derivedEntryID(e source.Entry) string {
	return "vod-" + hashParts("entry", e.Name, derefOr(e.Category, ""))
}

func derivedStreamID(s source.Stream) string {
	return "str-" + hashParts("stream", s.ChannelID, s.Label, s.URL)
}

func derivedVodStreamID(s source.VodStream) string {
	return "str-" + hashParts("vod-stream", s.VodID, s.Label, s.URL)
}

func derivedEpisodeID(e source.Episode) string {
	return "ep-" + hashParts("episode", e.VodID,
		strconv.Itoa(e.SeasonNumber), strconv.Itoa(e.EpisodeNumber))
}

// hashParts joins parts with a separator that cannot occur in a name and
// hashes the result, so ("a", "b") and ("ab", "") cannot collide.
func hashParts(kind string, parts ...string) string {
	h := sha256.New()
	h.Write([]byte(kind))
	for _, p := range parts {
		h.Write([]byte{0x1f})
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))[:idHexLen]
}

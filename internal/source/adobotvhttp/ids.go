package adobotvhttp

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
)

// Opaque ids are synthesised, because the playlist envelope carries no channel
// or asset id: the real id is sealed inside the encrypted runtime_attr_url
// blob, and AdoboTV identifies a channel only by name + epg_id.
//
// The interface treats every id as an opaque, adapter-owned string that is
// never parsed and never assumed to be a UUID. These ids are a truncated
// SHA-256 over stable envelope fields, prefixed to keep the three kinds apart.
// They are deterministic, so a URL a user bookmarked still resolves after a
// refetch, and they round-trip through GetChannel/GetEntry/GetEpisode because
// those methods recompute the same function and compare.
const idHexLen = 32 // 128 bits

// channelID derives a channel id from every stable identifying field, including
// category: two channels that share a name and epg_id but sit in different
// categories are distinct, and omitting the category would let the second be
// permanently shadowed by the first.
func channelID(name, epgID, category string) string {
	return "ch-" + hashParts("channel", name, epgID, category)
}

func vodAssetID(category, name string) string {
	return "vod-" + hashParts("vod", category, name)
}

func episodeID(assetID string, season, episode int) string {
	return "ep-" + hashParts("episode", assetID, strconv.Itoa(season), strconv.Itoa(episode))
}

// hashParts joins parts with a separator that cannot occur in envelope names
// and hashes the result, so ("a", "b") and ("ab", "") cannot collide.
func hashParts(kind string, parts ...string) string {
	h := sha256.New()
	h.Write([]byte(kind))
	for _, p := range parts {
		h.Write([]byte{0x1f})
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))[:idHexLen]
}

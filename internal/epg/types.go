package epg

import "encoding/xml"

// XMLTV structures for parsing the compiled EPG (XMLTV gzip format from production).
type XMLTV struct {
	XMLName    xml.Name        `xml:"tv"`
	Channels   []xmlChannel    `xml:"channel"`
	Programmes []xmlProgramme  `xml:"programme"`
}

type xmlChannel struct {
	ID           string           `xml:"id,attr"`
	DisplayNames []xmlDisplayName `xml:"display-name"`
	Icons        []xmlIcon        `xml:"icon"`
}

type xmlDisplayName struct {
	Value string `xml:",chardata"`
}

type xmlIcon struct {
	Src string `xml:"src,attr"`
}

type xmlProgramme struct {
	Start   string     `xml:"start,attr"`
	Stop    string     `xml:"stop,attr"`
	Channel string     `xml:"channel,attr"`
	Titles  []xmlTitle `xml:"title"`
	Descs   []xmlDesc  `xml:"desc"`
}

type xmlTitle struct {
	Value string `xml:",chardata"`
}

type xmlDesc struct {
	Value string `xml:",chardata"`
}

// ProgrammeInfo is the JSON response for a single programme entry.
type ProgrammeInfo struct {
	ChannelID   string `json:"channel_id"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Start       string `json:"start"`       // XMLTV raw start timestamp
	Stop        string `json:"stop"`        // XMLTV raw stop timestamp
	StartUnix   int64  `json:"start_unix"`  // Parsed unix timestamp
	StopUnix    int64  `json:"stop_unix"`   // Parsed unix timestamp
}

// ChannelEPGResponse is the JSON response for a channel's current EPG state.
type ChannelEPGResponse struct {
	ChannelName string         `json:"channel_name,omitempty"`
	EpgChannelID string        `json:"epg_channel_id"`
	Current     *ProgrammeInfo  `json:"current,omitempty"`
	Next        *ProgrammeInfo  `json:"next,omitempty"`
	Upcoming    []ProgrammeInfo `json:"upcoming,omitempty"`
}

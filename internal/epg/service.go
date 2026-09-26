package epg

import (
	"bytes"
	"compress/gzip"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"sort"
	"sync"
	"time"

	"github.com/jmvbambico/adoboflix/internal/source"
)

// Service provides EPG data by decoding the compiled XMLTV blob supplied by
// the active source adapter. Decompressing gzip and parsing XMLTV is identical
// for every source, so it stays here; only the byte source differs, and that
// is the optional source.CompiledEPGProvider capability.
type Service struct {
	provider source.CompiledEPGProvider

	mu            sync.RWMutex
	programmes    []xmlProgramme
	channels      map[string]xmlChannel // keyed by XMLTV channel id
	updatedAt     time.Time
	dataHash      string
}

// NewService creates an EPG service and does an initial load. An empty or
// missing EPG is non-fatal: a fresh install with an empty compiled_epg table
// still boots, and the service simply serves no programmes until data arrives.
func NewService(provider source.CompiledEPGProvider) *Service {
	s := &Service{provider: provider}
	if err := s.Refresh(); err != nil {
		log.Printf("[EPG] No EPG data available yet: %v (compiled_epg table may be empty)", err)
	}
	return s
}

// Refresh reloads the compiled EPG from the source into memory.
func (s *Service) Refresh() error {
	if s.provider == nil {
		return fmt.Errorf("no EPG provider configured")
	}

	data, hash, err := s.provider.CompiledEPG()
	if err != nil {
		return fmt.Errorf("failed to read compiled EPG blob: %w", err)
	}

	// Decompress gzip
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gz.Close()

	xmlData, err := io.ReadAll(gz)
	if err != nil {
		return fmt.Errorf("failed to decompress EPG: %w", err)
	}

	var tv XMLTV
	if err := xml.Unmarshal(xmlData, &tv); err != nil {
		return fmt.Errorf("failed to parse XMLTV: %w", err)
	}

	chMap := make(map[string]xmlChannel, len(tv.Channels))
	for _, ch := range tv.Channels {
		chMap[ch.ID] = ch
	}

	s.mu.Lock()
	s.programmes = tv.Programmes
	s.channels = chMap
	s.updatedAt = time.Now()
	s.dataHash = hex.EncodeToString([]byte(hash))
	s.mu.Unlock()

	log.Printf("[EPG] Loaded %d programmes, %d channels from compiled EPG", len(tv.Programmes), len(tv.Channels))
	return nil
}

// GetForChannel resolves the current and next programmes for a given XMLTV channel id.
func (s *Service) GetForChannel(epgChannelID string) *ChannelEPGResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now().UTC()

	resp := &ChannelEPGResponse{
		EpgChannelID: epgChannelID,
	}

	// Try to get display name from channel map
	if ch, ok := s.channels[epgChannelID]; ok && len(ch.DisplayNames) > 0 {
		resp.ChannelName = ch.DisplayNames[0].Value
	}

	// Filter programmes for this channel
	var matching []xmlProgramme
	for _, p := range s.programmes {
		if p.Channel == epgChannelID {
			matching = append(matching, p)
		}
	}

	// Sort by start time
	sort.Slice(matching, func(i, j int) bool {
		return matching[i].Start < matching[j].Start
	})

	var currentIdx = -1
	for i, p := range matching {
		start, stop := parseXMLTVTime(p.Start), parseXMLTVTime(p.Stop)
		if !start.IsZero() && !stop.IsZero() && !now.Before(start) && now.Before(stop) {
			currentIdx = i
			break
		}
	}

	if currentIdx == -1 {
		// Nothing currently airing — find the next upcoming
		for i, p := range matching {
			start := parseXMLTVTime(p.Start)
			if !start.IsZero() && start.After(now) {
				resp.Next = programmeToInfo(epgChannelID, &matching[i])
				break
			}
		}
		return resp
	}

	resp.Current = programmeToInfo(epgChannelID, &matching[currentIdx])

	// Collect upcoming (up to 5)
	for i := currentIdx + 1; i < len(matching) && i < currentIdx+6; i++ {
		resp.Upcoming = append(resp.Upcoming, *programmeToInfo(epgChannelID, &matching[i]))
	}

	if len(resp.Upcoming) > 0 {
		next := resp.Upcoming[0]
		resp.Next = &next
	}

	return resp
}

// programmeToInfo converts an internal XML programme to a public ProgrammeInfo.
func programmeToInfo(channelID string, p *xmlProgramme) *ProgrammeInfo {
	title := ""
	if len(p.Titles) > 0 {
		title = p.Titles[0].Value
	}
	desc := ""
	if len(p.Descs) > 0 {
		desc = p.Descs[0].Value
	}

	startUnix := parseXMLTVTime(p.Start).Unix()
	stopUnix := parseXMLTVTime(p.Stop).Unix()

	return &ProgrammeInfo{
		ChannelID:   channelID,
		Title:       title,
		Description: desc,
		Start:       p.Start,
		Stop:        p.Stop,
		StartUnix:   startUnix,
		StopUnix:    stopUnix,
	}
}

// parseXMLTVTime parses an XMLTV timestamp like "20260624080000 +0800" into time.Time.
func parseXMLTVTime(s string) time.Time {
	if len(s) < 14 {
		return time.Time{}
	}

	// Parse: "YYYYMMDDHHmmSS ±HHMM"
_LAYOUT := "20060102150405"
	_tzLayout := "20060102150405 -0700"

	t, err := time.Parse(_tzLayout, s[:14]+" "+s[15:])
	if err != nil {
		// Try without timezone
		t, err = time.Parse(_LAYOUT, s[:14])
		if err != nil {
			return time.Time{}
		}
	}
	return t
}

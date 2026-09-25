package main

import (
	"errors"
	"strings"
	"testing"
)

func TestDashSegmentRefCoordinates(t *testing.T) {
	cases := []struct {
		name     string
		manifest string
		wantRef  string
		wantBase string
		wantErr  bool
	}{
		{
			name: "startNumber absent defaults to 1",
			manifest: `<MPD><Period><AdaptationSet>
				<SegmentTemplate media="seg/$Number$.m4s"/>
				<Representation id="r0"/>
			</AdaptationSet></Period></MPD>`,
			wantRef: "seg/1.m4s",
		},
		{
			name: "startNumber 0 is honoured",
			manifest: `<MPD><Period><AdaptationSet>
				<SegmentTemplate media="seg/$Number$.m4s" startNumber="0"/>
				<Representation id="r0"/>
			</AdaptationSet></Period></MPD>`,
			wantRef: "seg/0.m4s",
		},
		{
			name: "startNumber 100 is honoured",
			manifest: `<MPD><Period><AdaptationSet>
				<SegmentTemplate media="seg/$Number$.m4s" startNumber="100"/>
				<Representation id="r0"/>
			</AdaptationSet></Period></MPD>`,
			wantRef: "seg/100.m4s",
		},
		{
			name: "printf modifier zero-pads Number",
			manifest: `<MPD><Period><AdaptationSet>
				<SegmentTemplate media="seg/$Number%05d$.m4s" startNumber="7"/>
				<Representation id="r0"/>
			</AdaptationSet></Period></MPD>`,
			wantRef: "seg/00007.m4s",
		},
		{
			name: "Time comes from the first S with nonzero t",
			manifest: `<MPD><Period><AdaptationSet>
				<SegmentTemplate media="seg/$Time$.m4s">
					<SegmentTimeline><S t="1000" d="100" r="2"/><S t="1300" d="100"/></SegmentTimeline>
				</SegmentTemplate>
				<Representation id="r0"/>
			</AdaptationSet></Period></MPD>`,
			wantRef: "seg/1000.m4s",
		},
		{
			name: "Time defaults to 0 when the first S has no t",
			manifest: `<MPD><Period><AdaptationSet>
				<SegmentTemplate media="seg/$Time$.m4s">
					<SegmentTimeline><S d="1000" r="3"/></SegmentTimeline>
				</SegmentTemplate>
				<Representation id="r0"/>
			</AdaptationSet></Period></MPD>`,
			wantRef: "seg/0.m4s",
		},
		{
			name: "printf modifier zero-pads Time",
			manifest: `<MPD><Period><AdaptationSet>
				<SegmentTemplate media="seg/$Time%08d$.m4s">
					<SegmentTimeline><S t="1200" d="100"/></SegmentTimeline>
				</SegmentTemplate>
				<Representation id="r0"/>
			</AdaptationSet></Period></MPD>`,
			wantRef: "seg/00001200.m4s",
		},
		{
			name: "coordinates do not leak from a later SegmentTemplate",
			manifest: `<MPD><Period>
				<AdaptationSet>
					<SegmentTemplate media="a/$Number$.m4s"/>
					<Representation id="r0"/>
				</AdaptationSet>
				<AdaptationSet>
					<SegmentTemplate media="b/$Number$.m4s" startNumber="100"/>
					<Representation id="r1"/>
				</AdaptationSet>
			</Period></MPD>`,
			wantRef: "a/1.m4s",
		},
		{
			name: "a media-less template does not lend its startNumber",
			manifest: `<MPD><Period>
				<AdaptationSet>
					<SegmentTemplate initialization="i/$Number$.mp4" startNumber="7"/>
					<Representation id="r0"/>
				</AdaptationSet>
				<AdaptationSet>
					<SegmentTemplate media="m/$Number$.m4s" startNumber="100"/>
					<Representation id="r1"/>
				</AdaptationSet>
			</Period></MPD>`,
			wantRef: "m/100.m4s",
		},
		{
			name: "SegmentList startNumber and SegmentURL media",
			manifest: `<MPD><Period><AdaptationSet><Representation id="r0">
				<SegmentList startNumber="50"><SegmentURL media="seg/$Number$.m4s"/></SegmentList>
			</Representation></AdaptationSet></Period></MPD>`,
			wantRef: "seg/50.m4s",
		},
		{
			name: "unresolvable placeholder falls through to the next candidate",
			manifest: `<MPD><Period>
				<AdaptationSet>
					<SegmentTemplate media="a/$Bandwidth$.m4s"/>
					<Representation id="r0"/>
				</AdaptationSet>
				<AdaptationSet>
					<SegmentTemplate media="b/$Number$.m4s" startNumber="3"/>
					<Representation id="r1"/>
				</AdaptationSet>
			</Period></MPD>`,
			wantRef: "b/3.m4s",
		},
		{
			name: "RepresentationID is filled from the representation",
			manifest: `<MPD><Period><AdaptationSet>
				<SegmentTemplate media="$RepresentationID$/$Number$.m4s" startNumber="4"/>
				<Representation id="video"/>
			</AdaptationSet></Period></MPD>`,
			wantRef: "video/4.m4s",
		},
		{
			name: "directory BaseURL is returned as the resolution base",
			manifest: `<MPD><BaseURL>/cdn/</BaseURL><Period><AdaptationSet>
				<SegmentTemplate media="seg/$Number$.m4s"/>
				<Representation id="r0"/>
			</AdaptationSet></Period></MPD>`,
			wantRef:  "seg/1.m4s",
			wantBase: "/cdn/",
		},
		{
			name: "us-ascii encoding declaration is decoded",
			manifest: `<?xml version="1.0" encoding="us-ascii"?>
			<MPD><Period><AdaptationSet>
				<SegmentTemplate media="seg/$Number$.m4s" startNumber="9"/>
				<Representation id="r0"/>
			</AdaptationSet></Period></MPD>`,
			wantRef: "seg/9.m4s",
		},
		{
			name: "all placeholders unresolvable yields a coordinates error",
			manifest: `<MPD><Period><AdaptationSet>
				<SegmentTemplate media="a/$Bandwidth$.m4s"/>
				<Representation id="r0"/>
			</AdaptationSet></Period></MPD>`,
			wantErr: true,
		},
		{
			name:     "no segment reference at all yields a coordinates error",
			manifest: `<MPD><Period/></MPD>`,
			wantErr:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref, base, err := dashSegmentRef([]byte(tc.manifest))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got ref %q", ref)
				}
				if !errors.Is(err, errNoSegmentCoords) {
					t.Fatalf("expected errNoSegmentCoords, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if ref != tc.wantRef {
				t.Errorf("ref = %q, want %q", ref, tc.wantRef)
			}
			if base != tc.wantBase {
				t.Errorf("base = %q, want %q", base, tc.wantBase)
			}
		})
	}
}

func TestNoSegmentCoordsErrorIsDistinguishable(t *testing.T) {
	_, _, err := dashSegmentRef([]byte(`<MPD><Period/></MPD>`))
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "could not derive segment coordinates") {
		t.Fatalf("harness limitation must be reported distinguishably, got %q", err.Error())
	}
}

func TestSubstituteTemplateRejectsBadVerb(t *testing.T) {
	// A string-valued placeholder with an integer verb cannot render; the
	// candidate must be rejected rather than producing a mangled URL.
	if _, ok := substituteTemplate("$RepresentationID%05d$/$Number$.m4s", segmentCoords{startNumber: 1, repID: "v"}); ok {
		t.Error("expected an integer verb against a string value to be rejected")
	}
}

func TestSubstituteTemplateFormatModifiers(t *testing.T) {
	got, ok := substituteTemplate("seg/$Number%05d$-$Time%03d$.m4s", segmentCoords{startNumber: 7, time: 5})
	if !ok {
		t.Fatal("expected substitution to succeed")
	}
	if want := "seg/00007-005.m4s"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

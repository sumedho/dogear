package dogear

import "testing"

func TestQualityClass(t *testing.T) {
	tests := []struct {
		name    string
		heading string
		text    string
		want    string
	}{
		{name: "toc", heading: "TABLE OF CONTENTS", text: "MIDI CONFIG 58", want: QualityTOC},
		{name: "index", heading: "INDEX", text: "MIDI sync 58", want: QualityIndex},
		{name: "reference", heading: "MIDI", text: "MIDI sync 58\nMIDI config 58", want: QualityReferenceOnly},
		{name: "content", heading: "12.3.1 SYNC", text: "Controls how the instrument receives and sends MIDI clock and transport commands.", want: QualityContent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := qualityClass(tt.heading, tt.text); got != tt.want {
				t.Fatalf("qualityClass() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRerankPrefersSyncContentOverReferenceList(t *testing.T) {
	chunks := []RetrievedChunk{
		{
			ChunkID:     1,
			HeadingPath: "MIDI",
			Text:        "MIDI config 58\nMIDI sync 58",
			Score:       -10,
		},
		{
			ChunkID:     2,
			HeadingPath: "12.3.1 SYNC",
			Text:        "Controls how the instrument receives and sends MIDI clock and transport commands.",
			Score:       -6,
		},
	}
	got := rerankChunks("How do I configure MIDI sync?", chunks, 2)
	if got[0].ChunkID != 2 {
		t.Fatalf("top chunk = %d, want 2; debug=%#v", got[0].ChunkID, got[0].Debug)
	}
}

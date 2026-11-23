// Package transcript defines the transport-neutral result shared by remote and local ASR.
package transcript

// Word is an individual timed token returned by a transcription provider.
type Word struct {
	Text  string  `json:"word"`
	Start float64 `json:"start"`
	End   float64 `json:"end"`
}

// Segment is a line of transcript with its audio range and optional tokens.
type Segment struct {
	Text    string  `json:"text"`
	Speaker string  `json:"speaker,omitempty"`
	Start   float64 `json:"start"`
	End     float64 `json:"end"`
	Words   []Word  `json:"words,omitempty"`
}

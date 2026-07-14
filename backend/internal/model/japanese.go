package model

type FuriganaSegment struct {
	Text    string `json:"text"`
	Reading string `json:"reading,omitempty"`
}

type FuriganaResponse struct {
	Segments []FuriganaSegment `json:"segments"`
	Source   string            `json:"source"`
}

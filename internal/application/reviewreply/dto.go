package reviewreply

// verdictWire mirrors the critic's JSON output shape. Kept package-local so
// domain types stay free of wire concerns.
type verdictWire struct {
	Pass       bool     `json:"pass"`
	Issues     []string `json:"issues"`
	Confidence float64  `json:"confidence"`
	Reasoning  string   `json:"reasoning"`
}

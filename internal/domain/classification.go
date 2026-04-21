package domain

import "time"

// PrimaryCode is the Traffic Light classification for a guest turn.
type PrimaryCode string

const (
	G1 PrimaryCode = "G1"
	G2 PrimaryCode = "G2"
	Y1 PrimaryCode = "Y1"
	Y2 PrimaryCode = "Y2"
	Y3 PrimaryCode = "Y3"
	Y4 PrimaryCode = "Y4"
	Y5 PrimaryCode = "Y5"
	Y6 PrimaryCode = "Y6"
	Y7 PrimaryCode = "Y7"
	R1 PrimaryCode = "R1"
	R2 PrimaryCode = "R2"
	X1 PrimaryCode = "X1"
)

// NextAction is the LLM's advisory routing signal. The Go gate is authoritative.
type NextAction string

const (
	ActionGenerate NextAction = "generate_reply"
	ActionEscalate NextAction = "escalate_human"
	ActionQualify  NextAction = "qualify_question"
)

// Observation is one entry in the open "additional" entity bag.
type Observation struct {
	Key        string  // snake_case, <=40 chars
	Value      string  // <=200 chars
	ValueType  string  // "string" | "number" | "bool" | "list"
	Confidence float64 // 0..1
	Source     string  // quoted guest text, <=120 chars
}

// ExtractedEntities are the facts the classifier pulled from the guest turn.
type ExtractedEntities struct {
	CheckIn     *time.Time
	CheckOut    *time.Time
	GuestCount  *int
	Pets        *bool
	Vehicles    *int
	ListingHint *string
	Additional  []Observation
}

// Classification is the full typed output of Stage A.
type Classification struct {
	PrimaryCode       PrimaryCode
	SecondaryCode     *PrimaryCode
	Confidence        float64
	ExtractedEntities ExtractedEntities
	RiskFlag          bool
	RiskReason        string
	NextAction        NextAction
	Reasoning         string
}

// LowRiskCodes is the set of primary codes eligible for auto-send (see spec §6).
var LowRiskCodes = map[PrimaryCode]struct{}{
	G1: {}, G2: {}, Y1: {}, Y3: {}, Y4: {}, Y6: {}, Y7: {},
}

// AlwaysEscalateCodes require a human regardless of confidence.
var AlwaysEscalateCodes = map[PrimaryCode]struct{}{
	Y2: {}, Y5: {}, R1: {}, R2: {},
}

// priorityRank ranks Traffic Light codes for multi-signal resolution per
// CHALLENGE.md §6: RED > Y5 > Y2 > Y4 > Y1 > Y3 > Y6 > Y7 > GREEN > GRAY.
// Lower rank = higher priority. Consumed by Classification.EnforcePriority.
var priorityRank = map[PrimaryCode]int{
	R1: 0, R2: 1,
	Y5: 2, Y2: 3, Y4: 4, Y1: 5, Y3: 6, Y6: 7, Y7: 8,
	G1: 9, G2: 10,
	X1: 11,
}

// EnforcePriority returns a copy of c with primary and secondary ordered per
// the §6 priority rule. When SecondaryCode outranks PrimaryCode the two are
// swapped and swapped=true is returned so the caller can log the correction.
// Unknown codes rank below X1 and are never promoted above a known code.
func (c Classification) EnforcePriority() (Classification, bool) {
	if c.SecondaryCode == nil {
		return c, false
	}
	pr, pok := priorityRank[c.PrimaryCode]
	if !pok {
		pr = len(priorityRank)
	}
	sr, sok := priorityRank[*c.SecondaryCode]
	if !sok {
		sr = len(priorityRank)
	}
	if sr >= pr {
		return c, false
	}
	prev := c.PrimaryCode
	c.PrimaryCode = *c.SecondaryCode
	c.SecondaryCode = &prev
	return c, true
}

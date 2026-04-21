package classify

import (
	"encoding/json"
	"time"

	"github.com/chaustre/inquiryiq/internal/domain"
)

// classificationWire mirrors the LLM's JSON output shape (snake_case). Kept
// package-local so domain.Classification stays free of wire-format concerns.
type classificationWire struct {
	PrimaryCode   string             `json:"primary_code"`
	SecondaryCode *string            `json:"secondary_code,omitempty"`
	Confidence    float64            `json:"confidence"`
	RiskFlag      bool               `json:"risk_flag"`
	RiskReason    string             `json:"risk_reason,omitempty"`
	NextAction    string             `json:"next_action"`
	Reasoning     string             `json:"reasoning"`
	Entities      extractedEntityDTO `json:"extracted_entities"`
}

type extractedEntityDTO struct {
	CheckIn     *string          `json:"check_in,omitempty"`
	CheckOut    *string          `json:"check_out,omitempty"`
	GuestCount  *int             `json:"guest_count,omitempty"`
	Pets        *bool            `json:"pets,omitempty"`
	Vehicles    *int             `json:"vehicles,omitempty"`
	ListingHint *string          `json:"listing_hint,omitempty"`
	Additional  []observationDTO `json:"additional,omitempty"`
}

type observationDTO struct {
	Key        string  `json:"key"`
	Value      string  `json:"value"`
	ValueType  string  `json:"value_type"`
	Confidence float64 `json:"confidence"`
	Source     string  `json:"source"`
}

func (w *classificationWire) toDomain() (domain.Classification, error) {
	entities, err := w.Entities.toDomain()
	if err != nil {
		return domain.Classification{}, err
	}
	c := domain.Classification{
		PrimaryCode:       domain.PrimaryCode(w.PrimaryCode),
		Confidence:        w.Confidence,
		ExtractedEntities: entities,
		RiskFlag:          w.RiskFlag,
		RiskReason:        w.RiskReason,
		NextAction:        domain.NextAction(w.NextAction),
		Reasoning:         w.Reasoning,
	}
	if w.SecondaryCode != nil && *w.SecondaryCode != "" {
		sc := domain.PrimaryCode(*w.SecondaryCode)
		c.SecondaryCode = &sc
	}
	return c, nil
}

func (e *extractedEntityDTO) toDomain() (domain.ExtractedEntities, error) {
	ci, err := parseISODate(e.CheckIn)
	if err != nil {
		return domain.ExtractedEntities{}, err
	}
	co, err := parseISODate(e.CheckOut)
	if err != nil {
		return domain.ExtractedEntities{}, err
	}
	add := make([]domain.Observation, 0, len(e.Additional))
	for i := range e.Additional {
		o := e.Additional[i]
		add = append(add, domain.Observation{
			Key:        o.Key,
			Value:      o.Value,
			ValueType:  o.ValueType,
			Confidence: o.Confidence,
			Source:     o.Source,
		})
	}
	return domain.ExtractedEntities{
		CheckIn:     ci,
		CheckOut:    co,
		GuestCount:  e.GuestCount,
		Pets:        e.Pets,
		Vehicles:    e.Vehicles,
		ListingHint: e.ListingHint,
		Additional:  add,
	}, nil
}

func parseISODate(s *string) (*time.Time, error) {
	if s == nil || *s == "" {
		return nil, nil
	}
	t, err := time.Parse("2006-01-02", *s)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// unmarshalClassification deserializes a raw classifier payload into the
// wire DTO, primarily for tests that want to inspect the wire shape.
func unmarshalClassification(raw []byte) (classificationWire, error) {
	var w classificationWire
	if err := json.Unmarshal(raw, &w); err != nil {
		return classificationWire{}, err
	}
	return w, nil
}

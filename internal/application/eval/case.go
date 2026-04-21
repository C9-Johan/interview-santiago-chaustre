// Package eval is the regression harness for the Stage A classifier. It loads
// a labeled golden set (eval/golden_set.json) and exercises a *classify.UseCase
// — real or fake — against each case, reporting per-case pass/fail plus
// aggregate accuracy and confidence calibration metrics. The harness is the
// mechanism we use to catch prompt regressions BEFORE they ship: a failing
// golden case means the next shipped change broke a contract we care about.
package eval

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/chaustre/inquiryiq/internal/domain"
)

// Case is one labeled guest turn in the golden set. Expectations are permissive
// on purpose: AllowPrimary is the set of codes we accept so a correct-but-edge
// LLM call does not flap the regression gate. ExpectedPrimary is the single
// canonical answer used when we report accuracy.
type Case struct {
	ID                       string               `json:"id"`
	Body                     string               `json:"body"`
	Language                 string               `json:"language"`
	ExpectedPrimary          domain.PrimaryCode   `json:"expected_primary"`
	AllowPrimary             []domain.PrimaryCode `json:"allow_primary"`
	ExpectedSecondary        *domain.PrimaryCode  `json:"expected_secondary,omitempty"`
	MinConfidence            float64              `json:"min_confidence"`
	ExpectedRiskFlag         bool                 `json:"expected_risk_flag"`
	ExpectedAutoSendEligible bool                 `json:"expected_auto_send_eligible"`
}

// GoldenSet is the on-disk shape of eval/golden_set.json.
type GoldenSet struct {
	Version     int    `json:"version"`
	Description string `json:"description"`
	Cases       []Case `json:"cases"`
}

// LoadGoldenSet reads and parses the labeled cases from path. Fails loud on
// empty or malformed files — a silent zero-case run would pass vacuously and
// defeat the regression gate.
func LoadGoldenSet(path string) (GoldenSet, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return GoldenSet{}, fmt.Errorf("read golden set %s: %w", path, err)
	}
	var g GoldenSet
	if err := json.Unmarshal(b, &g); err != nil {
		return GoldenSet{}, fmt.Errorf("parse golden set %s: %w", path, err)
	}
	if len(g.Cases) == 0 {
		return GoldenSet{}, fmt.Errorf("golden set %s has zero cases", path)
	}
	return g, nil
}

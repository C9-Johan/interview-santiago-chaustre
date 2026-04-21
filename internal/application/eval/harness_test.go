package eval

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chaustre/inquiryiq/internal/application/classify"
	"github.com/chaustre/inquiryiq/internal/domain"
)

// fakeClassifier returns a pre-scripted Classification keyed by turn body so
// we can assert harness accounting logic without an LLM. A missing key yields
// an X1/low-confidence fallback to mimic the real conservative-bias default.
type fakeClassifier struct {
	replies map[string]domain.Classification
}

func (f fakeClassifier) Classify(_ context.Context, in classify.Input) (domain.Classification, error) {
	if len(in.Turn.Messages) == 0 {
		return domain.Classification{}, nil
	}
	if c, ok := f.replies[in.Turn.Messages[0].Body]; ok {
		return c, nil
	}
	return domain.Classification{PrimaryCode: domain.X1, Confidence: 0.2}, nil
}

func newFake(m map[string]domain.Classification) fakeClassifier {
	return fakeClassifier{replies: m}
}

func TestRunAggregatesPerCaseResults(t *testing.T) {
	set := GoldenSet{Cases: []Case{
		{
			ID: "happy_g1", Body: "book it",
			ExpectedPrimary: domain.G1, AllowPrimary: []domain.PrimaryCode{domain.G1},
			MinConfidence: 0.7, ExpectedRiskFlag: false, ExpectedAutoSendEligible: true,
		},
		{
			ID: "wrong_code", Body: "send me venmo",
			ExpectedPrimary: domain.Y2, AllowPrimary: []domain.PrimaryCode{domain.Y2},
			MinConfidence: 0.5, ExpectedRiskFlag: true, ExpectedAutoSendEligible: false,
		},
	}}
	fake := newFake(map[string]domain.Classification{
		"book it":       {PrimaryCode: domain.G1, Confidence: 0.9},
		"send me venmo": {PrimaryCode: domain.Y7, Confidence: 0.8, RiskFlag: false},
	})
	r := Run(context.Background(), fake, set, time.Now())
	if r.Total != 2 || r.Passed != 1 || r.Failed != 1 {
		t.Fatalf("unexpected counts: total=%d passed=%d failed=%d", r.Total, r.Passed, r.Failed)
	}
	if r.PrimaryAccuracy != 0.5 {
		t.Errorf("primary_accuracy: got %.2f, want 0.5", r.PrimaryAccuracy)
	}
	if r.RiskFlagAgreement != 0.5 {
		t.Errorf("risk_flag_agreement: got %.2f, want 0.5", r.RiskFlagAgreement)
	}
}

func TestRunAcceptsAnyAllowedPrimary(t *testing.T) {
	set := GoldenSet{Cases: []Case{{
		ID: "flex", Body: "late checkin availability",
		ExpectedPrimary:          domain.Y4,
		AllowPrimary:             []domain.PrimaryCode{domain.Y4, domain.Y6},
		MinConfidence:            0.6,
		ExpectedAutoSendEligible: true,
	}}}
	fake := newFake(map[string]domain.Classification{
		"late checkin availability": {PrimaryCode: domain.Y6, Confidence: 0.72},
	})
	r := Run(context.Background(), fake, set, time.Now())
	if r.Passed != 1 {
		t.Fatalf("Y6 is in AllowPrimary, case should pass; report=%+v", r)
	}
}

func TestRunPropagatesClassifierError(t *testing.T) {
	set := GoldenSet{Cases: []Case{{
		ID: "err", Body: "unknown",
		ExpectedPrimary: domain.G1, AllowPrimary: []domain.PrimaryCode{domain.G1},
		MinConfidence: 0.7,
	}}}
	fake := newFake(nil) // every body falls through to X1 / low confidence
	r := Run(context.Background(), fake, set, time.Now())
	if r.Passed != 0 || r.Failed != 1 {
		t.Fatalf("unexpected counts: %+v", r)
	}
}

func TestPrintReportTerseOnGreen(t *testing.T) {
	set := GoldenSet{Cases: []Case{{
		ID: "ok", Body: "book it",
		ExpectedPrimary: domain.G1, AllowPrimary: []domain.PrimaryCode{domain.G1},
		MinConfidence: 0.7, ExpectedAutoSendEligible: true,
	}}}
	fake := newFake(map[string]domain.Classification{
		"book it": {PrimaryCode: domain.G1, Confidence: 0.9},
	})
	r := Run(context.Background(), fake, set, time.Now())
	var buf bytes.Buffer
	PrintReport(&buf, r)
	out := buf.String()
	if !strings.Contains(out, "passed               1") {
		t.Errorf("want passed line in report:\n%s", out)
	}
	if strings.Contains(out, "# failures") {
		t.Errorf("green run must not emit failures section:\n%s", out)
	}
}

func TestPrintReportListsFailures(t *testing.T) {
	set := GoldenSet{Cases: []Case{{
		ID: "broken", Body: "foo",
		ExpectedPrimary: domain.G1, AllowPrimary: []domain.PrimaryCode{domain.G1},
		MinConfidence: 0.9, ExpectedAutoSendEligible: true,
	}}}
	fake := newFake(map[string]domain.Classification{
		"foo": {PrimaryCode: domain.Y7, Confidence: 0.3},
	})
	r := Run(context.Background(), fake, set, time.Now())
	var buf bytes.Buffer
	PrintReport(&buf, r)
	out := buf.String()
	if !strings.Contains(out, "# failures") {
		t.Errorf("failures section missing:\n%s", out)
	}
	if !strings.Contains(out, "primary_code") {
		t.Errorf("primary_code check should be listed:\n%s", out)
	}
}

func TestLoadGoldenSetRejectsEmpty(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(p, []byte(`{"version":1,"cases":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadGoldenSet(p); err == nil {
		t.Error("want error on zero-case golden set")
	}
}

func TestLoadGoldenSetParsesRepoFile(t *testing.T) {
	p := filepath.Join("..", "..", "..", "eval", "golden_set.json")
	g, err := LoadGoldenSet(p)
	if err != nil {
		t.Fatalf("load repo golden set: %v", err)
	}
	if len(g.Cases) < 15 {
		t.Errorf("repo golden set should carry >=15 cases, got %d", len(g.Cases))
	}
}

func TestLoadGoldenSetDirParsesRepoSets(t *testing.T) {
	p := filepath.Join("..", "..", "..", "eval", "sets")
	sets, err := LoadGoldenSetDir(p)
	if err != nil {
		t.Fatalf("load dir: %v", err)
	}
	if len(sets) < 2 {
		t.Errorf("expected multiple per-language sets, got %d", len(sets))
	}
	seen := map[string]bool{}
	for _, s := range sets {
		seen[s.Description] = true
	}
	for _, want := range []string{"en", "es", "fr"} {
		if !seen[want] {
			t.Errorf("locale %q missing from sets/", want)
		}
	}
}

func TestRunManyReportsPerLocaleAccuracy(t *testing.T) {
	sets := []GoldenSet{
		{Description: "en", Cases: []Case{{
			ID: "en_ok", Body: "book it", Language: "en",
			ExpectedPrimary: domain.G1, AllowPrimary: []domain.PrimaryCode{domain.G1},
			MinConfidence: 0.5, ExpectedAutoSendEligible: true,
		}}},
		{Description: "es", Cases: []Case{{
			ID: "es_fail", Body: "reservar", Language: "es",
			ExpectedPrimary: domain.G1, AllowPrimary: []domain.PrimaryCode{domain.G1},
			MinConfidence: 0.5, ExpectedAutoSendEligible: true,
		}}},
	}
	fake := newFake(map[string]domain.Classification{
		"book it":  {PrimaryCode: domain.G1, Confidence: 0.9},
		"reservar": {PrimaryCode: domain.Y6, Confidence: 0.9},
	})
	reports := RunMany(context.Background(), fake, sets, time.Now())
	if reports["en"].PrimaryAccuracy != 1.0 {
		t.Errorf("en report: %+v", reports["en"])
	}
	if reports["es"].PrimaryAccuracy != 0.0 {
		t.Errorf("es report: %+v", reports["es"])
	}
}

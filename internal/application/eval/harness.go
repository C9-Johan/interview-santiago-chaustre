package eval

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/chaustre/inquiryiq/internal/application/classify"
	"github.com/chaustre/inquiryiq/internal/application/decide"
	"github.com/chaustre/inquiryiq/internal/domain"
)

// classifier is the narrow contract the harness needs. Satisfied structurally
// by *classify.UseCase; tests pass a fake so the harness runs without an LLM.
type classifier interface {
	Classify(ctx context.Context, in classify.Input) (domain.Classification, error)
}

// Result is the per-case outcome produced by Run. Pass is the overall verdict;
// Checks lists every expectation we evaluated (in order) so operators can see
// partial failures, not just binary pass/fail.
type Result struct {
	CaseID    string
	Got       domain.Classification
	Err       error
	Pass      bool
	Checks    []Check
	LatencyMS int64
}

// Check is one expectation's outcome. Name identifies the assertion (e.g.
// "primary_code"); Want/Got are the short printable forms for the report.
type Check struct {
	Name string
	Pass bool
	Want string
	Got  string
}

// Report aggregates per-case results into the metrics the CLI prints and the
// eval target gates on. We intentionally keep metrics surface-level
// (accuracy on primary, risk-flag agreement, under-confidence count) — a
// heavier regression layer belongs in a separate evaluation pipeline.
type Report struct {
	Total             int
	Passed            int
	Failed            int
	PrimaryAccuracy   float64
	RiskFlagAgreement float64
	MeanConfidence    float64
	UnderThreshold065 int
	Results           []Result
}

// Run executes every case in g against c, applying the same GATE 1 rules the
// production orchestrator uses. The returned Report is safe to print and to
// compare between runs.
func Run(ctx context.Context, c classifier, g GoldenSet, now time.Time) Report {
	results := make([]Result, 0, len(g.Cases))
	for i := range g.Cases {
		results = append(results, runCase(ctx, c, g.Cases[i], now))
	}
	return aggregate(results)
}

func runCase(ctx context.Context, c classifier, cc Case, now time.Time) Result {
	in := classify.Input{
		Turn: domain.Turn{
			Key:        domain.ConversationKey("eval:" + cc.ID),
			LastPostID: "eval:" + cc.ID,
			Messages: []domain.Message{{
				PostID:    "eval:" + cc.ID,
				Body:      cc.Body,
				CreatedAt: now,
				Role:      domain.RoleGuest,
			}},
		},
		Prior: domain.PriorContext{},
		Now:   now,
	}
	start := time.Now()
	got, err := c.Classify(ctx, in)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return Result{
			CaseID: cc.ID, Got: got, Err: err, Pass: false,
			Checks:    []Check{{Name: "classifier_error", Pass: false, Want: "nil", Got: err.Error()}},
			LatencyMS: latency,
		}
	}
	checks := evaluate(cc, got)
	return Result{
		CaseID: cc.ID, Got: got, Pass: allPassed(checks), Checks: checks, LatencyMS: latency,
	}
}

func evaluate(cc Case, got domain.Classification) []Check {
	checks := make([]Check, 0, 4)
	checks = append(checks, checkPrimary(cc, got))
	checks = append(checks, checkMinConfidence(cc, got))
	checks = append(checks, checkRiskFlag(cc, got))
	checks = append(checks, checkAutoSendEligible(cc, got))
	return checks
}

func checkPrimary(cc Case, got domain.Classification) Check {
	allowed := cc.AllowPrimary
	if len(allowed) == 0 {
		allowed = []domain.PrimaryCode{cc.ExpectedPrimary}
	}
	pass := slices.Contains(allowed, got.PrimaryCode)
	return Check{Name: "primary_code", Pass: pass, Want: formatAllowed(allowed), Got: string(got.PrimaryCode)}
}

func checkMinConfidence(cc Case, got domain.Classification) Check {
	return Check{
		Name: "min_confidence",
		Pass: got.Confidence >= cc.MinConfidence,
		Want: fmt.Sprintf(">= %.2f", cc.MinConfidence),
		Got:  fmt.Sprintf("%.2f", got.Confidence),
	}
}

func checkRiskFlag(cc Case, got domain.Classification) Check {
	return Check{
		Name: "risk_flag",
		Pass: got.RiskFlag == cc.ExpectedRiskFlag,
		Want: fmt.Sprintf("%t", cc.ExpectedRiskFlag),
		Got:  fmt.Sprintf("%t", got.RiskFlag),
	}
}

func checkAutoSendEligible(cc Case, got domain.Classification) Check {
	gate := decide.PreGenerate(got, domain.Toggles{AutoResponseEnabled: true}, 0.65)
	return Check{
		Name: "auto_send_eligible",
		Pass: gate.AutoSend == cc.ExpectedAutoSendEligible,
		Want: fmt.Sprintf("%t", cc.ExpectedAutoSendEligible),
		Got:  fmt.Sprintf("%t (%s)", gate.AutoSend, gate.Reason),
	}
}

func formatAllowed(codes []domain.PrimaryCode) string {
	if len(codes) == 1 {
		return string(codes[0])
	}
	var b strings.Builder
	b.WriteByte('{')
	for i := range codes {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(string(codes[i]))
	}
	b.WriteByte('}')
	return b.String()
}

func allPassed(cs []Check) bool {
	for i := range cs {
		if !cs[i].Pass {
			return false
		}
	}
	return true
}

func aggregate(results []Result) Report {
	r := Report{Total: len(results), Results: results}
	if len(results) == 0 {
		return r
	}
	var sumConf float64
	var primaryHits, riskAgree int
	for i := range results {
		if results[i].Pass {
			r.Passed++
		}
		if !results[i].Pass {
			r.Failed++
		}
		sumConf += results[i].Got.Confidence
		if results[i].Got.Confidence < 0.65 {
			r.UnderThreshold065++
		}
		for j := range results[i].Checks {
			ck := results[i].Checks[j]
			if ck.Name == "primary_code" && ck.Pass {
				primaryHits++
			}
			if ck.Name == "risk_flag" && ck.Pass {
				riskAgree++
			}
		}
	}
	n := float64(len(results))
	r.PrimaryAccuracy = float64(primaryHits) / n
	r.RiskFlagAgreement = float64(riskAgree) / n
	r.MeanConfidence = sumConf / n
	return r
}

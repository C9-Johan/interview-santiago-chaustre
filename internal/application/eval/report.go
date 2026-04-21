package eval

import (
	"fmt"
	"io"
	"sort"
)

// PrintReport writes a human-readable summary of r to w. Per-case lines are
// emitted only for failures so green runs stay terse; aggregate metrics
// always appear. Returns the bytes written, ignoring the value on purpose
// (non-fatal if w is a tty that drops bytes).
func PrintReport(w io.Writer, r Report) {
	fmt.Fprintf(w, "# eval report\n")
	fmt.Fprintf(w, "cases                %d\n", r.Total)
	fmt.Fprintf(w, "passed               %d\n", r.Passed)
	fmt.Fprintf(w, "failed               %d\n", r.Failed)
	fmt.Fprintf(w, "primary_accuracy     %.3f\n", r.PrimaryAccuracy)
	fmt.Fprintf(w, "risk_flag_agreement  %.3f\n", r.RiskFlagAgreement)
	fmt.Fprintf(w, "mean_confidence      %.3f\n", r.MeanConfidence)
	fmt.Fprintf(w, "under_0.65           %d\n", r.UnderThreshold065)
	if len(r.PerLanguage) > 0 {
		fmt.Fprintf(w, "\n# per-language\n")
		langs := make([]string, 0, len(r.PerLanguage))
		for lang := range r.PerLanguage {
			langs = append(langs, lang)
		}
		sort.Strings(langs)
		for _, lang := range langs {
			s := r.PerLanguage[lang]
			fmt.Fprintf(w, "%-4s  total=%-3d passed=%-3d primary_accuracy=%.3f\n",
				lang, s.Total, s.Passed, s.PrimaryAccuracy)
		}
	}
	if r.Failed == 0 {
		return
	}
	fmt.Fprintf(w, "\n# failures\n")
	for i := range r.Results {
		res := r.Results[i]
		if res.Pass {
			continue
		}
		fmt.Fprintf(w, "- %s (latency=%dms)\n", res.CaseID, res.LatencyMS)
		if res.Err != nil {
			fmt.Fprintf(w, "    error: %s\n", res.Err.Error())
			continue
		}
		for j := range res.Checks {
			ck := res.Checks[j]
			if ck.Pass {
				continue
			}
			fmt.Fprintf(w, "    %s: want=%s got=%s\n", ck.Name, ck.Want, ck.Got)
		}
	}
}

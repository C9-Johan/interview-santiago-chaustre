package decide

import "regexp"

type restrictedCheck struct {
	name    string
	pattern *regexp.Regexp
}

var restrictedChecks = []restrictedCheck{
	{"off_platform_payment", regexp.MustCompile(`(?i)\b(venmo|cashapp|cash app|zelle|paypal|wire transfer|bank transfer|crypto|bitcoin|usdt|western union)\b`)},
	{"contact_bypass", regexp.MustCompile(`(?i)\b(whatsapp|telegram|signal|text me (at|on)|email me at|my number is)\b`)},
	{"address_leak", regexp.MustCompile(`(?i)\b\d{1,5}\s+\w+(\s\w+)*\s+(street|st|avenue|ave|road|rd|blvd|boulevard|lane|ln|drive|dr|court|ct|place|pl)\b`)},
	{"guarantee_language", regexp.MustCompile(`(?i)\b(guarantee(d)?|we promise|100% (safe|quiet)|no issues whatsoever)\b`)},
	{"discount_offer", regexp.MustCompile(`(?i)\b(discount|special rate|lower price|knock off|take off \$)\b`)},
}

// RestrictedContentHits returns the names of the patterns matched by body,
// ordered by first occurrence in restrictedChecks. Empty when clean.
func RestrictedContentHits(body string) []string {
	hits := make([]string, 0, 2)
	for _, c := range restrictedChecks {
		if c.pattern.MatchString(body) {
			hits = append(hits, c.name)
		}
	}
	return hits
}

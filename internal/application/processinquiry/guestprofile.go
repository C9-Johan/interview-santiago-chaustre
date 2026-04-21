// Package processinquiry implements the top-level ProcessInquiry orchestrator
// use case plus helpers specific to orchestration (guest-profile compression,
// auto-replay on boot).
package processinquiry

import (
	"fmt"
	"sort"
	"strings"

	"github.com/chaustre/inquiryiq/internal/domain"
)

const maxProfileChars = 300

// BuildGuestProfile compresses up to N prior ConversationMemoryRecord values
// for the same guest into one advisory paragraph (<=300 chars) fed to the
// classifier and generator prompts. Pure — no LLM call. Returns "" when
// records is empty.
func BuildGuestProfile(records []domain.ConversationMemoryRecord) string {
	if len(records) == 0 {
		return ""
	}
	autoSends, escalations := countOutcomes(records)
	reasons := aggregateReasons(records)
	var b strings.Builder
	fmt.Fprintf(&b, "guest has %d prior conversations on this platform; %d auto-sent, %d escalated.",
		len(records), autoSends, escalations)
	if top := topReasons(reasons, 3); len(top) > 0 {
		fmt.Fprintf(&b, " Common escalation reasons: %s.", strings.Join(top, ", "))
	}
	if pe := mostRecentPets(records); pe != nil {
		fmt.Fprintf(&b, " Prior trips indicated pets=%t.", *pe)
	}
	out := b.String()
	if len(out) > maxProfileChars {
		return out[:maxProfileChars]
	}
	return out
}

func countOutcomes(records []domain.ConversationMemoryRecord) (int, int) {
	var autoSends, escalations int
	for i := range records {
		if records[i].LastAutoSendAt != nil {
			autoSends++
		}
		if records[i].LastEscalationAt != nil {
			escalations++
		}
	}
	return autoSends, escalations
}

func aggregateReasons(records []domain.ConversationMemoryRecord) map[string]int {
	reasons := map[string]int{}
	for i := range records {
		for _, r := range records[i].EscalationReasons {
			reasons[r]++
		}
	}
	return reasons
}

func topReasons(m map[string]int, n int) []string {
	if len(m) == 0 {
		return nil
	}
	type kv struct {
		k string
		v int
	}
	kvs := make([]kv, 0, len(m))
	for k, v := range m {
		kvs = append(kvs, kv{k, v})
	}
	sort.SliceStable(kvs, func(i, j int) bool {
		if kvs[i].v != kvs[j].v {
			return kvs[i].v > kvs[j].v
		}
		return kvs[i].k < kvs[j].k
	})
	if n > len(kvs) {
		n = len(kvs)
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, kvs[i].k)
	}
	return out
}

func mostRecentPets(records []domain.ConversationMemoryRecord) *bool {
	for i := range records {
		if p := records[i].KnownEntities.Pets; p != nil {
			return p
		}
	}
	return nil
}

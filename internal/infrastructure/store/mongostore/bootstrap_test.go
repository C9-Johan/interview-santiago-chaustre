package mongostore

import (
	"slices"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// TestCollectionSpecsCoverAllCollections guards against a future collection
// being added to Collections without a matching bootstrap spec — a silent
// omission would leave that collection unvalidated and unindexed.
func TestCollectionSpecsCoverAllCollections(t *testing.T) {
	t.Parallel()
	c := DefaultCollections()
	want := map[string]bool{
		c.Webhooks:           true,
		c.Classifications:    true,
		c.Escalations:        true,
		c.ConversationMemory: true,
		c.Conversions:        true,
	}
	got := collectionSpecs(c)
	if len(got) != len(want) {
		t.Fatalf("got %d specs, want %d", len(got), len(want))
	}
	for i := range got {
		spec := got[i]
		if !want[spec.name] {
			t.Errorf("unexpected collection %q", spec.name)
		}
		if spec.validator == nil {
			t.Errorf("%s: validator is nil — server-side schema will not be enforced", spec.name)
		}
		if _, ok := spec.validator["$jsonSchema"]; !ok {
			t.Errorf("%s: validator missing $jsonSchema key", spec.name)
		}
		if len(spec.indexes) == 0 {
			t.Errorf("%s: no indexes declared", spec.name)
		}
	}
}

// TestValidatorsRequireInvariantFields pins the minimum required-field set per
// collection. These fields are load-bearing: dropping them would corrupt
// lookups (post_id), ordering (created_at), or uniqueness (reservation_id).
func TestValidatorsRequireInvariantFields(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		validator bson.M
		required  []string
	}{
		{"webhooks", webhooksValidator(), []string{"post_id", "received_at", "raw_body"}},
		{"classifications", classificationsValidator(), []string{"post_id", "payload", "updated_at"}},
		{"escalations", escalationsValidator(), []string{"id", "post_id", "reason", "created_at", "payload"}},
		{"conversation_memory", conversationMemoryValidator(), []string{"conversation_key", "updated_at", "payload"}},
		{"conversions", conversionsValidator(), []string{"reservation_id", "managed_at"}},
	}
	for i := range cases {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			schema, ok := tc.validator["$jsonSchema"].(bson.M)
			if !ok {
				t.Fatalf("$jsonSchema is not a bson.M")
			}
			req, ok := schema["required"].([]string)
			if !ok {
				t.Fatalf("required is not []string: %T", schema["required"])
			}
			for _, f := range tc.required {
				if !slices.Contains(req, f) {
					t.Errorf("missing required field %q", f)
				}
			}
		})
	}
}

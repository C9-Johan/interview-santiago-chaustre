package mongostore

import "go.mongodb.org/mongo-driver/v2/bson"

// Validators are intentionally minimal: only invariants the Go layer cannot
// recover from (missing post_id, wrong BSON type for a timestamp) are enforced
// server-side. Optional/evolving fields are left unconstrained so schema
// evolution doesn't require a migration round-trip for every PR.
//
// All validators include `"additionalProperties": true` implicitly — MongoDB's
// $jsonSchema defaults to permissive unless explicitly restricted.

func webhooksValidator() bson.M {
	return bson.M{
		"$jsonSchema": bson.M{
			"bsonType": "object",
			"required": []string{"post_id", "received_at", "raw_body"},
			"properties": bson.M{
				"post_id":     bson.M{"bsonType": "string"},
				"svix_id":     bson.M{"bsonType": "string"},
				"received_at": bson.M{"bsonType": "date"},
				"raw_body":    bson.M{"bsonType": "binData"},
				"headers":     bson.M{"bsonType": "object"},
				"conv_raw_id": bson.M{"bsonType": "string"},
				"trace_id":    bson.M{"bsonType": "string"},
			},
		},
	}
}

func classificationsValidator() bson.M {
	return bson.M{
		"$jsonSchema": bson.M{
			"bsonType": "object",
			"required": []string{"post_id", "payload", "updated_at"},
			"properties": bson.M{
				"post_id":    bson.M{"bsonType": "string"},
				"payload":    bson.M{"bsonType": "binData"},
				"updated_at": bson.M{"bsonType": "date"},
			},
		},
	}
}

func repliesValidator() bson.M {
	return bson.M{
		"$jsonSchema": bson.M{
			"bsonType": "object",
			"required": []string{"post_id", "payload", "updated_at"},
			"properties": bson.M{
				"post_id":    bson.M{"bsonType": "string"},
				"payload":    bson.M{"bsonType": "binData"},
				"updated_at": bson.M{"bsonType": "date"},
			},
		},
	}
}

func escalationsValidator() bson.M {
	return bson.M{
		"$jsonSchema": bson.M{
			"bsonType": "object",
			"required": []string{"id", "post_id", "reason", "created_at", "payload"},
			"properties": bson.M{
				"id":               bson.M{"bsonType": "string"},
				"trace_id":         bson.M{"bsonType": "string"},
				"post_id":          bson.M{"bsonType": "string"},
				"conversation_key": bson.M{"bsonType": "string"},
				"guest_id":         bson.M{"bsonType": "string"},
				"platform":         bson.M{"bsonType": "string"},
				"reason":           bson.M{"bsonType": "string"},
				"created_at":       bson.M{"bsonType": []string{"date", "string"}},
				"payload":          bson.M{"bsonType": "binData"},
			},
		},
	}
}

func conversationMemoryValidator() bson.M {
	return bson.M{
		"$jsonSchema": bson.M{
			"bsonType": "object",
			"required": []string{"conversation_key", "updated_at", "payload"},
			"properties": bson.M{
				"conversation_key": bson.M{"bsonType": "string"},
				"guest_id":         bson.M{"bsonType": "string"},
				"updated_at":       bson.M{"bsonType": "date"},
				"payload":          bson.M{"bsonType": "binData"},
			},
		},
	}
}

func conversionsValidator() bson.M {
	return bson.M{
		"$jsonSchema": bson.M{
			"bsonType": "object",
			"required": []string{"reservation_id", "managed_at"},
			"properties": bson.M{
				"reservation_id":   bson.M{"bsonType": "string"},
				"conversation_key": bson.M{"bsonType": "string"},
				"guest_id":         bson.M{"bsonType": "string"},
				"platform":         bson.M{"bsonType": "string"},
				"primary_code":     bson.M{"bsonType": "string"},
				"status":           bson.M{"bsonType": "string"},
				"managed_at":       bson.M{"bsonType": "date"},
				"converted_at":     bson.M{"bsonType": []string{"date", "null"}},
				"payload":          bson.M{"bsonType": "binData"},
			},
		},
	}
}

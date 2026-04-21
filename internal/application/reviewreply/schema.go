package reviewreply

// verdictSchema is the JSON schema the critic's output is validated against.
// issues is an ordered unique set of tags from a closed vocabulary enumerated
// in prompt.go; values outside that vocabulary still pass the schema (enum
// enforcement would require a hard-coded allow-list here too), but the
// prompt's "Use ONLY tags from that list" discipline keeps them bounded.
const verdictSchema = `{
  "type": "object",
  "required": ["pass","issues","confidence","reasoning"],
  "additionalProperties": false,
  "properties": {
    "pass":       {"type": "boolean"},
    "issues":     {"type": "array", "maxItems": 12, "items": {"type": "string", "maxLength": 60}},
    "confidence": {"type": "number", "minimum": 0, "maximum": 1},
    "reasoning":  {"type": "string", "maxLength": 240}
  }
}`

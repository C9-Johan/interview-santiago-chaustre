package classify

// classificationSchema is the JSON schema the Stage A classifier output is
// validated against in Go. DeepSeek only honors response_format: json_object,
// not full json_schema — so we enforce the shape ourselves.
const classificationSchema = `{
  "type": "object",
  "required": ["primary_code","confidence","extracted_entities","risk_flag","next_action","reasoning"],
  "additionalProperties": false,
  "properties": {
    "primary_code":   {"enum": ["G1","G2","Y1","Y2","Y3","Y4","Y5","Y6","Y7","R1","R2","X1"]},
    "secondary_code": {"type": ["string","null"]},
    "confidence":     {"type": "number", "minimum": 0, "maximum": 1},
    "risk_flag":      {"type": "boolean"},
    "risk_reason":    {"type": "string", "maxLength": 200},
    "next_action":    {"enum": ["generate_reply","escalate_human","qualify_question"]},
    "reasoning":      {"type": "string", "maxLength": 240},
    "extracted_entities": {
      "type": "object",
      "additionalProperties": false,
      "properties": {
        "check_in":    {"type": ["string","null"], "format": "date"},
        "check_out":   {"type": ["string","null"], "format": "date"},
        "guest_count": {"type": ["integer","null"], "minimum": 1, "maximum": 20},
        "pets":        {"type": ["boolean","null"]},
        "vehicles":    {"type": ["integer","null"], "minimum": 0, "maximum": 10},
        "listing_hint":{"type": ["string","null"], "maxLength": 120},
        "additional": {
          "type": "array", "maxItems": 8,
          "items": {
            "type": "object",
            "required": ["key","value","value_type","confidence","source"],
            "additionalProperties": false,
            "properties": {
              "key":        {"type": "string", "pattern": "^[a-z][a-z0-9_]{1,39}$"},
              "value":      {"type": "string", "maxLength": 200},
              "value_type": {"enum": ["string","number","bool","list"]},
              "confidence": {"type": "number", "minimum": 0, "maximum": 1},
              "source":     {"type": "string", "maxLength": 120}
            }
          }
        }
      }
    }
  }
}`

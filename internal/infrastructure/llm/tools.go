package llm

import openai "github.com/sashabaranov/go-openai"

// any: go-openai's FunctionDefinition.Parameters is typed as any because JSON
// schema is a free-form payload at the SDK boundary. We pass typed map
// literals in, not interface{} up the stack.

// GetListingTool is declared once and reused by every Chat request that needs
// listing facts.
var GetListingTool = openai.Tool{
	Type: openai.ToolTypeFunction,
	Function: &openai.FunctionDefinition{
		Name:        "get_listing",
		Description: "Look up facts for the listing on this reservation (title, bedrooms, amenities, house rules, base price, neighborhood). Call once if facts are needed for Overview/Explain.",
		Strict:      true,
		Parameters: map[string]any{
			"type":                 "object",
			"required":             []string{"listing_id"},
			"additionalProperties": false,
			"properties": map[string]any{
				"listing_id": map[string]any{"type": "string", "description": "Guesty listing id"},
			},
		},
	},
}

// CheckAvailabilityTool MUST be called before the generator asserts any
// availability or total price (the C.L.O.S.E.R. Sell-Certainty beat).
var CheckAvailabilityTool = openai.Tool{
	Type: openai.ToolTypeFunction,
	Function: &openai.FunctionDefinition{
		Name:        "check_availability",
		Description: "Check whether the listing is available for specific dates and return the total. REQUIRED before filling the Sell-Certainty beat.",
		Strict:      true,
		Parameters: map[string]any{
			"type":                 "object",
			"required":             []string{"listing_id", "from", "to"},
			"additionalProperties": false,
			"properties": map[string]any{
				"listing_id": map[string]any{"type": "string"},
				"from":       map[string]any{"type": "string", "format": "date"},
				"to":         map[string]any{"type": "string", "format": "date"},
			},
		},
	},
}

// GetConversationHistoryTool lets the generator pull older messages when the
// guest references prior context not visible in the thread window.
var GetConversationHistoryTool = openai.Tool{
	Type: openai.ToolTypeFunction,
	Function: &openai.FunctionDefinition{
		Name:        "get_conversation_history",
		Description: "Fetch older messages from this conversation beyond the recent window already provided. Use only when the current guest message references prior context you cannot see.",
		Strict:      true,
		Parameters: map[string]any{
			"type":                 "object",
			"required":             []string{"conversation_id", "limit"},
			"additionalProperties": false,
			"properties": map[string]any{
				"conversation_id": map[string]any{"type": "string"},
				"limit":           map[string]any{"type": "integer", "minimum": 1, "maximum": 30},
				"before_post_id":  map[string]any{"type": "string"},
			},
		},
	},
}

// AllTools is the slice passed to each agent-loop request.
var AllTools = []openai.Tool{GetListingTool, CheckAvailabilityTool, GetConversationHistoryTool}

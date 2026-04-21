package mappers

// NotePayload is the wire body for POST /conversations/{id}/messages.
// Fields are JSON-tagged for the infrastructure/guesty HTTP client.
type NotePayload struct {
	Body string `json:"body"`
	Type string `json:"type"` // always "note" for this service; never "platform"
}

// NoteFromDomain builds the outbound note body. Fixed type="note" — the
// spec is explicit that reaching real guests is out of scope.
func NoteFromDomain(body string) NotePayload {
	return NotePayload{Body: body, Type: "note"}
}

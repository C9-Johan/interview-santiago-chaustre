package domain

// Decision is the output of both gate functions.
type Decision struct {
	AutoSend bool
	Reason   string   // machine-readable: "ok", "code_requires_human", ...
	Detail   []string // human-readable specifics
}

// Toggles are runtime-controllable behavior flags applied in the gate.
type Toggles struct {
	AutoResponseEnabled bool
}

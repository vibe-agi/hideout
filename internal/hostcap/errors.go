package hostcap

// Error is a typed projection failure carrying a stable recovery code. Every
// fail-closed path in the projection layer returns one of these; the broker
// maps Code to a guest-visible recovery record. The Reason is operator-facing
// context and MUST NOT contain a host absolute path, host username, or token.
type Error struct {
	Code   string
	Reason string
}

func (e *Error) Error() string {
	if e == nil {
		return "hostcap: projection failed"
	}
	if e.Reason == "" {
		return e.Code
	}
	return e.Code + ": " + e.Reason
}

// CodeOf returns the recovery code carried by err, or "" if err is not a
// projection Error.
func CodeOf(err error) string {
	if pe, ok := err.(*Error); ok {
		return pe.Code
	}
	return ""
}

package hostcap

// WindowMode selects how the host application places the opened resource.
type WindowMode string

const (
	WindowReuse WindowMode = "reuse"
	WindowNew   WindowMode = "new"
)

// ResourceRef is the guest view of a resource. GuestPath is an absolute guest
// path (the untrusted grammar resolves it from the guest CWD + argument); Core
// maps it to a host path via the session-bound mapping and re-checks workspace
// containment. RelativePath is an optional audit-friendly form. The host path
// is never carried here.
type ResourceRef struct {
	Kind         ResourceKind `json:"kind"`
	GuestPath    string       `json:"guestPath"`
	RelativePath string       `json:"relativePath,omitempty"`
}

// Location is an optional cursor position applied to Resources[0].
type Location struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

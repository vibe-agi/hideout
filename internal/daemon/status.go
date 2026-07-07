package daemon

const (
	statusVersion = "hideout.daemon-status/v1"
	eventVersion  = "hideout.daemon-event/v1"
)

// Status is the daemon status/inventory shape (schemas/daemon-status.schema.json).
type Status struct {
	Version    string             `json:"version"`
	State      string             `json:"state"`
	StartedAt  string             `json:"startedAt,omitempty"`
	Transport  StatusTransport    `json:"transport"`
	Background []BackgroundStatus `json:"background,omitempty"`
}

type StatusTransport struct {
	Socket string `json:"socket"`
}

// BackgroundStatus reports a background operation (populated in the US3 slice).
type BackgroundStatus struct {
	ID     string `json:"id"`
	Op     string `json:"op"`
	Status string `json:"status"`
}

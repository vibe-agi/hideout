package daemon

import (
	"net/http"
	"slices"
	"strings"
)

const RouteClassDaemonSpecific = "daemon-specific"

const loopbackUIPath = "/"

type EndpointSpec struct {
	Method      string
	Path        string
	Class       string
	Owner       string
	Description string
}

func DaemonEndpoints() []EndpointSpec {
	out := append([]EndpointSpec(nil), daemonEndpointSpecs...)
	slices.SortFunc(out, func(a, b EndpointSpec) int {
		if c := strings.Compare(a.Method, b.Method); c != 0 {
			return c
		}
		return strings.Compare(a.Path, b.Path)
	})
	return out
}

func RecognizeDaemonEndpoint(method, path string) (EndpointSpec, bool) {
	for _, spec := range daemonEndpointSpecs {
		if spec.Method == method && spec.Path == path {
			return spec, true
		}
	}
	return EndpointSpec{}, false
}

func daemonEndpoint(method, path, description string) EndpointSpec {
	return EndpointSpec{
		Method:      method,
		Path:        path,
		Class:       RouteClassDaemonSpecific,
		Owner:       "internal/daemon.Daemon",
		Description: description,
	}
}

var daemonEndpointSpecs = []EndpointSpec{
	{
		Method:      http.MethodGet,
		Path:        loopbackUIPath,
		Class:       RouteClassDaemonSpecific,
		Owner:       "internal/daemon.startLoopbackUI",
		Description: "serve the authenticated loopback operator console root",
	},
	daemonEndpoint(http.MethodPost, backgroundPath, "submit existing typed env stop/clean as background work"),
	daemonEndpoint(http.MethodGet, eventsPath, "stream live daemon events"),
	daemonEndpoint(http.MethodGet, statusPath, "read daemon status"),
	daemonEndpoint(http.MethodPost, stopPath, "request ordered daemon shutdown"),
}

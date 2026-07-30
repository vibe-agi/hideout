package manager

import (
	"net/http"
	"slices"
	"strings"
	"unicode"
)

const (
	RouteClassManagerAPI          = "manager-api"
	DefaultRequestBodyLimit int64 = 1 << 20
	SecretRequestBodyLimit  int64 = 16 << 10
)

type RouteSpec struct {
	Method              string
	Path                string
	Resource            string
	Class               string
	Owner               string
	Description         string
	MaxRequestBodyBytes int64
	Sensitive           bool
	NoStore             bool
	NoBodyLog           bool
}

func (r RouteSpec) SamplePath() string {
	return "/api/v1/" + sampleResource(r.Resource)
}

func (r RouteSpec) ResponseResource() string {
	switch r.Resource {
	case "decisions/{id}":
		return "decision/inspect"
	case "decisions/{id}/claim":
		return "decision/claim"
	case "decisions/{id}/release":
		return "decision/release"
	case "decisions/{id}/approve":
		return "decision/approve"
	case "decisions/{id}/deny":
		return "decision/deny"
	case "decisions/{id}/reopen":
		return "decision/reopen"
	case "decisions/{id}/revoke":
		return "decision/revoke"
	case "notices/{id}":
		return "notice/inspect"
	case "notices/{id}/ack":
		return "notice/ack"
	case "operations/{operation}":
		return "operation/inspect"
	case "profiles/{profile}/projection":
		return "profile/projection"
	default:
		return r.Resource
	}
}

func ManagerRoutes() []RouteSpec {
	out := append([]RouteSpec(nil), managerRouteSpecs...)
	slices.SortFunc(out, func(a, b RouteSpec) int {
		if c := strings.Compare(a.Method, b.Method); c != 0 {
			return c
		}
		return strings.Compare(a.Path, b.Path)
	})
	return out
}

func RecognizeManagerRoute(method, path string) (RouteSpec, bool) {
	resource, ok := strings.CutPrefix(path, "/api/v1/")
	if !ok || resource == "" {
		return RouteSpec{}, false
	}
	return RecognizeManagerResource(method, resource)
}

func RecognizeManagerResource(method, resource string) (RouteSpec, bool) {
	for _, spec := range managerRouteSpecs {
		if spec.Method != method {
			continue
		}
		if routeResourceMatches(spec.Resource, resource) {
			return spec, true
		}
	}
	return RouteSpec{}, false
}

func RecognizeManagerResourceAnyMethod(resource string) (RouteSpec, bool) {
	for _, spec := range managerRouteSpecs {
		if routeResourceMatches(spec.Resource, resource) {
			return spec, true
		}
	}
	return RouteSpec{}, false
}

func routeResourceMatches(pattern, resource string) bool {
	if !strings.Contains(pattern, "{") {
		return pattern == resource
	}
	pp := strings.Split(pattern, "/")
	rp := strings.Split(resource, "/")
	if len(pp) != len(rp) {
		return false
	}
	for i := range pp {
		if routeParameterName(pp[i]) != "" {
			if !validRouteParameterValue(rp[i]) {
				return false
			}
			continue
		}
		if pp[i] != rp[i] {
			return false
		}
	}
	return true
}

func sampleResource(pattern string) string {
	segments := strings.Split(pattern, "/")
	for index, segment := range segments {
		name := routeParameterName(segment)
		if name == "" {
			continue
		}
		switch name {
		case "id":
			segments[index] = "sample-id"
		case "profile":
			segments[index] = "default"
		case "operation":
			segments[index] = "op_sample0000"
		case "session":
			segments[index] = "ses_sample"
		default:
			segments[index] = "sample-" + name
		}
	}
	return strings.Join(segments, "/")
}

func routeSpec(method, resource, description string) RouteSpec {
	spec := RouteSpec{
		Method:      method,
		Path:        "/api/v1/" + resource,
		Resource:    resource,
		Class:       RouteClassManagerAPI,
		Owner:       "internal/manager.API",
		Description: description,
		NoStore:     true,
		NoBodyLog:   true,
	}
	if method == http.MethodPost {
		spec.MaxRequestBodyBytes = DefaultRequestBodyLimit
	}
	return spec
}

func sensitiveRouteSpec(method, resource, description string, maxRequestBodyBytes int64) RouteSpec {
	spec := routeSpec(method, resource, description)
	spec.Sensitive = true
	if method == http.MethodPost && maxRequestBodyBytes > 0 {
		spec.MaxRequestBodyBytes = maxRequestBodyBytes
	}
	return spec
}

func routeParameterName(segment string) string {
	if len(segment) < 3 || segment[0] != '{' || segment[len(segment)-1] != '}' {
		return ""
	}
	name := segment[1 : len(segment)-1]
	for index, character := range name {
		if character >= 'a' && character <= 'z' ||
			index > 0 && character >= 'A' && character <= 'Z' {
			continue
		}
		return ""
	}
	return name
}

func validRouteParameterValue(value string) bool {
	if value == "" || value == "." || value == ".." || len(value) > 256 {
		return false
	}
	for _, character := range value {
		if character == '/' || character == '\\' || unicode.IsControl(character) {
			return false
		}
	}
	return true
}

var managerRouteSpecs = []RouteSpec{
	routeSpec(http.MethodGet, "adapter-pack/inspect", "inspect one adapter pack"),
	routeSpec(http.MethodGet, "adapter-packs", "list adapter packs"),
	routeSpec(http.MethodGet, "activity/coverage", "query exact-owner activity coverage"),
	routeSpec(http.MethodGet, "activity/events", "query exact-owner activity events"),
	routeSpec(http.MethodGet, "activity/executions", "query exact-owner execution trees"),
	routeSpec(http.MethodGet, "activity/risks", "query exact-owner activity risks"),
	routeSpec(http.MethodGet, "activity/summary", "query exact-owner activity summary"),
	routeSpec(http.MethodGet, "audit", "audit summary"),
	routeSpec(http.MethodGet, "audit/events", "audit event slice"),
	routeSpec(http.MethodGet, "backends", "backend summary"),
	routeSpec(http.MethodGet, "broker", "broker summary"),
	routeSpec(http.MethodGet, "bundles", "bundle summary"),
	routeSpec(http.MethodGet, "capabilities", "capability summary"),
	routeSpec(http.MethodGet, "decision/inspect", "inspect decision by query"),
	routeSpec(http.MethodGet, "decision/status", "decision status summary"),
	routeSpec(http.MethodGet, "decisions", "decision summary"),
	routeSpec(http.MethodGet, "decisions/{id}", "inspect decision by path"),
	routeSpec(http.MethodGet, "environments", "environment summary"),
	routeSpec(http.MethodGet, "hostfs/write/status", "HostFS write status"),
	routeSpec(http.MethodGet, "app/list", "list host application packs"),
	routeSpec(http.MethodGet, "app/inspect", "inspect one host application pack"),
	routeSpec(http.MethodGet, "init", "init summary"),
	routeSpec(http.MethodGet, "network", "network summary"),
	routeSpec(http.MethodGet, "notice/inspect", "inspect notice by query"),
	routeSpec(http.MethodGet, "notices", "notice summary"),
	routeSpec(http.MethodGet, "notices/{id}", "inspect notice by path"),
	routeSpec(http.MethodGet, "operator/snapshot", "authoritative operator console snapshot"),
	routeSpec(http.MethodGet, "operations/{operation}", "inspect durable operation"),
	routeSpec(http.MethodGet, "overview", "full overview"),
	routeSpec(http.MethodGet, "profiles", "profile summary"),
	routeSpec(http.MethodGet, "profiles/{profile}/projection", "inspect authoritative profile projection"),
	routeSpec(http.MethodGet, "projects", "project summary"),
	routeSpec(http.MethodGet, "run/status", "run status"),
	routeSpec(http.MethodGet, "runtime/catalog", "runtime catalog and contract"),
	routeSpec(http.MethodGet, "runtime/status", "environment runtime status"),
	routeSpec(http.MethodGet, "secrets", "secret summary"),
	routeSpec(http.MethodGet, "sessions", "session summary"),
	routeSpec(http.MethodGet, "settings", "settings summary"),

	routeSpec(http.MethodPost, "adapter-pack/apply", "apply adapter pack action"),
	routeSpec(http.MethodPost, "adapter-pack/plan", "plan adapter pack action"),
	sensitiveRouteSpec(http.MethodPost, "activity/export/apply", "apply reviewed exact-owner activity export", DefaultRequestBodyLimit),
	sensitiveRouteSpec(http.MethodPost, "activity/export/plan", "plan exact-owner activity export", DefaultRequestBodyLimit),
	routeSpec(http.MethodPost, "decision/approve", "approve decision"),
	routeSpec(http.MethodPost, "decision/claim", "claim decision"),
	routeSpec(http.MethodPost, "decision/deny", "deny decision"),
	sensitiveRouteSpec(http.MethodPost, "decision/release", "release an exact decision claim", DefaultRequestBodyLimit),
	routeSpec(http.MethodPost, "decision/reopen", "reopen eligible decision"),
	routeSpec(http.MethodPost, "decision/revoke", "revoke active trusted grant"),
	routeSpec(http.MethodPost, "decisions/{id}/approve", "approve decision by path"),
	routeSpec(http.MethodPost, "decisions/{id}/claim", "claim decision by path"),
	routeSpec(http.MethodPost, "decisions/{id}/deny", "deny decision by path"),
	sensitiveRouteSpec(http.MethodPost, "decisions/{id}/release", "release an exact decision claim by path", DefaultRequestBodyLimit),
	routeSpec(http.MethodPost, "decisions/{id}/reopen", "reopen eligible decision by path"),
	routeSpec(http.MethodPost, "decisions/{id}/revoke", "revoke active trusted grant by path"),
	routeSpec(http.MethodPost, "environment/clean/apply", "apply environment clean"),
	routeSpec(http.MethodPost, "environment/clean/plan", "plan environment clean"),
	routeSpec(http.MethodPost, "environment/delete/apply", "apply reviewed environment delete"),
	routeSpec(http.MethodPost, "environment/delete/plan", "plan environment delete"),
	routeSpec(http.MethodPost, "environment/stop/apply", "apply environment stop"),
	routeSpec(http.MethodPost, "environment/stop/plan", "plan environment stop"),
	routeSpec(http.MethodPost, "evidence/export/apply", "apply evidence export"),
	routeSpec(http.MethodPost, "evidence/export/plan", "plan evidence export"),
	routeSpec(http.MethodPost, "hostfs/write/apply", "apply HostFS write decision"),
	routeSpec(http.MethodPost, "hostfs/write/claim", "claim HostFS write decision"),
	routeSpec(http.MethodPost, "hostfs/write/discard", "discard HostFS write decision"),
	routeSpec(http.MethodPost, "hostfs/write/plan", "plan HostFS write"),
	routeSpec(http.MethodPost, "app/apply", "apply host application pack action"),
	routeSpec(http.MethodPost, "app/plan", "plan host application pack action"),
	routeSpec(http.MethodPost, "init/apply", "apply init"),
	routeSpec(http.MethodPost, "init/plan", "plan init"),
	routeSpec(http.MethodPost, "notice/ack", "ack notice"),
	routeSpec(http.MethodPost, "notices/{id}/ack", "ack notice by path"),
	routeSpec(http.MethodPost, "profile/command-proxy/apply", "apply command proxy profile change"),
	routeSpec(http.MethodPost, "profile/command-proxy/plan", "plan command proxy profile change"),
	sensitiveRouteSpec(http.MethodPost, "profile/env/apply", "apply profile environment change", DefaultRequestBodyLimit),
	sensitiveRouteSpec(http.MethodPost, "profile/env/plan", "plan profile environment change", DefaultRequestBodyLimit),
	routeSpec(http.MethodPost, "profile/hostfs/apply", "apply HostFS profile rule change"),
	routeSpec(http.MethodPost, "profile/hostfs/plan", "plan HostFS profile rule change"),
	routeSpec(http.MethodPost, "profile/network/apply", "apply profile network posture change"),
	routeSpec(http.MethodPost, "profile/network/plan", "plan profile network posture change"),
	sensitiveRouteSpec(http.MethodPost, "profile/transaction/apply", "apply a typed profile transaction", DefaultRequestBodyLimit),
	sensitiveRouteSpec(http.MethodPost, "profile/transaction/plan", "plan a typed profile transaction", DefaultRequestBodyLimit),
	sensitiveRouteSpec(http.MethodPost, "run/apply", "apply run", DefaultRequestBodyLimit),
	sensitiveRouteSpec(http.MethodPost, "run/plan", "plan run", DefaultRequestBodyLimit),
	routeSpec(http.MethodPost, "runtime/verify/apply", "apply runtime verification"),
	routeSpec(http.MethodPost, "runtime/verify/plan", "plan runtime verification"),
	sensitiveRouteSpec(http.MethodPost, "secret/apply", "apply secret metadata mutation", SecretRequestBodyLimit),
	sensitiveRouteSpec(http.MethodPost, "secret/plan", "plan secret metadata mutation", SecretRequestBodyLimit),
}

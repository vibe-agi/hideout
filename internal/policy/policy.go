package policy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"

	"github.com/vibe-agi/hideout/internal/profile"
)

type Decision string
type Route string

const (
	Allow     Decision = "allow"
	Deny      Decision = "deny"
	Ask       Decision = "ask"
	AuditOnly Decision = "audit-only"

	GuestDirect Route = "guest-direct"
	GuestExec   Route = "guest-exec"
	HostBroker  Route = "host-broker"
	Fake        Route = "fake"
	DenyRoute   Route = "deny"
)

const (
	MaxScriptSourceBytes = 64 * 1024
	MaxScriptInputBytes  = 64 * 1024
	MaxScriptOutputBytes = 16 * 1024
	ScriptTimeout        = 100 * time.Millisecond
)

var ErrScriptTimeout = errors.New("policy script execution timed out")

type CommandContext struct {
	Version   string                 `json:"version"`
	Profile   map[string]string      `json:"profile"`
	Session   map[string]any         `json:"session"`
	Subject   string                 `json:"subject"`
	Command   map[string]any         `json:"command"`
	Workspace map[string]any         `json:"workspace"`
	Env       map[string]any         `json:"env"`
	Network   map[string]any         `json:"network"`
	Extra     map[string]interface{} `json:"extra,omitempty"`
}

type AuditContext struct {
	Version  string                 `json:"version"`
	Profile  map[string]string      `json:"profile"`
	Session  map[string]any         `json:"session"`
	Subject  string                 `json:"subject,omitempty"`
	Action   string                 `json:"action"`
	Decision string                 `json:"decision"`
	Details  map[string]any         `json:"details"`
	Extra    map[string]interface{} `json:"extra,omitempty"`
}

type Proposal struct {
	Decision  Decision       `json:"decision"`
	Route     Route          `json:"route"`
	Action    string         `json:"action"`
	Resources []string       `json:"resources"`
	Reason    string         `json:"reason"`
	Audit     map[string]any `json:"audit,omitempty"`
}

type AuditRedaction struct {
	Details map[string]any `json:"details"`
	Reason  string         `json:"reason,omitempty"`
	Audit   map[string]any `json:"audit,omitempty"`
}

type Evaluator struct {
	MaxCapabilities []string
	Open            profile.OpenCapability
	ResolveHost     HostResolver
}

type HostResolver func(host string) ([]netip.Addr, error)

func NewEvaluator(p profile.Profile) Evaluator {
	return Evaluator{
		MaxCapabilities: append([]string(nil), p.Policy.MaxCapabilities...),
		Open:            p.HostCapabilities.Open,
		ResolveHost:     defaultResolveHost,
	}
}

func (e Evaluator) EvaluateOpen(target string) (Proposal, error) {
	u, err := url.Parse(target)
	if err == nil && (u.Scheme == "http" || u.Scheme == "https") {
		if localBrowserTarget(u.Hostname()) {
			return e.Validate(Proposal{
				Decision:  Deny,
				Route:     DenyRoute,
				Action:    "host.open",
				Resources: []string{"url-local:" + u.Scheme},
				Reason:    "URL open to localhost or a private host network address is denied by Phase 1 browser boundary",
			})
		}
		if !e.Open.AllowURLs {
			return e.Validate(Proposal{
				Decision:  Deny,
				Route:     DenyRoute,
				Action:    "host.open",
				Resources: []string{"url:" + u.Scheme},
				Reason:    "URL open is disabled by profile policy",
			})
		}
		if addrs, err := e.resolveHost(u.Hostname()); err != nil {
			return e.Validate(Proposal{
				Decision:  Deny,
				Route:     DenyRoute,
				Action:    "host.open",
				Resources: []string{"url-local:" + u.Scheme},
				Reason:    "URL host DNS resolution failed by Phase 1 browser boundary",
			})
		} else {
			for _, addr := range addrs {
				if localBrowserAddr(addr) {
					return e.Validate(Proposal{
						Decision:  Deny,
						Route:     DenyRoute,
						Action:    "host.open",
						Resources: []string{"url-local:" + u.Scheme},
						Reason:    "URL host resolves to localhost or a private host network address and is denied by Phase 1 browser boundary",
					})
				}
			}
		}
		return e.Validate(Proposal{
			Decision:  Allow,
			Route:     HostBroker,
			Action:    "host.open",
			Resources: []string{"url:" + u.Scheme},
			Reason:    "web URL open is allowed through host broker",
		})
	}
	return e.Validate(Proposal{
		Decision:  Deny,
		Route:     DenyRoute,
		Action:    "host.open",
		Resources: []string{"unknown"},
		Reason:    "open target is not an allowed http/https URL",
	})
}

func (e Evaluator) resolveHost(host string) ([]netip.Addr, error) {
	normalized := normalizeBrowserHost(host)
	if normalized == "" {
		return nil, errors.New("URL host is empty")
	}
	if before, _, ok := strings.Cut(normalized, "%"); ok {
		normalized = before
	}
	if _, err := netip.ParseAddr(normalized); err == nil {
		return nil, nil
	}
	resolve := e.ResolveHost
	if resolve == nil {
		resolve = defaultResolveHost
	}
	addrs, err := resolve(normalized)
	if err != nil {
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, errors.New("URL host resolved to no addresses")
	}
	return addrs, nil
}

func localBrowserTarget(host string) bool {
	host = normalizeBrowserHost(host)
	if host == "" {
		return true
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || host == "local" || strings.HasSuffix(host, ".local") {
		return true
	}
	switch host {
	case "host.docker.internal", "gateway.docker.internal", "kubernetes.docker.internal", "host.lima.internal", "host.containers.internal":
		return true
	}
	if before, _, ok := strings.Cut(host, "%"); ok {
		host = before
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	return localBrowserAddr(addr)
}

func normalizeBrowserHost(host string) string {
	host = strings.TrimSpace(strings.ToLower(host))
	return strings.TrimSuffix(host, ".")
}

func localBrowserAddr(addr netip.Addr) bool {
	addr = addr.Unmap()
	return addr.IsLoopback() ||
		addr.IsPrivate() ||
		addrInAnyPrefix(addr, nonPublicBrowserURLPrefixes) ||
		addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() ||
		addr.IsMulticast() ||
		addr.IsUnspecified()
}

var nonPublicBrowserURLPrefixes = []netip.Prefix{
	netip.MustParsePrefix("100.64.0.0/10"), // carrier-grade NAT
	netip.MustParsePrefix("198.18.0.0/15"), // benchmarking and Hideout TUN range
}

func addrInAnyPrefix(addr netip.Addr, prefixes []netip.Prefix) bool {
	for _, prefix := range prefixes {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

type resolveCacheEntry struct {
	addrs []netip.Addr
}

var resolveHostCache sync.Map

func defaultResolveHost(host string) ([]netip.Addr, error) {
	normalized := normalizeBrowserHost(host)
	if cached, ok := resolveHostCache.Load(normalized); ok {
		entry := cached.(resolveCacheEntry)
		return cloneResolvedAddrs(entry.addrs), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", normalized)
	if err != nil {
		return nil, err
	}
	entry := resolveCacheEntry{addrs: cloneResolvedAddrs(addrs)}
	resolveHostCache.Store(normalized, entry)
	return cloneResolvedAddrs(entry.addrs), nil
}

func cloneResolvedAddrs(addrs []netip.Addr) []netip.Addr {
	if addrs == nil {
		return nil
	}
	return append([]netip.Addr(nil), addrs...)
}

func (e Evaluator) EvaluateWorkspaceOpen() (Proposal, error) {
	if !e.Open.AllowWorkspaceFiles {
		return e.Validate(Proposal{
			Decision:  Deny,
			Route:     DenyRoute,
			Action:    "host.open",
			Resources: []string{"workspace-file"},
			Reason:    "workspace file open is disabled by profile policy",
		})
	}
	return e.Validate(Proposal{
		Decision:  Allow,
		Route:     HostBroker,
		Action:    "host.open",
		Resources: []string{"workspace-file"},
		Reason:    "workspace file open is allowed through host broker",
	})
}

func (e Evaluator) Validate(p Proposal) (Proposal, error) {
	if p.Decision == "" {
		return p, errors.New("policy proposal decision is required")
	}
	switch p.Decision {
	case Allow, Deny, Ask, AuditOnly:
	default:
		return p, fmt.Errorf("unsupported decision %q", p.Decision)
	}
	if strings.TrimSpace(p.Action) == "" {
		return p, errors.New("policy proposal action is required")
	}
	if strings.HasPrefix(p.Action, "host.exec") || p.Action == "host.run" || p.Action == "host.native" {
		return p, fmt.Errorf("generic host execution action %q is forbidden", p.Action)
	}
	switch p.Action {
	case "host.open", "guest.exec", "network.connect":
	default:
		return p, fmt.Errorf("unsupported action %q", p.Action)
	}
	if !slices.Contains(e.MaxCapabilities, p.Action) {
		return p, fmt.Errorf("action %q exceeds profile max capabilities", p.Action)
	}
	if p.Route == "" {
		return p, errors.New("policy proposal route is required")
	}
	switch p.Route {
	case GuestDirect, GuestExec, HostBroker, Fake, DenyRoute:
	default:
		return p, fmt.Errorf("unsupported route %q", p.Route)
	}
	if p.Action == "host.open" && p.Decision != Deny && p.Route != HostBroker {
		return p, errors.New("host.open must use host-broker route unless denied")
	}
	if p.Action == "guest.exec" && p.Decision != Deny && p.Route != GuestDirect && p.Route != GuestExec {
		return p, errors.New("guest.exec must use guest-direct or guest-exec route")
	}
	if p.Action == "network.connect" && p.Decision != Deny && p.Route != GuestDirect {
		return p, errors.New("network.connect must use guest-direct route")
	}
	if p.Decision == Deny && p.Route != DenyRoute {
		return p, errors.New("deny decision must use deny route")
	}
	if len(p.Resources) == 0 {
		return p, errors.New("policy proposal resources are required")
	}
	for _, r := range p.Resources {
		if strings.TrimSpace(r) == "" {
			return p, errors.New("policy resources must not be empty")
		}
		if resourceContainsSecretValue(r) {
			return p, errors.New("policy resources must not contain secret values")
		}
	}
	if strings.TrimSpace(p.Reason) == "" {
		return p, errors.New("policy proposal reason is required")
	}
	return p, nil
}

func resourceContainsSecretValue(resource string) bool {
	if containsURLUserInfo(resource) {
		return true
	}
	normalized := strings.ToLower(resource)
	for _, marker := range []string{
		"token=",
		"password=",
		"passwd=",
		"credential=",
		"authorization=",
		"cookie=",
		"privatekey=",
		"private-key=",
		"private_key=",
		"apikey=",
		"api-key=",
		"api_key=",
		"accesskey=",
		"access-key=",
		"access_key=",
		"secret=",
		"auth=",
		"code=",
		"key=",
	} {
		if strings.Contains(normalized, marker) {
			return true
		}
	}
	return false
}

func containsURLUserInfo(value string) bool {
	start := strings.Index(value, "://")
	if start < 0 {
		return false
	}
	authority := value[start+3:]
	if end := strings.IndexAny(authority, "/?#"); end >= 0 {
		authority = authority[:end]
	}
	return strings.Contains(authority, "@")
}

func (e Evaluator) RunCommandScript(source, entrypoint string, ctx CommandContext) (Proposal, error) {
	out, err := runPolicyScript(source, entrypoint, ctx)
	if err != nil {
		return Proposal{}, err
	}
	var proposal Proposal
	if err := decodeStrictScriptOutput(out, &proposal); err != nil {
		return Proposal{}, err
	}
	return e.Validate(proposal)
}

func (e Evaluator) RunAuditRedactScript(source, entrypoint string, ctx AuditContext) (AuditRedaction, error) {
	out, err := runPolicyScript(source, entrypoint, ctx)
	if err != nil {
		return AuditRedaction{}, err
	}
	var redaction AuditRedaction
	if err := decodeStrictScriptOutput(out, &redaction); err != nil {
		return AuditRedaction{}, err
	}
	if redaction.Details == nil {
		return AuditRedaction{}, errors.New("audit redaction details are required")
	}
	return redaction, nil
}

func decodeStrictScriptOutput(data []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("policy script output must contain a single JSON value")
		}
		return err
	}
	return nil
}

func runPolicyScript(source, entrypoint string, ctx any) ([]byte, error) {
	if len(source) > MaxScriptSourceBytes {
		return nil, fmt.Errorf("policy script source exceeds %d bytes", MaxScriptSourceBytes)
	}
	ctxData, err := json.Marshal(ctx)
	if err != nil {
		return nil, err
	}
	if len(ctxData) > MaxScriptInputBytes {
		return nil, fmt.Errorf("policy script input exceeds %d bytes", MaxScriptInputBytes)
	}
	var scriptCtx any
	if err := json.Unmarshal(ctxData, &scriptCtx); err != nil {
		return nil, err
	}
	rt := goja.New()
	rt.SetRandSource(deterministicScriptRandSource())
	rt.SetTimeSource(deterministicScriptTimeSource)
	if err := installSDK(rt); err != nil {
		return nil, err
	}
	if err := runScriptStep(rt, func() error {
		_, err := rt.RunString(source)
		return err
	}); err != nil {
		return nil, err
	}
	fn, ok := goja.AssertFunction(rt.Get(entrypoint))
	if !ok {
		return nil, fmt.Errorf("script entrypoint %q is not a function", entrypoint)
	}
	var value goja.Value
	if err := runScriptStep(rt, func() error {
		var err error
		value, err = fn(goja.Undefined(), rt.ToValue(scriptCtx))
		return err
	}); err != nil {
		return nil, err
	}
	rawOutput := value.Export()
	out, err := json.Marshal(rawOutput)
	if err != nil {
		return nil, err
	}
	if len(out) > MaxScriptOutputBytes {
		return nil, fmt.Errorf("policy script output exceeds %d bytes", MaxScriptOutputBytes)
	}
	return out, nil
}

func deterministicScriptTimeSource() time.Time {
	return time.Unix(0, 0).UTC()
}

func deterministicScriptRandSource() goja.RandSource {
	var state uint64 = 0x686964656f7574
	return func() float64 {
		state += 0x9e3779b97f4a7c15
		z := state
		z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
		z = (z ^ (z >> 27)) * 0x94d049bb133111eb
		z ^= z >> 31
		return float64(z>>11) / (1 << 53)
	}
}

func runScriptStep(rt *goja.Runtime, fn func() error) error {
	var timedOut atomic.Bool
	timer := time.AfterFunc(ScriptTimeout, func() {
		timedOut.Store(true)
		rt.Interrupt(ErrScriptTimeout)
	})
	err := fn()
	if !timer.Stop() {
		rt.ClearInterrupt()
	}
	if timedOut.Load() || errors.Is(err, ErrScriptTimeout) {
		return ErrScriptTimeout
	}
	return err
}

func installSDK(rt *goja.Runtime) error {
	decision := rt.NewObject()
	if err := decision.Set("allow", func(call goja.FunctionCall) goja.Value {
		return decisionValue(rt, call, Allow, "")
	}); err != nil {
		return err
	}
	if err := decision.Set("deny", func(call goja.FunctionCall) goja.Value {
		return decisionValue(rt, call, Deny, DenyRoute)
	}); err != nil {
		return err
	}
	if err := decision.Set("ask", func(call goja.FunctionCall) goja.Value {
		return decisionValue(rt, call, Ask, "")
	}); err != nil {
		return err
	}
	if err := decision.Set("auditOnly", func(call goja.FunctionCall) goja.Value {
		return decisionValue(rt, call, AuditOnly, "")
	}); err != nil {
		return err
	}
	urlSDK := map[string]func(string) map[string]any{
		"parse": func(raw string) map[string]any {
			u, err := url.Parse(raw)
			if err != nil {
				return map[string]any{"scheme": "", "error": err.Error()}
			}
			return map[string]any{"scheme": u.Scheme, "host": u.Host, "path": u.Path}
		},
	}
	hideout := rt.NewObject()
	if err := hideout.Set("decision", decision); err != nil {
		return err
	}
	if err := hideout.Set("url", urlSDK); err != nil {
		return err
	}
	return rt.Set("hideout", hideout)
}

func decisionValue(rt *goja.Runtime, call goja.FunctionCall, decision Decision, defaultRoute Route) goja.Value {
	proposal := map[string]any{}
	if len(call.Arguments) > 0 && !goja.IsUndefined(call.Argument(0)) && !goja.IsNull(call.Argument(0)) {
		if exported, ok := call.Argument(0).Export().(map[string]any); ok {
			proposal = exported
		} else {
			_ = rt.ExportTo(call.Argument(0), &proposal)
		}
	}
	proposal["decision"] = string(decision)
	if defaultRoute != "" {
		if _, ok := proposal["route"]; !ok {
			proposal["route"] = string(defaultRoute)
		}
	}
	obj := rt.NewObject()
	for k, v := range proposal {
		_ = obj.Set(k, v)
	}
	return obj
}

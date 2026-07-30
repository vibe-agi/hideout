package profilechanges

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/vibe-agi/hideout/internal/profile"
	"github.com/vibe-agi/hideout/internal/secrets"
)

type networkPostureValue struct {
	Mode string `json:"mode"`
}

type networkProxyRefValue struct {
	Ref string `json:"ref"`
}

type networkDNSValue struct {
	Mode     string `json:"mode"`
	ServerIP string `json:"serverIp,omitempty"`
}

func normalizeNetworkPosture(
	raw json.RawMessage,
) (networkPostureValue, error) {
	var value networkPostureValue
	if err := decodeStrict(raw, &value); err != nil {
		return networkPostureValue{}, err
	}
	switch value.Mode {
	case "direct", "proxy":
		return value, nil
	default:
		return networkPostureValue{}, errors.New(
			"mode must be direct or proxy",
		)
	}
}

func normalizeNetworkProxyRef(
	raw json.RawMessage,
) (networkProxyRefValue, error) {
	var value networkProxyRefValue
	if err := decodeStrict(raw, &value); err != nil {
		return networkProxyRefValue{}, err
	}
	if strings.TrimSpace(value.Ref) != value.Ref {
		return networkProxyRefValue{}, errors.New(
			"proxy secret ref must not contain surrounding whitespace",
		)
	}
	if err := secrets.ValidateRef(value.Ref); err != nil {
		return networkProxyRefValue{}, err
	}
	return value, nil
}

func normalizeNetworkDNS(raw json.RawMessage) (networkDNSValue, error) {
	var value networkDNSValue
	if err := decodeStrict(raw, &value); err != nil {
		return networkDNSValue{}, err
	}
	switch value.Mode {
	case "system":
		if value.ServerIP != "" {
			return networkDNSValue{}, errors.New(
				"system DNS cannot include serverIp",
			)
		}
	case "ip", "doh":
		if net.ParseIP(value.ServerIP) == nil ||
			strings.TrimSpace(value.ServerIP) != value.ServerIP {
			return networkDNSValue{}, errors.New(
				"DNS serverIp must be an IP literal",
			)
		}
	default:
		return networkDNSValue{}, errors.New(
			"DNS mode must be system, ip, or doh",
		)
	}
	return value, nil
}

func applyNetworkPosture(
	desired *profile.Profile,
	raw json.RawMessage,
) ([]Diff, error) {
	value, err := normalizeNetworkPosture(raw)
	if err != nil {
		return nil, err
	}
	before := desired.Network.Mode
	desired.Network.ProxyEnvVisible = false
	switch value.Mode {
	case "direct":
		desired.Network.Mode = profile.NetworkModeDirect
	case "proxy":
		desired.Network.Mode = profile.NetworkModeTun2Socks
	}
	return []Diff{{
		Kind:   KindNetworkPosture,
		Field:  "network.mode",
		Before: before,
		After:  desired.Network.Mode,
		Scope:  "environment",
	}}, nil
}

func applyNetworkProxyRef(
	desired *profile.Profile,
	raw json.RawMessage,
) ([]Diff, error) {
	value, err := normalizeNetworkProxyRef(raw)
	if err != nil {
		return nil, err
	}
	before := desired.Network.ProxySecretRef
	desired.Network.ProxySecretRef = value.Ref
	return []Diff{{
		Kind:   KindNetworkProxyRef,
		Field:  "network.proxySecretRef",
		Before: state(before != "", before, "not configured"),
		After:  value.Ref,
		Scope:  "active-connections",
	}}, nil
}

func applyNetworkDNS(
	desired *profile.Profile,
	raw json.RawMessage,
) ([]Diff, error) {
	value, err := normalizeNetworkDNS(raw)
	if err != nil {
		return nil, err
	}
	before := "system"
	if desired.Network.MediatedResolver != "" {
		before = "doh via " + desired.Network.MediatedResolver
	}
	after := "system"
	if value.Mode == "system" {
		desired.Network.MediatedResolver = ""
	} else {
		desired.Network.MediatedResolver = value.ServerIP
		after = value.Mode + " via " + value.ServerIP
	}
	return []Diff{{
		Kind: KindNetworkDNS, Field: "network.dns",
		Before: before, After: after, Scope: "environment",
	}}, nil
}

func validateCompleteNetwork(desired profile.Profile) error {
	if desired.Network.Mode != profile.NetworkModeTun2Socks {
		return nil
	}
	if desired.Network.ProxySecretRef == "" {
		return errors.New("proxy posture requires a proxy secret ref")
	}
	if net.ParseIP(desired.Network.MediatedResolver) == nil {
		return fmt.Errorf(
			"proxy posture requires a mediated DNS resolver IP",
		)
	}
	return nil
}

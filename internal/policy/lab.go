package policy

import (
	"errors"
	"fmt"
	"strings"
)

const (
	LabProbe Route = "lab-probe"

	ActionPortbridgeProbe     = "portbridge.probe"
	ActionBrowserControlProbe = "browser.control.probe"
	ActionPreviewOpenProbe    = "preview.open.probe"
)

type LabProposal struct {
	Subject   string         `json:"subject"`
	Decision  Decision       `json:"decision"`
	Route     Route          `json:"route"`
	Action    string         `json:"action"`
	Resources []string       `json:"resources"`
	Reason    string         `json:"reason"`
	Audit     map[string]any `json:"audit,omitempty"`
}

func ValidateLabProposal(p LabProposal) (LabProposal, error) {
	if strings.TrimSpace(p.Subject) == "" {
		return p, errors.New("lab proposal subject is required")
	}
	if strings.TrimSpace(p.Action) == "" {
		return p, errors.New("lab proposal action is required")
	}
	if strings.TrimSpace(p.Reason) == "" {
		return p, errors.New("lab proposal reason is required")
	}
	switch p.Decision {
	case Allow:
		if p.Route != LabProbe {
			return p, errors.New("allow lab proposal must use lab-probe route")
		}
	case Deny:
		if p.Route != DenyRoute {
			return p, errors.New("deny lab proposal must use deny route")
		}
	default:
		return p, fmt.Errorf("unsupported lab decision %q", p.Decision)
	}
	if err := validateLabSubjectAction(p.Subject, p.Action); err != nil {
		return p, err
	}
	if len(p.Resources) == 0 {
		return p, errors.New("lab proposal resources are required")
	}
	for _, resource := range p.Resources {
		if strings.TrimSpace(resource) == "" {
			return p, errors.New("lab proposal resources must not be empty")
		}
		if resourceContainsSecretValue(resource) {
			return p, errors.New("lab proposal resources must not contain secret values")
		}
	}
	return p, nil
}

func validateLabSubjectAction(subject, action string) error {
	switch action {
	case ActionPortbridgeProbe:
		if subject == "lab:portbridge" {
			return nil
		}
	case ActionBrowserControlProbe:
		if subject == "lab:browser" {
			return nil
		}
	case ActionPreviewOpenProbe:
		if subject == "lab:preview" {
			return nil
		}
	default:
		return fmt.Errorf("unsupported lab action %q", action)
	}
	return fmt.Errorf("lab action %q is not allowed for subject %q", action, subject)
}

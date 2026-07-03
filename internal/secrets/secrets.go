package secrets

import (
	"fmt"
	"strings"
)

const maxRefLength = 64

func ValidateRef(ref string) error {
	if ref == "" {
		return fmt.Errorf("secret ref is required")
	}
	if len(ref) > maxRefLength {
		return fmt.Errorf("secret ref %q exceeds %d characters", ref, maxRefLength)
	}
	if ref[0] == '-' || ref[len(ref)-1] == '-' {
		return fmt.Errorf("invalid secret ref %q", ref)
	}
	for _, r := range ref {
		if r >= 'a' && r <= 'z' {
			continue
		}
		if r >= '0' && r <= '9' {
			continue
		}
		if r == '-' {
			continue
		}
		return fmt.Errorf("invalid secret ref %q", ref)
	}
	return nil
}

func EnvName(ref string) (string, error) {
	if err := ValidateRef(ref); err != nil {
		return "", err
	}
	return "HIDEOUT_SECRET_" + strings.ToUpper(strings.ReplaceAll(ref, "-", "_")), nil
}

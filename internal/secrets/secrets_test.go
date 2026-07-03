package secrets

import "testing"

func TestEnvNameMapsValidRef(t *testing.T) {
	got, err := EnvName("default-proxy")
	if err != nil {
		t.Fatalf("EnvName: %v", err)
	}
	if got != "HIDEOUT_SECRET_DEFAULT_PROXY" {
		t.Fatalf("EnvName=%s", got)
	}
}

func TestValidateRefRejectsAmbiguousOrInvalidRefs(t *testing.T) {
	for _, ref := range []string{
		"",
		"Default-Proxy",
		"default_proxy",
		"default.proxy",
		"-default",
		"default-",
		"default proxy",
	} {
		t.Run(ref, func(t *testing.T) {
			if err := ValidateRef(ref); err == nil {
				t.Fatalf("expected invalid ref %q to fail", ref)
			}
		})
	}
}

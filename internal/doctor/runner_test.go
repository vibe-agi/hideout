package doctor

import "testing"

func TestValidateRequestFeatures(t *testing.T) {
	if err := ValidateRequest(Request{Level: LevelLight, Features: []string{"dns", "hostfs"}}); err != nil {
		t.Fatalf("ValidateRequest: %v", err)
	}
	if err := ValidateRequest(Request{Level: "medium"}); err == nil {
		t.Fatal("expected unsupported level")
	}
	if err := ValidateRequest(Request{Features: []string{"browser"}}); err == nil {
		t.Fatal("expected unsupported feature")
	}
}

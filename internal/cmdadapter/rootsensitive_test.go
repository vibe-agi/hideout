package cmdadapter

import "testing"

func TestRootSensitiveCategories(t *testing.T) {
	cases := map[string]string{
		"sudo":       "escalation",
		"apt":        "package-manager",
		"iptables":   "network-mutation",
		"resolvectl": "resolver",
		"systemctl":  "service-manager",
		"mount":      "mount",
		"sysctl":     "system-management",
	}
	for command, want := range cases {
		if got := RootSensitiveCategory(command); got != want {
			t.Fatalf("%s category = %q, want %q", command, got, want)
		}
	}
}

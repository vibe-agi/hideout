package privilege

import "strings"

func TargetFromChecks(user, home string, checks []CheckResult) TargetIdentity {
	target := TargetIdentity{User: user, Home: home}
	for _, check := range checks {
		switch check.Name {
		case CheckTargetUID:
			if check.Status == CheckPass {
				if uid, ok := parseObservedInt(check.Observed); ok {
					target.UID = Int(uid)
				}
			}
		case CheckTargetSudoN:
			target.SudoN = check
			if check.Status == CheckPass {
				target.CanPasswordlessSudo = true
				target.PasswordlessSudoKnown = true
			} else if check.Status == CheckFail {
				target.PasswordlessSudoKnown = true
			}
		case CheckTargetAbsoluteSudo:
			target.AbsoluteSudoN = check
			if check.Status == CheckPass {
				target.CanPasswordlessSudo = true
				target.PasswordlessSudoKnown = true
			} else if check.Status == CheckFail && target.SudoN.Status != "" {
				target.PasswordlessSudoKnown = true
			}
		}
	}
	return target
}

func parseObservedInt(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}

package lima

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/vibe-agi/hideout/internal/backend"
	"github.com/vibe-agi/hideout/internal/privilege"
)

func (b Backend) probeGuestPrivilege(ctx context.Context, session *backend.Session, runner CommandRunner, hostEnv []string, targetEnv []string) privilege.Status {
	checks := []privilege.CheckResult{
		b.runPrivilegeCheck(ctx, session, runner, hostEnv, targetEnv, privilege.CheckTargetUID, []string{"id", "-u"}, true),
		b.runPrivilegeCheck(ctx, session, runner, hostEnv, targetEnv, privilege.CheckTargetSudoN, []string{"sudo", "-n", "true"}, false),
		b.runPrivilegeCheck(ctx, session, runner, hostEnv, targetEnv, privilege.CheckTargetAbsoluteSudo, []string{"sh", "-c", "if [ -x /usr/bin/sudo ]; then /usr/bin/sudo -n true; else exit 1; fi"}, false),
	}
	target := privilege.TargetFromChecks("", session.GuestHome, checks)
	setup := b.setupIdentity(ctx, session)
	status, err := privilege.Classify(privilege.ClassificationInput{
		Profile:                 "",
		Backend:                 session.Backend,
		EnvironmentID:           session.EnvironmentID,
		Target:                  target,
		Setup:                   setup,
		Checks:                  checks,
		PrivilegedSetupRequired: session.PrivilegedSetupRequired,
	})
	if err != nil {
		status.Status = privilege.StatusUnknown
		status.Reason = err.Error()
		status.Guidance = "rerun privilege checks or recreate the environment"
	}
	return status
}

func (b Backend) runPrivilegeCheck(ctx context.Context, session *backend.Session, runner CommandRunner, hostEnv, targetEnv []string, name privilege.CheckName, command []string, parseUID bool) privilege.CheckResult {
	var stdout, stderr bytes.Buffer
	// Privilege checks probe target identity (uid, sudo reachability) and are
	// independent of the workspace. They run during activation, before any
	// per-session workspace view exists in shared machines, so the workspace
	// path must not be the working directory (it would fail every check with
	// "cd: no such file or directory"). Root mirrors runtime observation.
	workdir := "/"
	err := runner.Run(ctx, b.limactl(), ShellArgs(session.InstanceName, workdir, targetEnv, command), hostEnv, nil, &stdout, &stderr)
	observed := strings.TrimSpace(stdout.String())
	errText := strings.TrimSpace(stderr.String())
	if parseUID {
		if err != nil {
			return privilege.CheckResult{Name: name, Status: privilege.CheckError, Observed: observed, Error: boundedCheckError(err, errText)}
		}
		uid, parseErr := strconv.Atoi(strings.TrimSpace(observed))
		if parseErr != nil || uid < 0 {
			return privilege.CheckResult{Name: name, Status: privilege.CheckError, Observed: observed, Error: "ambiguous uid output"}
		}
		return privilege.CheckPassed(name, observed)
	}
	if err != nil {
		return privilege.CheckFailed(name, firstNonEmpty(errText, err.Error()))
	}
	return privilege.CheckPassed(name, observed)
}

func boundedCheckError(err error, stderr string) string {
	msg := firstNonEmpty(stderr, errString(err))
	if len(msg) > 240 {
		msg = msg[:240] + "..."
	}
	return msg
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	if err == io.EOF {
		return "EOF"
	}
	return fmt.Sprint(err)
}

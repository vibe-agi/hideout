package manager

type RunExplainOptions struct {
	Environment RunEnvironmentOptions
}

type RunExplanation struct {
	Plan    RunPlan
	Session RunSession
}

func (c Core) ExplainRun(plan RunPlan, opts RunExplainOptions, render func(RunExplanation) error) (retErr error) {
	runEnv, err := c.SelectRunEnvironment(plan, RunEnvironmentOptions{
		EnvName:        opts.Environment.EnvName,
		RemoveAfterRun: opts.Environment.RemoveAfterRun,
		Create:         false,
	})
	if err != nil {
		return err
	}
	runSession, err := c.BeginRunSession(plan, runEnv, RunSessionOptions{ExplainOnly: true})
	if err != nil {
		return err
	}
	defer func() {
		_, closeErr := c.CloseRunSession(runSession)
		if closeErr != nil && retErr == nil {
			retErr = closeErr
		}
	}()
	if render == nil {
		return nil
	}
	return render(RunExplanation{
		Plan:    plan,
		Session: runSession,
	})
}

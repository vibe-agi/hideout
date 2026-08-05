package app

import (
	"context"
	"errors"
	"io"
	"os"
	"os/signal"
	"syscall"

	tea "charm.land/bubbletea/v2"
	"github.com/vibe-agi/hideout/internal/daemon"
	"github.com/vibe-agi/hideout/internal/liveconsole"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
	tuimodel "github.com/vibe-agi/hideout/internal/tui"
	tuimodal "github.com/vibe-agi/hideout/internal/tui/modal"
	tuirender "github.com/vibe-agi/hideout/internal/tui/render"
)

func (a app) runTUICommand(options tuiOptions) error {
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	query := manager.OperatorSnapshotQuery{
		Profile: options.profileName, Session: options.sessionID,
		ActivityLimit: manager.DefaultOperatorActivityLimit,
	}
	snapshot, state, daemonSeed, err := loadOperatorTUIState(ctx, store, query)
	if err != nil {
		return err
	}
	state.ProfileScope = options.profileName
	if options.once {
		_, err := io.WriteString(a.stdout, tuirender.Overview(tuirender.OverviewInput{
			State: state, Now: snapshot.GeneratedAt,
		}, tuirender.Options{
			Width: 96, Height: 26, Unicode: false, NoColor: true,
		}))
		return err
	}

	var events <-chan liveconsole.Event
	var activityProvider manager.ActivityProvider
	var configProvider tuimodal.Provider
	var lifecycleProvider tuimodal.LifecycleProvider
	var migrationProvider tuimodal.MigrationProvider
	snapshotProvider := func(refreshContext context.Context) (liveconsole.State, error) {
		_, refreshedState, _, refreshErr := loadOperatorTUIState(
			refreshContext, store, query,
		)
		if refreshErr != nil {
			return liveconsole.State{}, refreshErr
		}
		refreshedState.ProfileScope = options.profileName
		return refreshedState, nil
	}
	if daemonSeed {
		activityProvider = daemon.NewActivityClient(store.Root)
		configurationClient := newTUIConfigurationClient(store.Root)
		configProvider = configurationClient
		lifecycleProvider = configurationClient
		migrationProvider = configurationClient
		events, err = daemon.SubscribeEvents(ctx, store.Root, snapshot.Sequence)
		if err != nil {
			state.ReadOnly = true
			state.RequiresReseed = true
			state.StreamHealth = liveconsole.StreamHealth{
				State:  liveconsole.HealthDisconnected,
				Reason: "authenticated daemon event stream is unavailable",
			}
		}
	}
	model := tuimodel.NewModel(tuimodel.ModelOptions{
		State: state, SessionID: options.sessionID,
		Events: events, Unicode: true, Context: ctx,
		ActivityProvider:  activityProvider,
		ConfigProvider:    configProvider,
		LifecycleProvider: lifecycleProvider,
		MigrationProvider: migrationProvider,
		SnapshotProvider:  snapshotProvider,
		FallbackInterval:  options.interval,
		HelpCatalog:       defaultOperatorHelpCatalog(),
	})
	_, err = runTUIProgram(ctx, a.stdin, a.stdout, model)
	if errors.Is(err, tea.ErrProgramPanic) {
		return err
	}
	if err != nil && ctx.Err() != nil && errors.Is(err, tea.ErrProgramKilled) {
		return nil
	}
	return err
}

func loadOperatorTUIState(
	ctx context.Context,
	store profile.Store,
	query manager.OperatorSnapshotQuery,
) (manager.OperatorSnapshot, liveconsole.State, bool, error) {
	snapshot, daemonErr := daemon.FetchOperatorSnapshot(ctx, store.Root, query)
	daemonSeed := daemonErr == nil
	if daemonErr != nil {
		snapshot, daemonErr = (manager.OperatorSnapshotService{
			Core: manager.New(store),
		}).Build(ctx, query)
	}
	if daemonErr != nil {
		return manager.OperatorSnapshot{}, liveconsole.State{}, false, daemonErr
	}
	state, err := liveconsole.NewStateFromOperatorSnapshot(snapshot)
	if err != nil {
		return manager.OperatorSnapshot{}, liveconsole.State{}, false, err
	}
	return snapshot, state, daemonSeed, nil
}

func runTUIProgram(
	ctx context.Context,
	input io.Reader,
	output io.Writer,
	model tea.Model,
) (tea.Model, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	program := tea.NewProgram(
		model,
		tea.WithContext(ctx),
		tea.WithInput(input),
		tea.WithOutput(output),
	)
	return program.Run()
}

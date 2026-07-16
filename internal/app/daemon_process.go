package app

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/vibe-agi/hideout/internal/daemon"
	"github.com/vibe-agi/hideout/internal/profile"
)

const daemonShutdownTimeout = 10 * time.Second

// daemonServe is the hidden role entered by the installed hideout binary.
// It owns Manager and backend authority; normal CLI processes only connect to
// its authenticated local transports.
func (a app) daemonServe(args []string) error {
	if len(args) != 0 {
		return errors.New("internal daemon role does not accept arguments")
	}
	store, err := profile.DefaultStore()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(store.Root, 0o700); err != nil {
		return err
	}
	d, err := daemon.Start(a.daemonOptions(store, 0))
	if err != nil {
		if daemon.IsAlreadyRunning(err) {
			return nil
		}
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), daemonShutdownTimeout)
		defer cancel()
		return d.Stop(shutdownCtx)
	case <-d.Done():
		return nil
	}
}

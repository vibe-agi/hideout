package render

import (
	"strings"

	"github.com/vibe-agi/hideout/internal/liveconsole"
)

func healthLabel(health liveconsole.StreamHealth) string {
	switch health.State {
	case liveconsole.HealthLive:
		return "LIVE"
	case liveconsole.HealthIdleLive:
		return "IDLE LIVE"
	case liveconsole.HealthStale:
		return "STALE"
	case liveconsole.HealthDisconnected:
		return "DISCONNECTED"
	case liveconsole.HealthCredentialExpired:
		return "CREDENTIAL EXPIRED"
	case liveconsole.HealthSchemaMismatch:
		return "SCHEMA MISMATCH"
	case liveconsole.HealthDaemonless:
		return "DAEMONLESS"
	case liveconsole.HealthSeeding:
		return "SEEDING"
	default:
		if health.State == "" {
			return "DISCONNECTED"
		}
		return strings.ToUpper(sanitizeInline(health.State))
	}
}

func healthReadOnly(state liveconsole.State) bool {
	return !state.CanMutate()
}

package webui

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/vibe-agi/hideout/internal/daemon"
	"github.com/vibe-agi/hideout/internal/decision"
	"github.com/vibe-agi/hideout/internal/manager"
	"github.com/vibe-agi/hideout/internal/profile"
)

const noticeID = "ui-e2e-notice"

type Fixture struct {
	StoreRoot string `json:"storeRoot"`
	UIURL     string `json:"uiURL"`
	BaseURL   string `json:"baseURL"`
	Token     string `json:"token"`
	NoticeID  string `json:"noticeId"`

	daemon *daemon.Daemon
}

func StartFixture() (Fixture, error) {
	root, err := os.MkdirTemp("/tmp", "hdui")
	if err != nil {
		return Fixture{}, err
	}
	if err := os.Chmod(root, 0o700); err != nil {
		_ = os.RemoveAll(root)
		return Fixture{}, err
	}
	store := profile.Store{Root: root}
	if err := store.Save(profile.Default("default")); err != nil {
		_ = os.RemoveAll(root)
		return Fixture{}, err
	}
	core := manager.New(store)
	if _, err := core.CreateNotice(decision.Notice{
		ID:       noticeID,
		Kind:     decision.KindPrivilegeStatus,
		Severity: decision.NoticeSeverityWarning,
		Status:   "degraded",
		Source:   decision.Source{Profile: "default", Backend: "native", Surface: "ui-e2e"},
		Payload:  map[string]any{"reason": "ui-e2e-visible-notice"},
		Preview:  decision.Preview{Summary: "UI E2E notice requires acknowledgement"},
		AuditRef: "audit:notice:" + noticeID,
	}); err != nil {
		_ = os.RemoveAll(root)
		return Fixture{}, err
	}
	d, err := daemon.Start(daemon.Options{Store: store, TTL: 5 * time.Minute})
	if err != nil {
		_ = os.RemoveAll(root)
		return Fixture{}, err
	}
	base := d.UIURL()
	if idx := indexFragment(base); idx >= 0 {
		base = base[:idx]
	}
	base = strings.TrimRight(base, "/")
	return Fixture{
		StoreRoot: root,
		UIURL:     d.UIURL(),
		BaseURL:   base,
		Token:     d.Token(),
		NoticeID:  noticeID,
		daemon:    d,
	}, nil
}

func (f Fixture) Close() {
	if f.daemon != nil {
		_ = f.daemon.Stop(context.Background())
	}
	if f.StoreRoot != "" {
		_ = os.RemoveAll(f.StoreRoot)
	}
}

func (f Fixture) StopDaemon(ctx context.Context) error {
	if f.daemon == nil {
		return nil
	}
	return f.daemon.Stop(ctx)
}

func indexFragment(s string) int {
	for i := range s {
		if s[i] == '#' {
			return i
		}
	}
	return -1
}

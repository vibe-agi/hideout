package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnglishAndChineseUserGuidesCoverSupportedJourney(t *testing.T) {
	tests := []struct {
		path string
		want []string
	}{
		{
			path: filepath.Join("..", "..", "docs", "user-guide.md"),
			want: []string{
				"Start here",
				"Proxy secrets",
				"Desired, effective, and pending",
				"Update and uninstall",
				"Troubleshooting",
				"No daemon restart",
				"Observation and retention",
				"hideout activity coverage",
			},
		},
		{
			path: filepath.Join(
				"..", "..", "docs", "user-guide.zh-CN.md",
			),
			want: []string{
				"从这里开始",
				"代理密钥",
				"期望状态、生效状态与待处理状态",
				"升级与卸载",
				"故障排查",
				"不需要停止 daemon",
				"观测与保留",
				"hideout activity coverage",
			},
		},
	}
	catalog := defaultCommandCatalog()
	for _, test := range tests {
		t.Run(filepath.Base(test.path), func(t *testing.T) {
			data, err := os.ReadFile(test.path)
			if err != nil {
				t.Fatalf("read user guide: %v", err)
			}
			text := string(data)
			for _, want := range test.want {
				if !strings.Contains(text, want) {
					t.Errorf("guide missing %q", want)
				}
			}
			for lineNumber, line := range strings.Split(text, "\n") {
				line = strings.TrimSpace(line)
				if !strings.HasPrefix(line, "hideout ") {
					continue
				}
				fields := strings.Fields(line)
				if len(fields) < 2 {
					t.Errorf("line %d has incomplete command: %q", lineNumber+1, line)
					continue
				}
				entry, ok := catalog.lookup(fields[1])
				if !ok || entry.spec.Hidden {
					t.Errorf(
						"line %d uses a command absent from the visible catalog: %q",
						lineNumber+1,
						line,
					)
				}
			}
		})
	}
}

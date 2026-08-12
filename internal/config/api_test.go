package config

import (
	"strings"
	"testing"
)

type mapEnv map[string]string

func (e mapEnv) Getenv(key string) string { return e[key] }

func TestLoadAPI_RequiresWatchNamespaces(t *testing.T) {
	_, err := LoadAPI(mapEnv{
		"CLUSTER_ID": "local-test",
		"AUTH_MODE":  "disabled",
	})
	if err == nil || !strings.Contains(err.Error(), "WATCH_NAMESPACES") {
		t.Fatalf("缺少 WATCH_NAMESPACES 应失败，得到 %v", err)
	}
}

func TestLoadAPI_LoadsWatchNamespaces(t *testing.T) {
	cfg, err := LoadAPI(mapEnv{
		"CLUSTER_ID":       "local-test",
		"AUTH_MODE":        "disabled",
		"WATCH_NAMESPACES": "fault-lab, team-a",
	})
	if err != nil {
		t.Fatalf("LoadAPI: %v", err)
	}
	if got, want := strings.Join(cfg.WatchNamespaces, ","), "fault-lab,team-a"; got != want {
		t.Errorf("WatchNamespaces = %q, want %q", got, want)
	}
}

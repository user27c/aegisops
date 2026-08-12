package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

const configModeCrashLoop = "crashloop"

// startConfigModeWatcher observes the ConfigMap volume mounted into FaultLab.
// The watcher deliberately exits the process when the mounted mode requests a
// crash loop. Kubernetes then records the real container failure, while the
// only recovery path in the E2E is to restore the ConfigMap data.
//
// A volume-backed ConfigMap is used instead of an environment variable because
// kubelet can refresh the mounted file without requiring a Deployment patch.
func startConfigModeWatcher(ctx context.Context, path string, exit func(int)) {
	if path == "" || exit == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				mode, err := readConfigMode(path)
				if err != nil {
					// The volume may be between atomic symlink revisions. Keep
					// serving until a complete mode value is available.
					continue
				}
				if mode == configModeCrashLoop {
					exit(1)
					return
				}
			}
		}
	}()
}

func readConfigMode(path string) (string, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- path is an explicit fixture mount.
	if err != nil {
		return "", fmt.Errorf("read fault-lab config: %w", err)
	}
	return strings.TrimSpace(string(raw)), nil
}

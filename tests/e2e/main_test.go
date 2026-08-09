package e2e

import (
	"fmt"
	"os"
	"testing"
)

// TestMain 是所有 E2E 测试的入口保护:无 AEGISOPS_E2E=1 直接跳过;有则加载环境,失败即退出。
func TestMain(m *testing.M) {
	if os.Getenv("AEGISOPS_E2E") != "1" {
		fmt.Fprintln(os.Stderr, "跳过 E2E: 设置 AEGISOPS_E2E=1 且先运行 scripts/e2e-up.sh(或 make test-e2e)")
		os.Exit(0)
	}
	if os.Getenv("AEGISOPS_E2E_CONTEXT") != requiredE2EContext {
		fmt.Fprintf(os.Stderr, "拒绝 E2E: AEGISOPS_E2E_CONTEXT 必须为 %s\n", requiredE2EContext)
		os.Exit(1)
	}
	if os.Getenv("AEGISOPS_E2E_KUBECONFIG") == "" {
		fmt.Fprintln(os.Stderr, "拒绝 E2E: AEGISOPS_E2E_KUBECONFIG 未设置")
		os.Exit(1)
	}
	var err error
	env, err = LoadEnvironment()
	if err != nil {
		fmt.Fprintln(os.Stderr, "E2E 环境加载失败:", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

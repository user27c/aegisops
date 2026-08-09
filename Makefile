# AegisOps 工程 Makefile。默认 target 是 help，绝不默认部署或删除。

##@ 通用

.PHONY: help
help: ## 显示所有 target
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ 生成

.PHONY: generate
generate: controller-gen ## 生成 deepcopy 等代码
	"$(CONTROLLER_GEN)" object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: manifests
manifests: controller-gen ## 生成 CRD / RBAC manifests
	"$(CONTROLLER_GEN)" rbac:roleName=manager-role crd:allowDangerousTypes=true webhook paths="./..." output:crd:artifacts:config=config/crd/bases

.PHONY: verify-generated
verify-generated: generate manifests ## 校验生成文件无漂移
	@git diff --exit-code -- api/ config/ deploy/helm/aegisops/crds/ || { echo "生成文件有漂移，请先运行 make generate manifests"; exit 1; }

##@ 格式与静态检查

.PHONY: fmt
fmt: ## go fmt
	go fmt ./...

.PHONY: vet
vet: ## go vet
	go vet ./...

.PHONY: lint-go
lint-go: golangci-lint ## golangci-lint
	"$(GOLANGCI_LINT)" run

.PHONY: lint-python
lint-python: ## ruff + mypy（诊断服务）
	cd services/diagnosis && uv run ruff check app tests && uv run mypy app

.PHONY: lint-web
lint-web: ## oxlint + typecheck（Web 控制台）
	cd web && pnpm lint && pnpm typecheck

.PHONY: lint-helm
lint-helm: ## helm lint
	helm lint deploy/helm/aegisops

.PHONY: lint
lint: lint-go lint-python lint-web lint-helm ## 全部静态检查

##@ 测试

.PHONY: test-go
test-go: manifests generate fmt vet ## Go 单元测试（含 race）
	go test $$(go list ./... | grep -v /test/) -race -coverprofile cover.out

.PHONY: test-python
test-python: ## Python 单元测试
	cd services/diagnosis && uv run pytest

.PHONY: test-web
test-web: ## Web 单元测试
	cd web && pnpm test

.PHONY: test-envtest
test-envtest: manifests generate fmt vet setup-envtest ## 真实 API server CRD/envtest 集成测试
	KUBEBUILDER_ASSETS="$(shell "$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path)" go test ./tests/integration/controller/... -count=1

.PHONY: test-rules
test-rules: ## 渲染并 promtool 校验告警规则(需要 aegisops-prom 容器或本地 promtool)
	bash scripts/render-prometheus-rules.sh

.PHONY: test-integration
test-integration: ## 真实 PostgreSQL Diagnosis API + Alertmanager/MailHog 集成测试
	cd services/diagnosis && TESTCONTAINERS_RYUK_DISABLED=true uv run pytest tests/integration
	$(MAKE) test-alerting

.PHONY: test-e2e
test-e2e: ## Kind E2E 闭环(Auto/审批/回滚/安全/邮件;先运行 scripts/e2e-up.sh)
	scripts/run-e2e.sh

.PHONY: test-alerting
test-alerting: ## 真实邮件通知链路（仅启动并清理 Alertmanager + MailHog）
	@bash -ec 'scripts/alerting-up.sh; trap "scripts/alerting-down.sh" EXIT; python3 tests/integration/alertmanager_email_test.py'

.PHONY: test-all
test-all: test-go test-python test-web test-envtest test-rules ## 全部测试

.PHONY: verify
verify: fmt lint test-go lint-python lint-web lint-helm manifests helm-lint ## 轻量验收：fmt + lint + 单元测试 + manifests

##@ 构建

# 三个 Go 二进制
BINARIES := operator alert-gateway incident-api

.PHONY: build
build: manifests generate fmt vet ## 构建三个 Go 二进制到 bin/
	@for b in $(BINARIES); do \
		echo "构建 bin/$$b ..."; \
		go build -trimpath -o bin/$$b ./cmd/$$b || exit 1; \
	done

.PHONY: build-images
build-images: ## 构建五个镜像（不 push）: make build-images REGISTRY=aegisops.local TAG=dev
	scripts/build-images.sh --registry $(REGISTRY) --tag $(TAG)

.PHONY: helm-lint
helm-lint: ## 校验 Helm Chart 与 values schema
	helm lint deploy/helm/aegisops

##@ Runbook

.PHONY: runbooks-validate
runbooks-validate: ## 校验 runbook frontmatter 与 JSON Schema
	uv run python scripts/validate-runbooks.py

.PHONY: runbooks-index
runbooks-index: ## 索引 runbook 到 pgvector（M3 后可用）
	cd services/diagnosis && uv run aegis-runbooks index --root ../../runbooks

##@ 评估

.PHONY: eval
eval: ## 运行评估实验（fake，输出到新的时间戳目录）
	cd services/diagnosis && uv run python ../../eval/run_campaign.py fake

.PHONY: eval-report
eval-report: ## 列出 fake 评估报告（历史记录不可改写）
	@find eval/runs -maxdepth 2 -path '*/fake-*/report.md' -print | sort | tail -n 1 | grep . || { echo "先运行 make eval" >&2; exit 1; }

##@ 开发环境

CONTEXT ?= kind-aegisops-dev
PROFILE ?= minimal
TAG ?= dev
REGISTRY ?= aegisops.local

.PHONY: dev-up
dev-up: ## 启动本地开发环境: make dev-up CONTEXT=kind-aegisops-dev PROFILE=full TAG=v0.2.0
	scripts/dev-up.sh --context $(CONTEXT) --profile $(PROFILE) --registry $(REGISTRY) --tag $(TAG)

.PHONY: dev-down
dev-down: ## 卸载本地开发环境: make dev-down CONTEXT=kind-aegisops-dev [PURGE_DATA=true]
	scripts/dev-down.sh --context $(CONTEXT) $(if $(PURGE_DATA),--purge-data) $(if $(DELETE_KIND),--delete-kind-cluster)

.PHONY: init-local-config
init-local-config: ## 初始化 .local/ 配置目录(随机 token,0600)
	scripts/init-local-config.sh

.PHONY: smoke
smoke: ## 开发环境冒烟检查: make smoke CONTEXT=kind-aegisops-dev
	scripts/smoke.sh --context $(CONTEXT)

.PHONY: alerting-up
alerting-up: ## 启动 Alertmanager+MailHog 测试环境
	scripts/alerting-up.sh

.PHONY: alerting-down
alerting-down: ## 停止 Alertmanager+MailHog 测试环境
	scripts/alerting-down.sh

.PHONY: eval-fake
eval-fake: ## 评估: fake LLM 基线
	cd services/diagnosis && uv run python ../../eval/run_campaign.py fake

.PHONY: eval-deepseek
eval-deepseek: ## 评估: 真实 DeepSeek（新时间戳目录；需要 DEEPSEEK_API_KEY，绝不回退 fake）
	cd services/diagnosis && uv run python ../../eval/run_campaign.py deepseek

.PHONY: verify-all
verify-all: ## 完整验收: verify + envtest + integration + E2E(耗时,需集群)
	$(MAKE) verify
	$(MAKE) test-envtest
	$(MAKE) test-integration
	$(MAKE) test-e2e

.PHONY: clean
clean: ## 仅清理本地可再生产物
	rm -rf bin dist coverage htmlcov web/dist web/test-results .pytest_cache .mypy_cache .ruff_cache

##@ 依赖工具

SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

CONTAINER_TOOL ?= docker
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p "$(LOCALBIN)"

KUBECTL ?= kubectl
KIND ?= kind
KUSTOMIZE ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest
GOLANGCI_LINT = $(LOCALBIN)/golangci-lint

KUSTOMIZE_VERSION ?= v5.8.1
CONTROLLER_TOOLS_VERSION ?= v0.20.1

ENVTEST_VERSION ?= $(shell v='$(call gomodver,sigs.k8s.io/controller-runtime)'; \
  [ -n "$$v" ] || { echo "Set ENVTEST_VERSION manually" >&2; exit 1; }; \
  printf '%s\n' "$$v" | sed -E 's/^v?([0-9]+)\.([0-9]+).*/release-\1.\2/')
ENVTEST_K8S_VERSION ?= $(shell v='$(call gomodver,k8s.io/api)'; \
  [ -n "$$v" ] || { echo "Set ENVTEST_K8S_VERSION manually" >&2; exit 1; }; \
  printf '%s\n' "$$v" | sed -E 's/^v?[0-9]+\.([0-9]+).*/1.\1/')
GOLANGCI_LINT_VERSION ?= v2.8.0

.PHONY: kustomize
kustomize: $(KUSTOMIZE)
$(KUSTOMIZE): $(LOCALBIN)
	$(call go-install-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v5,$(KUSTOMIZE_VERSION))

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN)
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: setup-envtest
setup-envtest: envtest
	@"$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path

.PHONY: envtest
envtest: $(ENVTEST)
$(ENVTEST): $(LOCALBIN)
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT)
$(GOLANGCI_LINT): $(LOCALBIN)
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/v2/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))

define go-install-tool
@[ -f "$(1)-$(3)" ] && [ "$$(readlink -- "$(1)" 2>/dev/null)" = "$(1)-$(3)" ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "下载 $${package}" ;\
rm -f "$(1)" ;\
GOBIN="$(LOCALBIN)" go install $${package} ;\
mv "$(LOCALBIN)/$$(basename "$(1)")" "$(1)-$(3)" ;\
} ;\
ln -sf "$$(realpath "$(1)-$(3)")" "$(1)"
endef

define gomodver
$(shell go list -m -f '{{if .Replace}}{{.Replace.Version}}{{else}}{{.Version}}{{end}}' $(1) 2>/dev/null)
endef

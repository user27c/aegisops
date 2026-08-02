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
test-envtest: manifests generate fmt vet setup-envtest ## envtest 集成测试
	KUBEBUILDER_ASSETS="$(shell "$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path)" go test ./internal/controller/... -coverprofile cover-controller.out

.PHONY: test-rules
test-rules: ## promtool 校验告警规则
	promtool test rules deploy/observability/tests/rules.test.yaml

.PHONY: test-integration
test-integration: ## 集成测试（envtest/PostgreSQL，M9.1+ 实现）
	@echo "test-integration 尚未实现，见 docs/NEXT-STEPS-IMPLEMENTATION-PLAN.md §12" >&2
	@exit 1

.PHONY: test-e2e
test-e2e: ## Kind E2E（M9.6 实现；需要 --context 保护的脚本）
	@echo "test-e2e 尚未实现，见 docs/NEXT-STEPS-IMPLEMENTATION-PLAN.md §11" >&2
	@exit 1

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
build-images: ## 构建五个镜像（不 push）
	scripts/build-images.sh --tag $(TAG)

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
eval: ## 运行评估实验（当前仅支持 fake 基线）
	cd services/diagnosis && uv run python ../../eval/run_campaign.py fake

.PHONY: eval-report
eval-report: ## 生成评估报告（由 run_campaign.py 输出 report.md）
	@test -f eval/report.md || { echo "先运行 make eval" >&2; exit 1; }
	@echo "报告已生成: eval/report.md (原始记录: eval/runs/raw.jsonl)"

##@ 开发环境

.PHONY: dev-up
dev-up: ## 启动本地开发环境（需 --context 显式参数）
	scripts/dev-up.sh --context $(CONTEXT)

.PHONY: dev-down
dev-down: ## 卸载本地开发环境
	scripts/dev-down.sh --context $(CONTEXT)

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

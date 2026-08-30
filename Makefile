.PHONY: dev backend frontend build test lint-arch migrate-down clean help

# 开发：终端1 起后端(:8080)，终端2 起前端(:5173，代理 /api)
dev: backend
	@echo "后端已启动: http://localhost:8080 （前端请另开终端运行 make frontend）"

backend:
	cd cmd/reqflow && go run .

frontend:
	cd web && pnpm dev

# 发布：构建前端 → 拷贝产物到 embed 目录 → 打进二进制 → 输出 bin/reqflow
build:
	cd web && pnpm install --frozen-lockfile || pnpm install
	cd web && pnpm build
	rm -rf cmd/reqflow/dist && cp -r web/dist cmd/reqflow/dist
	go build -trimpath -ldflags "-s -w" -tags embed -o bin/reqflow ./cmd/reqflow
	@ls -lh bin/reqflow

# 测试 + 类型检查 + 架构围栏 + 密钥护栏
test:
	go vet ./...
	go test ./...
	$(MAKE) lint-arch
	$(MAKE) check-secrets

# 架构围栏：依赖方向白名单校验（详见 scripts/arch-check.sh）
lint-arch:
	bash scripts/arch-check.sh

# 密钥护栏：扫描 git 追踪文件中的疑似泄漏（敏感字段非空值 / 带密码 DSN）
check-secrets:
	bash scripts/secret-check-test.sh
	bash scripts/secret-check.sh tracked

# 一次性初始化（clone 后执行）：启用 git 钩子目录
setup:
	git config core.hooksPath .githooks
	@echo "✓ pre-commit 密钥护栏已启用"

clean:
	rm -rf bin web/dist

help:
	@echo "setup | dev | build | test | lint-arch | check-secrets | clean"

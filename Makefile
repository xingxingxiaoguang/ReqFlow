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

# 测试 + 类型检查 + 架构围栏
test:
	go vet ./...
	go test ./...
	$(MAKE) lint-arch

# 架构围栏：依赖方向白名单校验（详见 scripts/arch-check.sh）
lint-arch:
	bash scripts/arch-check.sh

clean:
	rm -rf bin web/dist

help:
	@echo "dev | build | test | lint-arch | clean"

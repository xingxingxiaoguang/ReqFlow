//go:build integration

// 集成测试公共环境（默认不跑，保持 go test 纯净）：
//
//	go test -tags integration ./internal/infra/repository/ -run TestIntegration -v
//
// 依赖本机 docker PG（docker-compose.yml）；REQFLOW_TEST_DSN 可覆盖连接串。
package repository

import (
	"os"
)

func testDSN() string {
	if dsn := os.Getenv("REQFLOW_TEST_DSN"); dsn != "" {
		return dsn
	}
	return "postgres://reqflow:reqflow@127.0.0.1:5432/reqflow?sslmode=disable"
}

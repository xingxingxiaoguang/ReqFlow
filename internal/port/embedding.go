package port

import "context"

// Embedder 向量化客户端（OpenAI 兼容 /embeddings 抽象）。
// 未配置密钥时实现返回 Available()=false，语义匹配自动降级为仅精确匹配。
type Embedder interface {
	Generate(ctx context.Context, texts []string) ([][]float32, error)
	Available() bool
}

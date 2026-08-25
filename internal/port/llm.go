package port

import "context"

// StreamPhase 流式输出的两相位：思考期内容只展示、不进 JSON 解析缓冲。
type StreamPhase string

const (
	PhaseThinking StreamPhase = "thinking"
	PhaseAnswer   StreamPhase = "answer"
)

// StreamDelta 模型输出增量。
type StreamDelta struct {
	Phase StreamPhase
	Text  string
}

// LLMClient 对话模型客户端（OpenAI 兼容协议的抽象）。
// 第二波 bug 定级、微调会话均在同一契约上扩展，不新增依赖边。
type LLMClient interface {
	// StreamChat 流式对话：返回拼接后的完整正文；onDelta 实时回调增量。
	StreamChat(ctx context.Context, prompt string, onDelta func(StreamDelta)) (string, error)
	// Chat 非流式对话（流式解析失败时的回退通道）
	Chat(ctx context.Context, prompt string) (string, error)
	// Ping 连通性测试
	Ping(ctx context.Context) error
}

package app

import (
	"context"
	"fmt"
	"sync"

	"reqflow/internal/app/tools"
)

// DialogHub agent 人工交互的运行期桥：工具侧 Ask 阻塞登记，HTTP 侧 Answer 投递，
// SSE 侧 dialog 事件通知前端弹窗。每任务至多一个 pending（loop 顺序执行工具）。
//
// 刷新恢复走快照：dialog 是必须回答的阻塞事件，不能只靠瞬时 SSE（Broker 满则丢帧）——
// PendingDialog 随 SSE snapshot 下发，前端重连/刷新必收快照即可恢复弹窗。
// 服务重启 = 任务被 Recover 标 paused，本 hub 无 pending，语义自洽。
type DialogHub struct {
	mu      sync.Mutex
	broker  *Broker
	pending map[string]*pendingDialog // taskID → 等待中的提问
}

type pendingDialog struct {
	callID   string
	question string
	options  []string
	answer   chan string // 缓冲 1：Answer 投递后立即返回，不等待工具收取
}

func NewDialogHub(broker *Broker) *DialogHub {
	return &DialogHub{broker: broker, pending: map[string]*pendingDialog{}}
}

// Ask 登记 pending 并推送 ask 事件，阻塞等待人工回答。返回错误即未获得回答
// （ctx 取消 = 任务暂停）。任何出口都清理 pending 并推送 close 事件。
// 实现 tools.HumanAsker。
func (h *DialogHub) Ask(ctx context.Context, taskID, callID, question string, options []string) (string, error) {
	d := &pendingDialog{callID: callID, question: question, options: options, answer: make(chan string, 1)}

	h.mu.Lock()
	if prev, ok := h.pending[taskID]; ok {
		// 防御（顺序执行下不可达）：作废旧提问，让旧 Ask 以空回答收束
		close(prev.answer)
	}
	h.pending[taskID] = d
	h.mu.Unlock()
	h.broker.Publish(taskID, Event{Type: "dialog", TaskID: taskID,
		Data: dialogEvent{Phase: "ask", CallID: callID, Question: question, Options: options}})

	select {
	case answer := <-d.answer:
		h.finish(taskID, callID, "answered")
		return answer, nil
	case <-ctx.Done():
		h.finish(taskID, callID, "cancelled")
		return "", fmt.Errorf("等待人工回答时任务被中止: %w", ctx.Err())
	}
}

// Answer 人工应答入口（httpgin → TaskManager.AnswerDialog 透传）。
// 校验 call_id 匹配当前 pending 后投递；缓冲 1 保证工具侧必然收到。
func (h *DialogHub) Answer(taskID, callID, answer string) error {
	h.mu.Lock()
	d, ok := h.pending[taskID]
	h.mu.Unlock()
	if !ok {
		return fmt.Errorf("当前没有等待回答的问题")
	}
	if d.callID != callID {
		return fmt.Errorf("问题已更新，请刷新后重试")
	}
	d.answer <- answer
	return nil
}

// PendingDialog 当前等待回答的问题（SSE snapshot 恢复用）；无 pending 返回 nil。
func (h *DialogHub) PendingDialog(taskID string) any {
	h.mu.Lock()
	d, ok := h.pending[taskID]
	h.mu.Unlock()
	if !ok {
		return nil
	}
	return dialogEvent{Phase: "ask", CallID: d.callID, Question: d.question, Options: d.options}
}

// Clear 清理该任务的 pending（步骤退出兜底；Ask 自身出口已清理，通常无事可做）。
// 若仍有阻塞中的 Ask（防御路径），以空回答唤醒收束并广播 close 事件。
func (h *DialogHub) Clear(taskID string) {
	h.mu.Lock()
	d, ok := h.pending[taskID]
	if ok {
		delete(h.pending, taskID)
		close(d.answer) // 唤醒可能仍阻塞的 Ask（空回答收束）
	}
	h.mu.Unlock()
	if ok {
		h.broker.Publish(taskID, Event{Type: "dialog", TaskID: taskID,
			Data: dialogEvent{Phase: "close", CallID: d.callID, Reason: "cancelled"}})
	}
}

func (h *DialogHub) finish(taskID, callID, reason string) {
	h.mu.Lock()
	if d, ok := h.pending[taskID]; ok && d.callID == callID {
		delete(h.pending, taskID)
	}
	h.mu.Unlock()
	h.broker.Publish(taskID, Event{Type: "dialog", TaskID: taskID,
		Data: dialogEvent{Phase: "close", CallID: callID, Reason: reason}})
}

// dialogEvent SSE dialog 事件的负载形状（前端 Modal 的数据源）。
type dialogEvent struct {
	Phase    string   `json:"phase"` // ask | close
	CallID   string   `json:"call_id"`
	Question string   `json:"question,omitempty"`
	Options  []string `json:"options,omitempty"`
	Reason   string   `json:"reason,omitempty"` // close：answered | cancelled
}

// 接口断言：DialogHub 即分析工具的 HumanAsker。
var _ tools.HumanAsker = (*DialogHub)(nil)

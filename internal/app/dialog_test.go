package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// waitPending 轮询等待 Ask 完成 pending 登记（goroutine 启动时序）。
func waitPending(h *DialogHub, taskID string) {
	deadline := time.Now().Add(2 * time.Second)
	for h.PendingDialog(taskID) == nil && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
}

func TestDialogHubAskAnswerHandshake(t *testing.T) {
	broker := NewBroker()
	hub := NewDialogHub(broker)
	ch, unsub := broker.Subscribe("t1")
	defer unsub()

	type result struct {
		ans string
		err error
	}
	res := make(chan result, 1)
	go func() {
		ans, err := hub.Ask(context.Background(), "t1", "c1", "部署环境选哪个？", []string{"内部", "公有云"})
		res <- result{ans, err}
	}()

	// ask 事件发布（负载含问题与选项）
	ev := <-ch
	if ev.Type != "dialog" {
		t.Fatalf("事件类型 = %s", ev.Type)
	}
	raw, _ := json.Marshal(ev.Data)
	if !strings.Contains(string(raw), "部署环境选哪个？") || !strings.Contains(string(raw), `"options"`) {
		t.Fatalf("ask 事件负载 = %s", raw)
	}

	// PendingDialog 快照形状（SSE snapshot 恢复弹窗的依据）
	pd, ok := hub.PendingDialog("t1").(dialogEvent)
	if !ok || pd.CallID != "c1" || pd.Question != "部署环境选哪个？" || len(pd.Options) != 2 {
		t.Fatalf("PendingDialog = %+v", hub.PendingDialog("t1"))
	}

	// 错误 call_id 不匹配
	if err := hub.Answer("t1", "wrong", "内部"); err == nil {
		t.Fatal("call_id 不匹配应报错")
	}
	// 正确应答
	if err := hub.Answer("t1", "c1", "内部"); err != nil {
		t.Fatalf("Answer: %v", err)
	}
	r := <-res
	if r.err != nil || r.ans != "内部" {
		t.Fatalf("Ask 结果 = %+v", r)
	}

	// close 事件 + pending 清空
	ev2 := <-ch
	if raw2, _ := json.Marshal(ev2.Data); !strings.Contains(string(raw2), "answered") {
		t.Fatalf("close 事件负载 = %s", raw2)
	}
	if hub.PendingDialog("t1") != nil {
		t.Fatal("回答后 pending 应清空")
	}
	// 无 pending 时再答报错
	if err := hub.Answer("t1", "c1", "内部"); err == nil {
		t.Fatal("无 pending 应报错")
	}
}

func TestDialogHubCancelOnContextDone(t *testing.T) {
	hub := NewDialogHub(NewBroker())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	res := make(chan error, 1)
	go func() {
		_, err := hub.Ask(ctx, "t1", "c1", "问题", nil)
		res <- err
	}()
	waitPending(hub, "t1")

	cancel()
	select {
	case err := <-res:
		if err == nil {
			t.Fatal("ctx 取消应返回错误（任务暂停路径）")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Ask 未随 ctx 取消返回")
	}
	if hub.PendingDialog("t1") != nil {
		t.Fatal("取消后 pending 应清空")
	}
}

func TestDialogHubClearFallback(t *testing.T) {
	hub := NewDialogHub(NewBroker())
	ctx := context.Background()

	res := make(chan error, 1)
	go func() {
		_, err := hub.Ask(ctx, "t1", "c1", "问题", nil)
		res <- err
	}()
	waitPending(hub, "t1")

	// 步骤退出兜底：Clear 清 pending，阻塞的 Ask 以空回答收束
	hub.Clear("t1")
	select {
	case err := <-res:
		if err != nil {
			t.Fatalf("Clear 应让 Ask 以空回答收束而非报错: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Ask 未随 Clear 返回")
	}
	hub.Clear("t1") // 幂等：无 pending 时静默
}

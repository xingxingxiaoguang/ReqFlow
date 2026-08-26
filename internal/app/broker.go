package app

import "sync"

// Event 任务事件的统一载荷（SSE 事件名 = ev.Type，httpgin 原样透传）。
// 事件词表：task | step | items | progress | token | tool_trace | error
// 持久化不变量：Runner 先落库再 Publish（重放由快照兜底，实时事件只是增量）。
type Event struct {
	Type   string `json:"type"`
	TaskID string `json:"task_id"`
	Data   any    `json:"data"`
}

// Broker 进程内任务事件扇出：SSE 可重接的基础——客户端断开只退订，任务照跑，
// 重连后先收快照再收实时事件（subscribe-before-snapshot 消除竞态窗口）。
type Broker struct {
	mu   sync.Mutex
	subs map[string]map[chan Event]struct{}
}

// NewBroker 构造事件扇出。
func NewBroker() *Broker {
	return &Broker{subs: make(map[string]map[chan Event]struct{})}
}

// Publish 非阻塞发布：订阅通道满时丢弃该帧（快照回放兜底，发布者绝不被拖死）。
func (b *Broker) Publish(taskID string, ev Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs[taskID] {
		select {
		case ch <- ev:
		default:
		}
	}
}

// Subscribe 订阅任务事件流，返回接收通道与退订函数。退订后通道被关闭；
// 发送与退订均在锁内串行，不存在 send-on-closed。
func (b *Broker) Subscribe(taskID string) (<-chan Event, func()) {
	ch := make(chan Event, 64)
	b.mu.Lock()
	if b.subs[taskID] == nil {
		b.subs[taskID] = make(map[chan Event]struct{})
	}
	b.subs[taskID][ch] = struct{}{}
	b.mu.Unlock()

	var unsubOnce sync.Once
	unsub := func() {
		unsubOnce.Do(func() {
			b.mu.Lock()
			defer b.mu.Unlock()
			if _, ok := b.subs[taskID][ch]; !ok {
				return
			}
			delete(b.subs[taskID], ch)
			if len(b.subs[taskID]) == 0 {
				delete(b.subs, taskID)
			}
			close(ch)
		})
	}
	return ch, unsub
}

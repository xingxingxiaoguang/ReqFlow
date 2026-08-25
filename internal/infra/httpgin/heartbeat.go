package httpgin

import (
	"sync"
	"time"
)

const timeSecond = time.Second

func nowMillis() int64 { return time.Now().UnixMilli() }

func trim(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\n' || s[0] == '\r') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

// heartbeat 周期任务（心跳推送），Start 后可反复 Stop/Restart。
type heartbeat struct {
	interval time.Duration
	onTick   func()

	mu     sync.Mutex
	stopCh chan struct{}
}

func newHeartbeat(interval time.Duration, onTick func()) *heartbeat {
	return &heartbeat{interval: interval, onTick: onTick}
}

func (h *heartbeat) Start() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stopCh != nil {
		return
	}
	stop := make(chan struct{})
	h.stopCh = stop
	go func() {
		t := time.NewTicker(h.interval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				h.onTick()
			}
		}
	}()
}

func (h *heartbeat) Stop() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stopCh != nil {
		close(h.stopCh)
		h.stopCh = nil
	}
}

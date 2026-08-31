package agent

import (
	"context"
	"errors"
	"testing"

	"reqflow/internal/port"
)

func TestExecuteBuildsGenericAgentTrace(t *testing.T) {
	client := &scriptedClient{responses: []*port.Message{
		assistantToolCalls(port.StopReasonToolUse, port.ToolCall{ID: "c1", Name: "done"}),
	}}
	state := RunState{Context: port.Context{Messages: []port.Message{port.NewUserMessage("开始")}}}
	traces := TraceEnvelope{}
	flushes := 0
	err := Execute(context.Background(), client, []Tool{&doneTool{echoTool{name: "done"}}},
		&state, &traces, RunOptions{ID: "run-1", Label: "结构化分析", Loop: Config{
			MaxIterations: 2, RequireToolTermination: true,
		}, Stats: func() map[string]int { return map[string]int{"submitted": 1} },
			Trace:   TraceOptions{ExposeToolResult: func(string) bool { return true }},
			OnFlush: func(RunTrace) error { flushes++; return nil }})
	if err != nil {
		t.Fatal(err)
	}
	if flushes < 2 || len(traces.AgentRuns) != 1 {
		t.Fatalf("flushes=%d traces=%+v", flushes, traces.AgentRuns)
	}
	trace := traces.AgentRuns[0]
	if trace.ID != "run-1" || trace.Label != "结构化分析" || trace.Status != "succeeded" ||
		trace.RequestCount != 1 || trace.Stats["submitted"] != 1 {
		t.Fatalf("trace=%+v", trace)
	}
	if len(trace.Tools) != 1 || trace.Tools[0].Status != "done" || trace.Tools[0].Result != "收束" {
		t.Fatalf("tools=%+v", trace.Tools)
	}
}

func TestExecuteStopsWhenInitialCheckpointFails(t *testing.T) {
	want := errors.New("checkpoint unavailable")
	client := &scriptedClient{responses: []*port.Message{assistantText("不应调用")}}
	state := RunState{Context: port.Context{Messages: []port.Message{port.NewUserMessage("开始")}}}
	err := Execute(context.Background(), client, nil, &state, &TraceEnvelope{}, RunOptions{
		ID: "run-1", OnFlush: func(RunTrace) error { return want },
	})
	if !errors.Is(err, want) || client.calls != 0 {
		t.Fatalf("err=%v calls=%d", err, client.calls)
	}
}

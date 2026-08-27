package logic

import (
	"strings"
	"testing"

	"reqflow/internal/domain/model"
)

func wf(steps ...model.WorkflowStep) model.Workflow {
	return model.Workflow{Type: "t", Name: "工作流", Desc: "描述", Steps: steps}
}

func step(seq int, name string, kind model.StepKind, deps ...model.StepDependency) model.WorkflowStep {
	return model.WorkflowStep{Seq: seq, Name: name, Kind: kind, Deps: deps}
}

func TestIsValidIdentifier(t *testing.T) {
	yes := []string{"a", "bug_import", "x1_y2", strings.Repeat("a", 63)}
	no := []string{"", "A", "1abc", "has-dash", "has space", "中文", strings.Repeat("a", 64), "_lead"}
	for _, s := range yes {
		if !IsValidIdentifier(s) {
			t.Errorf("%q 应合法", s)
		}
	}
	for _, s := range no {
		if IsValidIdentifier(s) {
			t.Errorf("%q 应非法", s)
		}
	}
}

func TestValidateWorkflowShape(t *testing.T) {
	okChain := func() model.Workflow {
		return wf(
			step(1, "上传解析", model.StepKindParse),
			step(2, "确认解析", model.StepKindHuman),
			step(3, "AI 分析", model.StepKindAnalyze),
			step(4, "生成数据集", model.StepKindDataset),
		)
	}
	t.Run("合法链通过", func(t *testing.T) {
		if err := ValidateWorkflowShape(okChain()); err != nil {
			t.Fatalf("应通过: %v", err)
		}
	})
	t.Run("空步骤链拒绝", func(t *testing.T) {
		w := wf()
		if err := ValidateWorkflowShape(w); err == nil || !strings.Contains(err.Error(), "不能为空") {
			t.Fatalf("期望空步骤错误，got %v", err)
		}
	})
	t.Run("未知 kind 拒绝（封闭集合）", func(t *testing.T) {
		w := okChain()
		w.Steps[3].Kind = model.StepKind("deploy")
		err := ValidateWorkflowShape(w)
		if err == nil || !strings.Contains(err.Error(), "封闭集合") {
			t.Fatalf("期望封闭集合错误，got %v", err)
		}
	})
	t.Run("序号断裂拒绝", func(t *testing.T) {
		w := okChain()
		w.Steps[2].Seq = 5
		if err := ValidateWorkflowShape(w); err == nil || !strings.Contains(err.Error(), "连续") {
			t.Fatalf("期望连续性错误，got %v", err)
		}
	})
	t.Run("步骤名重复拒绝", func(t *testing.T) {
		w := okChain()
		w.Steps[3].Name = w.Steps[0].Name
		if err := ValidateWorkflowShape(w); err == nil || !strings.Contains(err.Error(), "重复") {
			t.Fatalf("期望重复错误，got %v", err)
		}
	})
	t.Run("名称与描述超长拒绝", func(t *testing.T) {
		w := okChain()
		w.Name = strings.Repeat("长", 61)
		if err := ValidateWorkflowShape(w); err == nil {
			t.Fatal("名称超长应拒绝")
		}
		w = okChain()
		w.Desc = strings.Repeat("述", 2001)
		if err := ValidateWorkflowShape(w); err == nil {
			t.Fatal("描述超长应拒绝")
		}
	})
	t.Run("依赖条数与长度上限", func(t *testing.T) {
		deps := make([]model.StepDependency, MaxDepsPerStep+1)
		for i := range deps {
			deps[i] = model.StepDependency{Data: "d", Tool: "t"}
		}
		w := okChain()
		w.Steps[2].Deps = deps
		if err := ValidateWorkflowShape(w); err == nil {
			t.Fatal("依赖条数超限应拒绝")
		}
		w = okChain()
		w.Steps[2].Deps = []model.StepDependency{{Data: strings.Repeat("d", 241)}}
		if err := ValidateWorkflowShape(w); err == nil {
			t.Fatal("依赖描述超长应拒绝")
		}
	})
}

func TestCheckWorkflowCompat(t *testing.T) {
	base := func() model.Workflow {
		return wf(
			step(1, "上传解析", model.StepKindParse),
			step(2, "确认解析", model.StepKindHuman),
			step(3, "AI 分析", model.StepKindAnalyze),
			step(4, "生成数据集", model.StepKindDataset),
		)
	}

	t.Run("无变更", func(t *testing.T) {
		r := CheckWorkflowCompat(base(), base())
		if r.Blocked || r.NeedsConfirm || len(r.Findings) != 1 || r.Findings[0].Rule != "unchanged" {
			t.Fatalf("期望单一 unchanged: %+v", r)
		}
	})
	t.Run("新增步骤 OK", func(t *testing.T) {
		nw := base()
		nw.Steps = append(nw.Steps, step(5, "补充确认", model.StepKindHuman))
		r := CheckWorkflowCompat(base(), nw)
		if r.Blocked || r.NeedsConfirm {
			t.Fatalf("新增不应 warn/block: %+v", r)
		}
	})
	t.Run("移除人工门 gate_removed 警告", func(t *testing.T) {
		nw := base()
		// 去掉「确认解析」，其余步骤前移重排（按名对齐不产生连锁误报）
		nw.Steps = []model.WorkflowStep{
			step(1, "上传解析", model.StepKindParse),
			step(2, "AI 分析", model.StepKindAnalyze),
			step(3, "生成数据集", model.StepKindDataset),
		}
		r := CheckWorkflowCompat(base(), nw)
		found := false
		for _, f := range r.Findings {
			if f.Rule == "gate_removed" && f.Level == CompatWarn {
				found = true
			}
		}
		if !found || !r.NeedsConfirm {
			t.Fatalf("期望 gate_removed 警告: %+v", r.Findings)
		}
	})
	t.Run("步骤位置调整仅 order_changed", func(t *testing.T) {
		nw := wf(
			step(1, "AI 分析", model.StepKindAnalyze),
			step(2, "上传解析", model.StepKindParse),
			step(3, "确认解析", model.StepKindHuman),
			step(4, "生成数据集", model.StepKindDataset),
		)
		r := CheckWorkflowCompat(base(), nw)
		for _, f := range r.Findings {
			switch f.Rule {
			case "kind_changed", "step_added", "step_removed", "gate_removed":
				t.Fatalf("纯位移不应触发 %s: %v", f.Rule, f.Message)
			}
		}
	})
	t.Run("kind 变更警告", func(t *testing.T) {
		nw := base()
		nw.Steps[1].Kind = model.StepKindAnalyze
		r := CheckWorkflowCompat(base(), nw)
		hit := false
		for _, f := range r.Findings {
			if f.Rule == "kind_changed" {
				hit = true
			}
		}
		if !hit {
			t.Fatalf("期望 kind_changed: %+v", r.Findings)
		}
	})
	t.Run("产出缺失警告", func(t *testing.T) {
		nw := base()
		nw.Steps = nw.Steps[:3]
		r := CheckWorkflowCompat(base(), nw)
		hit := false
		for _, f := range r.Findings {
			if f.Rule == "output_missing" {
				hit = true
			}
		}
		if !hit {
			t.Fatalf("期望 output_missing: %+v", r.Findings)
		}
	})
	t.Run("多分析步骤警告", func(t *testing.T) {
		nw := base()
		nw.Steps = append(nw.Steps[:2], step(3, "粗析", model.StepKindAnalyze), step(4, "细析", model.StepKindAnalyze))
		r := CheckWorkflowCompat(base(), nw)
		hit := false
		for _, f := range r.Findings {
			if f.Rule == "multi_analyze" {
				hit = true
			}
		}
		if !hit {
			t.Fatalf("期望 multi_analyze: %+v", r.Findings)
		}
	})
	t.Run("永无 block 级（快照隔离兜底）", func(t *testing.T) {
		nw := base()
		nw.Steps = nw.Steps[:2]
		r := CheckWorkflowCompat(base(), nw)
		for _, f := range r.Findings {
			if f.Level == CompatBlock {
				t.Fatalf("工作流兼容判定不应出现 ❌: %+v", f)
			}
		}
	})
}

func TestCountStepsOf(t *testing.T) {
	w := wf(step(1, "a", model.StepKindHuman), step(2, "b", model.StepKindAnalyze), step(3, "c", model.StepKindAnalyze))
	if got := CountStepsOf(w, model.StepKindAnalyze); got != 2 {
		t.Fatalf("期望 2，got %d", got)
	}
	if got := CountStepsOf(w, model.StepKindParse); got != 0 {
		t.Fatalf("期望 0，got %d", got)
	}
}

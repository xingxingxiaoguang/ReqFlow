// 工作流定义的形状校验与兼容规则（M4 工作流定义外置的写守卫核心；纯函数）。
//
// 红线不变量（PRODUCT §4 决策二）：StepKind 是封闭集合——元数据只能「编排」既有
// kind 的步骤链，新增 kind 执行器仍是代码开发。因此形状校验的第一关是 kind 白名单。
// 快照语义（METADATA §4.5）保证存量任务按 tasks.workflow 快照执行展示，
// 工作流热编辑只影响新任务——兼容判定的所有结论都以「对新任务的影响」为口径。
package logic

import (
	"fmt"
	"strings"

	"reqflow/internal/domain/model"
)

// 工作流护栏常量（长度上限均为 rune 数或字符串字节长度内的人话边界）。
const (
	MaxWorkflowSteps    = 16  // 步骤数上限
	MaxStepNameLen      = 60  // 步骤名
	MaxWorkflowDescLen  = 2000 // 工作流描述
	MaxDepsPerStep      = 8   // 单步骤依赖声明条数
	MaxDependencyFnQLen = 240 // 单条依赖描述长度
)

// IsValidIdentifier 平台标识符白名单：小写字母开头的 snake_case（≤63 字符）。
// 字段 key、任务类型标识、数据集类型共用同一语法——key 会被拼进过滤 SQL、
// 类型会拼进向量集合等运行时位置，注入面在形状层统一收口。
func IsValidIdentifier(s string) bool {
	return fieldKeyPattern.MatchString(s)
}

// ValidStepKinds 当前封闭集合内的全部步骤类型（向导下拉与校验同源）。
func ValidStepKinds() []model.StepKind {
	return []model.StepKind{
		model.StepKindParse,
		model.StepKindHuman,
		model.StepKindAnalyze,
		model.StepKindDataset,
	}
}

// ValidStepKind kind 是否在封闭集合内。
func ValidStepKind(k model.StepKind) bool {
	switch k {
	case model.StepKindParse, model.StepKindHuman, model.StepKindAnalyze, model.StepKindDataset:
		return true
	}
	return false
}

// ValidateWorkflowShape 工作流结构合法性（纯形状，不含语义编排建议——
// 编排级建议归 CheckWorkflowCompat 的 ⚠️ 判定）。Type 由调用方强制为 key，
// 这里只校验其余部分。
func ValidateWorkflowShape(w model.Workflow) error {
	if strings.TrimSpace(w.Name) == "" {
		return fmt.Errorf("工作流名称不能为空")
	}
	if len([]rune(w.Name)) > MaxLabelLen {
		return fmt.Errorf("工作流名称超长（≤%d 字符）", MaxLabelLen)
	}
	if len([]rune(w.Desc)) > MaxWorkflowDescLen {
		return fmt.Errorf("工作流描述超长（≤%d 字符）", MaxWorkflowDescLen)
	}
	if len(w.Steps) == 0 {
		return fmt.Errorf("步骤链不能为空")
	}
	if len(w.Steps) > MaxWorkflowSteps {
		return fmt.Errorf("步骤数超上限（≤%d 步），当前 %d", MaxWorkflowSteps, len(w.Steps))
	}
	names := map[string]bool{}
	for i, s := range w.Steps {
		if s.Seq != i+1 {
			return fmt.Errorf("步骤 %d 的序号必须连续且从 1 开始（期望 seq=%d，实际 %d）", i+1, i+1, s.Seq)
		}
		if !ValidStepKind(s.Kind) {
			return fmt.Errorf("步骤「%s」的类型非法: %s（StepKind 为封闭集合：%v）",
				s.Name, s.Kind, ValidStepKinds())
		}
		if strings.TrimSpace(s.Name) == "" {
			return fmt.Errorf("第 %d 步缺少步骤名", s.Seq)
		}
		if len([]rune(s.Name)) > MaxStepNameLen {
			return fmt.Errorf("步骤「%s」名称超长（≤%d 字符）", s.Name, MaxStepNameLen)
		}
		if names[s.Name] {
			return fmt.Errorf("步骤名重复: %s", s.Name)
		}
		names[s.Name] = true
		if len(s.Deps) > MaxDepsPerStep {
			return fmt.Errorf("步骤「%s」依赖声明超过 %d 条", s.Name, MaxDepsPerStep)
		}
		for _, d := range s.Deps {
			if len([]rune(d.Data)) > MaxDependencyFnQLen || len([]rune(d.Tool)) > MaxDependencyFnQLen {
				return fmt.Errorf("步骤「%s」存在超长依赖声明（单项 ≤%d 字符）", s.Name, MaxDependencyFnQLen)
			}
		}
	}
	return nil
}

// CountStepsOf 统计某 kind 的步骤数（语义校验与判定共用的口径）。
func CountStepsOf(w model.Workflow, k model.StepKind) int {
	n := 0
	for _, s := range w.Steps {
		if s.Kind == k {
			n++
		}
	}
	return n
}

/* ---- 兼容规则引擎 ---- */

// CheckWorkflowCompat 新旧工作流对比。按**步骤名**对齐（形状校验已保证唯一；
// 序号/位置的增删不会引起连锁误报）：
//   - 消失的步骤名 → step_removed / gate_removed（人工门）
//   - 新出现的步骤名 → step_added
//   - 同名不同 kind → kind_changed（执行器更换）
//
// 级别约定：全部结论都是 ✅ / ⚠️ —— StepKind 封闭集已由形状校验把守，工作流变更
// 借快照机制天然隔离存量任务，故没有 ❌ 级硬拦截；⚠️ 提醒确认新任务的执行链变化。
func CheckWorkflowCompat(old, new model.Workflow) CompatReport {
	var r CompatReport
	oldByName := map[string]model.WorkflowStep{}
	for _, s := range old.Steps {
		oldByName[s.Name] = s
	}
	newNames := map[string]bool{}
	for _, n := range new.Steps {
		newNames[n.Name] = true
		o, existed := oldByName[n.Name]
		switch {
		case !existed:
			r.add(CompatFinding{Level: CompatOK, Rule: "step_added", Field: n.Name,
				Message: fmt.Sprintf("新增步骤「%s」（%s）：仅影响新任务", n.Name, n.Kind)})
		case o.Kind != n.Kind:
			r.add(CompatFinding{Level: CompatWarn, Rule: "kind_changed", Field: n.Name,
				Message: fmt.Sprintf("步骤「%s」类型 %s → %s：执行器更换，新任务该环节行为改变", n.Name, o.Kind, n.Kind)})
		case o.Seq != n.Seq:
			r.add(CompatFinding{Level: CompatOK, Rule: "order_changed", Field: n.Name,
				Message: fmt.Sprintf("步骤「%s」位置 %d → %d：编排顺序调整，执行环节不变，兼容", n.Name, o.Seq, n.Seq)})
		}
	}
	// 旧链中消失的步骤
	for _, o := range old.Steps {
		if newNames[o.Name] {
			continue
		}
		msg := fmt.Sprintf("移除步骤「%s」（%s）：新任务跳过该环节；存量任务按创建时快照不受影响",
			o.Name, o.Kind)
		lvl := CompatWarn
		rule := "step_removed"
		if o.Kind == model.StepKindHuman {
			msg += "；人工确认门被移除，请确认无需人工把关"
			rule = "gate_removed"
		}
		r.add(CompatFinding{Level: lvl, Rule: rule, Field: o.Name, Message: msg})
	}

	// 编排级语义提示
	if CountStepsOf(old, model.StepKindDataset) > 0 && CountStepsOf(new, model.StepKindDataset) == 0 {
		r.add(CompatFinding{Level: CompatWarn, Rule: "output_missing",
			Message: "新工作流不含数据集生成步骤：任务将没有产出数据集（无法作为后续任务的输入底料），请确认"})
	}
	if c := CountStepsOf(new, model.StepKindAnalyze); c > 1 {
		r.add(CompatFinding{Level: CompatWarn, Rule: "multi_analyze",
			Message: fmt.Sprintf("新工作流含 %d 个分析步骤：每个都会以同一装配描述发起独立分析，通常不是预期编排", c)})
	}
	if strings.TrimSpace(old.Desc) != "" && old.Desc != new.Desc {
		r.add(CompatFinding{Level: CompatOK, Rule: "desc_changed",
			Message: "工作流描述变更：纯展示层，兼容"})
	}
	if len(r.Findings) == 0 {
		r.add(CompatFinding{Level: CompatOK, Rule: "unchanged", Message: "未检测到工作流变更"})
	}
	return r
}

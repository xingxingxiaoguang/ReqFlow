package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reqflow/internal/app/tools"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

/* ---- 金标准（M2 验收定义）：玩具任务类型 test_review 全链路 ----
 *
 * 只注册聚合定义（自定义 schema + 自定义写入工具名 + 最小 profile），
 * 不写任何 struct / Normalize / ValuesOf——schema 声明端到端生效：
 * 提示词渲染 / 写入校验 / 归一化默认值 / 草稿落库 / 数据集写入全链路跟随。
 * 此处断言失败 = 字段袋化出现回归（某处又硬编码了 requirement 字段）。
 */

const (
	toyTaskType    = "test_review"
	toyDatasetType = "review_note"
)

// toySchema 自定义 schema：与 requirement 无任何字段重叠。
func toySchema() model.DatasetSchema {
	return model.DatasetSchema{
		Type: toyDatasetType, Label: "评审发现", Version: 1,
		Fields: []model.FieldSpec{
			{Key: "finding", Label: "发现", Type: model.FieldString, Required: true,
				InVector: model.VectorTitle, InKey: true, Default: "未命名发现",
				Prompt: "发现的问题，一句话概括"},
			{Key: "severity", Label: "级别", Type: model.FieldEnum,
				Enum: []string{"p0", "p1", "p2"}, Default: "p1", Filterable: true,
				Prompt: "严重级别：p0=致命 p1=一般 p2=轻微，默认 p1"},
			{Key: "hours", Label: "修复耗时", Type: model.FieldNumber, Default: 2,
				Prompt: "预计修复耗时（小时）"},
		},
	}
}

// registerToyType 经注册缝注入玩具任务类型（复用 requirement 四步工作流骨架）。
func registerToyType(t *testing.T) {
	t.Helper()
	orig := extraTaskTypes
	extraTaskTypes = append(orig, TaskTypeDefinition{
		Type:        toyTaskType,
		Workflow:    requirementImportWorkflow(),
		DatasetType: toyDatasetType,
		Schema:      toySchema,
		Profile: AnalyzeProfile{
			Role:   "你是代码评审助手，阅读评审记录并提取问题发现。\n\n{field_spec}",
			Schema: toySchema,
			Write:  tools.WriteSpec{Name: "write_findings", Schema: toySchema()},
		},
	})
	t.Cleanup(func() { extraTaskTypes = orig })
}

// TestToyTaskTypeAgentFullChain agent 模式全链路：创建 → 解析 → 分析 → 生成数据集。
func TestToyTaskTypeAgentFullChain(t *testing.T) {
	registerToyType(t)
	repo := newMemTasks()
	datasets := newMemDatasets()
	writer := NewDatasetWriter(&fakeEmbedder{}, datasets, 10) // 真实写入器：schema 驱动校验/身份/指纹
	llm := &scriptedLLM{responses: []*port.Message{
		toolCallMsg("g1", "read_document", `{"offset":1}`),
		toolCallMsg("g2", "write_findings",
			`{"items":[{"finding":"登录接口无防刷限制","severity":"p0"},{"finding":"日志明文打印密码","hours":"3.5"}]}`),
		finalTextMsg("共 2 个发现"),
	}}
	analyze := NewAnalyzeService(llm, "")
	analyze.EnableAgentMode(0)
	mgr := newTestManager(repo, &fakeParse{text: "## 评审记录\n\n登录接口无防刷限制。"}, analyze, datasets, writer)

	ctx := context.Background()
	// 新流程：字段定义随数据集行——预置玩具 schema 数据集并绑定创建
	ds := seedTestDataset(t, datasets, toySchema(), "评审发现集")
	task, err := mgr.Create(ctx, toyTaskType, "评审记录.txt", ds.ID)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	_ = mgr.TriggerParse(ctx, task.ID, "/tmp/review.txt")
	waitTask(t, repo, task.ID, func(tk *model.Task) bool { return tk.Status == model.TaskStatusAwaiting })

	if err := mgr.TriggerAnalyze(ctx, task.ID); err != nil {
		t.Fatalf("TriggerAnalyze: %v", err)
	}
	waitTask(t, repo, task.ID, func(tk *model.Task) bool {
		return tk.Status == model.TaskStatusAwaiting && tk.CurrentStep == 4
	})

	// 系统提示词由玩具 schema 装配：自定义字段规范段 + 枚举值域 + 自定义工具名
	var cc port.Context
	if err := json.Unmarshal([]byte(mustTask(t, repo, task.ID).AgentContext), &cc); err != nil {
		t.Fatalf("AgentContext: %v", err)
	}
	for _, want := range []string{"**finding**", `"p0"/"p1"/"p2"`, "write_findings"} {
		if !strings.Contains(cc.SystemPrompt, want) {
			t.Fatalf("SystemPrompt 缺 %q:\n%s", want, cc.SystemPrompt)
		}
	}
	if strings.Contains(cc.SystemPrompt, "write_work_items") {
		t.Fatal("玩具类型不应出现 requirement 的写入工具名")
	}

	// 草稿 = 自定义字段的字段袋；默认值随玩具 schema 填充
	items := mustItems(t, repo, task.ID)
	if len(items) != 2 {
		t.Fatalf("items = %d", len(items))
	}
	a := items[0].Values()
	if a["finding"] != "登录接口无防刷限制" || a["severity"] != "p0" {
		t.Fatalf("item0 = %+v", a)
	}
	b := items[1].Values()
	if b["severity"] != "p1" { // 枚举缺省 → 玩具 schema 默认
		t.Fatalf("自定义默认未生效: %+v", b)
	}
	if b["hours"] != 3.5 { // 字符串数字宽松解析
		t.Fatalf("数字解析: %+v", b)
	}

	// 生成数据集：条目身份/校验/落库同样由数据集行上的玩具 schema 驱动（写入绑定数据集）
	if err := mgr.TriggerGenerateDataset(ctx, task.ID, DatasetTarget{Mode: WriteModeMerge, DatasetID: ds.ID}); err != nil {
		t.Fatalf("TriggerGenerateDataset: %v", err)
	}
	got := waitTask(t, repo, task.ID, func(tk *model.Task) bool { return tk.Status == model.TaskStatusSucceeded })
	written, err := datasets.GetDataset(ctx, got.OutputDatasetID)
	if err != nil || written.Type != toyDatasetType || written.ItemCount != 2 {
		t.Fatalf("数据集 = %+v err=%v", written, err)
	}
	dsItems, _ := datasets.ListDatasetItems(ctx, written.ID, 10)
	first := parseFieldsValues(dsItems[0].Fields)
	if first["finding"] != "登录接口无防刷限制" || first["severity"] != "p0" {
		t.Fatalf("数据集条目 = %+v", first)
	}
}

// TestToyTaskTypeClassicMode 单发降级路径：输出契约与归一化同样由玩具 schema 驱动。
func TestToyTaskTypeClassicMode(t *testing.T) {
	registerToyType(t)
	llm := &scriptedLLM{responses: []*port.Message{
		finalTextMsg(`[{"finding":"空指针解引用","severity":"p2"}]`),
	}}
	svc := NewAnalyzeService(llm, "")
	res, err := svc.Run(context.Background(),
		AnalyzeInput{TaskID: "t-toy", TaskType: toyTaskType, FileName: "评审.txt", Text: "评审记录"},
		nil, nil, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Items) != 1 {
		t.Fatalf("items = %+v", res.Items)
	}
	v := res.Items[0].Values()
	if v["finding"] != "空指针解引用" || v["severity"] != "p2" || v["hours"] != float64(2) {
		t.Fatalf("字段袋 = %+v（hours 应填玩具默认 2）", v)
	}
	// 单发 prompt 由玩具 schema 装配（字段规范段 + 枚举值域）
	var cc port.Context
	if err := json.Unmarshal([]byte(res.AgentContext), &cc); err != nil {
		t.Fatalf("AgentContext: %v", err)
	}
	prompt := cc.Messages[0].Text()
	if !strings.Contains(prompt, "**finding**") || !strings.Contains(prompt, `"p0"/"p1"/"p2"`) {
		t.Fatalf("单发 prompt = %.300s", prompt)
	}
}

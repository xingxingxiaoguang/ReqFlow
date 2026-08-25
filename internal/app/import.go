package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"reqflow/internal/domain/logic"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

// ImportError 单条导入失败信息。
type ImportError struct {
	Title  string `json:"title"`
	Detail string `json:"error"`
}

// CreatedProjectInfo 导入过程中自动创建的项目。
type CreatedProjectInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ImportProgress 逐条导入进度。
type ImportProgress struct {
	Current int    `json:"current"`
	Total   int    `json:"total"`
	Title   string `json:"title"`
	Status  string `json:"status"` // success | failed
	Message string `json:"message,omitempty"`
}

// ImportResult 导入统计。
type ImportResult struct {
	Success         int                  `json:"success"`
	Failed          int                  `json:"failed"`
	Errors          []ImportError        `json:"errors"`
	CreatedProjects []CreatedProjectInfo `json:"createdProjects"`
	RecordStatus    string               `json:"recordStatus"`
}

// DraftInput 导入/匹配草稿的 HTTP 入参形状（app 层 DTO，port/domain 类型不外泄）。
type DraftInput struct {
	ProjectName        string  `json:"project_name"`
	Title              string  `json:"title"`
	Description        string  `json:"description"`
	Priority           string  `json:"priority"`
	EstimatedHours     float64 `json:"estimated_hours"`
	StartAt            string  `json:"start_at"`
	EndAt              string  `json:"end_at"`
	TypeID             string  `json:"type_id"`
	AssigneeName       string  `json:"assignee_name"`
	State              string  `json:"state"`
	SolutionSuggestion string  `json:"solution_suggestion"`
}

func (d DraftInput) toModel() model.DraftItem {
	return model.DraftItem{
		ProjectName: d.ProjectName, Title: d.Title, Description: d.Description,
		Priority: d.Priority, EstimatedHours: d.EstimatedHours,
		StartAt: d.StartAt, EndAt: d.EndAt, TypeID: d.TypeID,
		AssigneeName: d.AssigneeName, State: d.State,
		SolutionSuggestion: d.SolutionSuggestion,
	}
}

// ImportItemInput 参与导入的单条草稿（携带明细 ID 用于结果回写）。
type ImportItemInput struct {
	ItemID string     `json:"id"`
	Draft  DraftInput `json:"draft"`
}

// ImportInput 导入请求。
type ImportInput struct {
	RecordID  string
	ProjectID string // 平台项目 ID；"new:项目名" 表示自动创建
	Items     []ImportItemInput
}

// ImportService 批量导入用例：元数据映射 + 负责人解析 + 工时换算 + 有限并发建单 + 结果回写。
type ImportService struct {
	platform      port.PlatformClient
	meta          port.MetaRepo
	records       port.ImportRepo
	projects      port.ProjectRepo
	workloadUnit  string
	concurrency   int
}

// NewImportService 构造用例。
func NewImportService(
	platform port.PlatformClient, meta port.MetaRepo, records port.ImportRepo, projects port.ProjectRepo,
	workloadUnit string, concurrency int,
) *ImportService {
	if concurrency <= 0 {
		concurrency = 3
	}
	return &ImportService{
		platform: platform, meta: meta, records: records, projects: projects,
		workloadUnit: workloadUnit, concurrency: concurrency,
	}
}

// Run 执行批量导入。report 按完成顺序逐条回调（喂给 SSE）。
func (s *ImportService) Run(ctx context.Context, in ImportInput, report func(ImportProgress)) (*ImportResult, error) {
	if report == nil {
		report = func(ImportProgress) {}
	}
	if len(in.Items) == 0 {
		return nil, fmt.Errorf("没有可导入的工作项")
	}

	rec, err := s.records.GetRecord(ctx, in.RecordID)
	if err != nil {
		return nil, fmt.Errorf("导入记录不存在: %w", err)
	}

	/* ---- 目标项目解析（含 new: 自动建项目） ---- */
	targetProjectID := in.ProjectID
	var result ImportResult
	if name, ok := strings.CutPrefix(in.ProjectID, "new:"); ok {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, fmt.Errorf("自动创建项目时项目名不能为空")
		}
		created, err := s.platform.CreateProject(ctx, port.CreateProjectInput{
			Name: name, Identifier: logic.GenerateProjectIdentifier(name),
		})
		if err != nil {
			return nil, fmt.Errorf("自动创建项目失败: %w", err)
		}
		targetProjectID = created.ID
		result.CreatedProjects = append(result.CreatedProjects, CreatedProjectInfo{ID: created.ID, Name: name})
		// 立即写入本地缓存，保证导入后无需手动同步即可见
		_ = s.projects.UpsertWithVectors(ctx, []port.ProjectVector{{
			Project: model.Project{ID: created.ID, Name: name, Description: created.Description},
		}})
	}

	/* ---- 元数据与成员（名称→UUID、姓名→ID 映射的底料） ---- */
	types, err := s.meta.ListTypes(ctx, targetProjectID)
	if err != nil {
		return nil, err
	}
	priorities, err := s.meta.ListPriorities(ctx, targetProjectID)
	if err != nil {
		return nil, err
	}
	members, _ := s.platform.ListProjectMembers(ctx, targetProjectID) // 失败降级：负责人留空

	rec.Status = model.RecordStatusImporting
	rec.TargetProjectID = targetProjectID
	if name, ok := strings.CutPrefix(in.ProjectID, "new:"); ok {
		rec.TargetProjectName = strings.TrimSpace(name)
	}
	_ = s.records.UpdateRecord(ctx, rec)

	/* ---- 有限并发建单 ---- */
	type job struct {
		itemID string
		draft  model.DraftItem
	}
	jobs := make([]job, len(in.Items))
	for i, it := range in.Items {
		jobs[i] = job{itemID: it.ItemID, draft: it.Draft.toModel()}
	}

	total := len(jobs)
	var mu sync.Mutex
	completed := 0
	sem := make(chan struct{}, s.concurrency)
	var wg sync.WaitGroup

	for _, j := range jobs {
		j := j
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			payload := s.buildPayload(j.draft, targetProjectID, types, priorities, members)
			created, err := s.platform.CreateWorkItem(ctx, payload)

			mu.Lock()
			defer mu.Unlock()
			completed++
			if err != nil {
				result.Failed++
				detail := err.Error()
				result.Errors = append(result.Errors, ImportError{Title: j.draft.Title, Detail: detail})
				if j.itemID != "" {
					_ = s.records.UpdateItemResult(ctx, j.itemID, "", "", model.ItemStatusFailed, detail)
				}
				report(ImportProgress{Current: completed, Total: total, Title: j.draft.Title, Status: "failed", Message: detail})
				return
			}
			result.Success++
			if j.itemID != "" {
				_ = s.records.UpdateItemResult(ctx, j.itemID, created.ID, created.Identifier, model.ItemStatusSuccess, "")
			}
			report(ImportProgress{Current: completed, Total: total, Title: j.draft.Title, Status: "success"})
		}()
	}
	wg.Wait()

	/* ---- 记录终态 ---- */
	finalStatus := model.RecordStatusSuccess
	switch {
	case result.Failed == total:
		finalStatus = model.RecordStatusFailed
	case result.Failed > 0:
		finalStatus = model.RecordStatusPartialSuccess
	}
	rec.Status = finalStatus
	rec.ImportedCount = result.Success
	rec.FailedCount = result.Failed
	rec.TargetProjectID = targetProjectID
	_ = s.records.UpdateRecord(ctx, rec)
	result.RecordStatus = finalStatus
	return &result, nil
}

// buildPayload 组装平台创建载荷：类型/优先级 UUID 映射、负责人三级解析、
// 工时单位换算、解决方案建议并入描述、ISO 时间转 Unix 秒。
func (s *ImportService) buildPayload(
	d model.DraftItem, projectID string,
	types []model.MetaType, priorities []model.MetaPriority, members []port.PlatformMember,
) port.CreateWorkItemInput {
	desc := d.Description
	if d.SolutionSuggestion != "" {
		desc = strings.TrimRight(desc, "\n") + "\n\n【解决方案建议】\n" + d.SolutionSuggestion
	}
	in := port.CreateWorkItemInput{
		ProjectID:    projectID,
		TypeID:       logic.ResolveTypeID(d.TypeID, types),
		Title:        d.Title,
		Description:  desc,
		PriorityID:   logic.ResolvePriorityID("", d.Priority, priorities),
		AssigneeID:   resolveAssigneeID(d.AssigneeName, members),
		StartAt:      parseUnix(d.StartAt),
		EndAt:        parseUnix(d.EndAt),
		EstimatedWorkload: logic.HoursToWorkload(d.EstimatedHours, s.workloadUnit),
	}
	return in
}

// resolveAssigneeID 负责人三级解析：精确（name/display_name）→ 包含匹配（双向，≥2字防误匹配）→ 空。
func resolveAssigneeID(name string, members []port.PlatformMember) string {
	name = strings.TrimSpace(name)
	if name == "" || len(members) == 0 {
		return ""
	}
	lower := strings.ToLower(name)
	for _, m := range members {
		if strings.EqualFold(strings.TrimSpace(m.Name), lower) ||
			strings.EqualFold(strings.TrimSpace(m.DisplayName), lower) {
			return m.ID
		}
	}
	if len(lower) >= 2 {
		for _, m := range members {
			n, dn := strings.ToLower(m.Name), strings.ToLower(m.DisplayName)
			if (len(n) >= 2 && (strings.Contains(n, lower) || strings.Contains(lower, n))) ||
				(len(dn) >= 2 && (strings.Contains(dn, lower) || strings.Contains(lower, dn))) {
				return m.ID
			}
		}
	}
	return ""
}

var timeLayouts = []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"}

// parseUnix ISO 时间串转 Unix 秒；无法解析返回 0（不设置）。
func parseUnix(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	for _, layout := range timeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t.Unix()
		}
	}
	return 0
}

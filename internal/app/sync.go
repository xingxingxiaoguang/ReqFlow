// Package app 承载业务用例编排：只依赖 port 与 domain，不知道任何 infra 实现。
// 依赖经构造函数注入；进度以回调上报（由 httpgin 转为 SSE 事件）。
package app

import (
	"context"
	"fmt"
	"time"

	"reqflow/internal/domain/logic"
	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

// SyncProgress 同步进度上报。
type SyncProgress struct {
	Stage   string // meta | projects | workitems | done
	Message string
}

// SyncResult 一次增量同步的统计。
type SyncResult struct {
	Projects          int  `json:"projects"`
	WorkItems         int  `json:"workItems"`
	AddedProjects     int  `json:"addedProjects"`
	AddedWorkItems    int  `json:"addedWorkItems"`
	UpdatedWorkItems  int  `json:"updatedWorkItems"`
	ArchivedProjects  int  `json:"archivedProjects"`
	ArchivedWorkItems int  `json:"archivedWorkItems"`
	SemanticDisabled  bool `json:"semanticDisabled"`
}

// SyncService 增量同步用例：平台项目/工作项/元数据 → 本地缓存 + 向量。
// 三态同步：新增、更新（远端更新时间或内容变化）、软删除（平台侧已不存在）。
type SyncService struct {
	platform    port.PlatformClient
	embedder    port.Embedder
	projects    port.ProjectRepo
	workItems   port.WorkItemRepo
	meta        port.MetaRepo
	concurrency int           // 元数据拉取并发
	batchSize   int           // 工作项向量写入批大小
	batchDelay  time.Duration // 批间延迟（缓解 embedding 服务压力）
}

// NewSyncService 构造用例。
func NewSyncService(
	platform port.PlatformClient, embedder port.Embedder,
	projects port.ProjectRepo, workItems port.WorkItemRepo, meta port.MetaRepo,
	concurrency, batchSize int, batchDelay time.Duration,
) *SyncService {
	if concurrency <= 0 {
		concurrency = 3
	}
	if batchSize <= 0 {
		batchSize = 25
	}
	return &SyncService{
		platform: platform, embedder: embedder,
		projects: projects, workItems: workItems, meta: meta,
		concurrency: concurrency, batchSize: batchSize, batchDelay: batchDelay,
	}
}

// 向量写入侧的文档格式（查询侧必须对齐，否则相似度被稀释）。
const projectVectorDocFmt = "Project: %s\nDescription: %s"
const workItemVectorDocFmt = "Title: %s\nDescription: %s"

type localRef struct {
	a, b, remoteUpdated string // a/b：项目为名称/描述，工作项为标题/描述
	archived            bool
}

// Run 执行增量同步。report 在各阶段被调用；返回本次统计。
func (s *SyncService) Run(ctx context.Context, report func(SyncProgress)) (*SyncResult, error) {
	if report == nil {
		report = func(SyncProgress) {}
	}
	res := &SyncResult{SemanticDisabled: !s.embedder.Available()}

	report(SyncProgress{Stage: "projects", Message: "拉取平台项目列表…"})
	remoteProjects, err := s.platform.ListProjects(ctx)
	if err != nil {
		return nil, err
	}

	/* ---- 元数据（类型/状态/优先级，并发受限） + 类型组索引（kind 推导用） ---- */
	report(SyncProgress{Stage: "meta", Message: "同步元数据（类型/状态/优先级）…"})
	typeGroups, err := s.syncMetadata(ctx, remoteProjects)
	if err != nil {
		return nil, err
	}

	/* ---- 项目：新增 + 更新 + 软删除，写向量 ---- */
	report(SyncProgress{Stage: "projects", Message: "比对项目变更…"})
	localProjects, err := s.projects.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	localIdx := make(map[string]localRef, len(localProjects))
	activeLocal := 0
	for _, p := range localProjects {
		localIdx[p.ID] = localRef{a: p.Name, b: p.Description, remoteUpdated: p.RemoteUpdatedAt, archived: p.IsArchived}
		if !p.IsArchived {
			activeLocal++
		}
	}

	var projectUpserts []port.ProjectVector
	remoteIDs := make(map[string]bool, len(remoteProjects))
	var archivedProjectIDs []string
	for _, rp := range remoteProjects {
		remoteIDs[rp.ID] = true
		if old, ok := localIdx[rp.ID]; ok {
			if !logic.ProjectChanged(old.a, old.b, old.remoteUpdated, old.archived, rp.Name, rp.Description, rp.UpdatedAt) {
				continue
			}
		} else {
			res.AddedProjects++
		}
		projectUpserts = append(projectUpserts, port.ProjectVector{
			Project: model.Project{
				ID: rp.ID, Name: rp.Name, Description: rp.Description, RemoteUpdatedAt: rp.UpdatedAt,
			},
		})
	}
	for _, lp := range localProjects {
		if !remoteIDs[lp.ID] && !lp.IsArchived {
			archivedProjectIDs = append(archivedProjectIDs, lp.ID)
		}
	}
	res.ArchivedProjects = len(archivedProjectIDs)
	res.Projects = max0(activeLocal + res.AddedProjects - res.ArchivedProjects)

	if err := s.upsertProjects(ctx, projectUpserts); err != nil {
		return nil, err
	}
	if err := s.projects.Archive(ctx, archivedProjectIDs); err != nil {
		return nil, err
	}

	/* ---- 工作项：逐项目拉取、比对，批量写向量 ---- */
	localItems, err := s.workItems.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	localItemIdx := make(map[string]localRef, len(localItems))
	activeItems := 0
	for _, w := range localItems {
		localItemIdx[w.ID] = localRef{a: w.Title, b: w.Description, remoteUpdated: w.RemoteUpdatedAt, archived: w.IsArchived}
		if !w.IsArchived {
			activeItems++
		}
	}

	var itemUpserts []port.WorkItemVector
	remoteItemIDs := make(map[string]bool)
	var archivedItemIDs []string
	for i, proj := range remoteProjects {
		report(SyncProgress{Stage: "workitems", Message: "拉取工作项…"})
		items, err := s.platform.ListWorkItems(ctx, proj.ID)
		if err != nil {
			report(SyncProgress{Stage: "workitems", Message: "项目 " + proj.Name + " 工作项拉取失败，已跳过"})
			continue
		}
		for _, it := range items {
			remoteItemIDs[it.ID] = true
			old, exists := localItemIdx[it.ID]
			if exists {
				if !logic.ItemChanged(old.a, old.b, old.remoteUpdated, old.archived, it.Title, it.Description, it.UpdatedAt) {
					continue
				}
				res.UpdatedWorkItems++
			} else {
				res.AddedWorkItems++
			}
			itemUpserts = append(itemUpserts, port.WorkItemVector{
				WorkItem: workItemModel(it, proj.ID, typeGroups[proj.ID][it.TypeID]),
			})
		}
		if i == len(remoteProjects)-1 {
			break
		}
	}
	for _, lw := range localItems {
		if !remoteItemIDs[lw.ID] && !lw.IsArchived {
			archivedItemIDs = append(archivedItemIDs, lw.ID)
		}
	}
	res.ArchivedWorkItems = len(archivedItemIDs)
	res.WorkItems = max0(activeItems + res.AddedWorkItems - res.ArchivedWorkItems)

	if err := s.upsertWorkItems(ctx, itemUpserts, report); err != nil {
		return nil, err
	}
	if err := s.workItems.Archive(ctx, archivedItemIDs); err != nil {
		return nil, err
	}

	report(SyncProgress{Stage: "done", Message: "同步完成"})
	return res, nil
}

/* ---- 元数据同步：并发受限拉取并入库，返回 projectID → typeID → 类型组 ---- */

func (s *SyncService) syncMetadata(ctx context.Context, projects []port.PlatformProject) (map[string]map[string]string, error) {
	typeGroups := make(map[string]map[string]string, len(projects))
	sem := make(chan struct{}, s.concurrency)
	type jobRes struct {
		projectID string
		groups    map[string]string
		types     []model.MetaType
		states    []model.MetaState
		prios     []model.MetaPriority
		err       error
	}
	results := make([]jobRes, len(projects))
	for i := range projects {
		i, proj := i, projects[i]
		sem <- struct{}{}
		go func() {
			defer func() { <-sem }()
			res := jobRes{projectID: proj.ID, groups: map[string]string{}}
			types, err := s.platform.ListTypes(ctx, proj.ID)
			if err != nil {
				res.err = err
				results[i] = res
				return
			}
			for _, t := range types {
				res.types = append(res.types, t)
				res.groups[t.ID] = t.Group
				states, err := s.platform.ListStates(ctx, proj.ID, t.ID)
				if err != nil {
					continue // 单类型状态失败不阻断整体
				}
				res.states = append(res.states, states...)
			}
			prios, err := s.platform.ListPriorities(ctx, proj.ID)
			if err == nil {
				res.prios = prios
			}
			results[i] = res
		}()
	}
	for _, r := range results {
		if r.err != nil {
			return nil, r.err
		}
		typeGroups[r.projectID] = r.groups
		if err := s.meta.UpsertTypes(ctx, r.types); err != nil {
			return nil, err
		}
		if err := s.meta.UpsertStates(ctx, r.states); err != nil {
			return nil, err
		}
		if err := s.meta.UpsertPriorities(ctx, r.prios); err != nil {
			return nil, err
		}
	}
	return typeGroups, nil
}

/* ---- 向量化 + 批量入库 ---- */

func (s *SyncService) upsertProjects(ctx context.Context, items []port.ProjectVector) error {
	if len(items) == 0 {
		return nil
	}
	if s.embedder.Available() {
		docs := make([]string, len(items))
		for i, it := range items {
			docs[i] = projectDoc(it.Name, it.Description)
		}
		vecs, err := s.embedder.Generate(ctx, docs)
		if err != nil {
			return err
		}
		for i := range items {
			items[i].Embedding = vecs[i]
		}
	}
	return s.projects.UpsertWithVectors(ctx, items)
}

func (s *SyncService) upsertWorkItems(ctx context.Context, items []port.WorkItemVector, report func(SyncProgress)) error {
	for len(items) > 0 {
		n := s.batchSize
		if n > len(items) {
			n = len(items)
		}
		batch := items[:n]
		items = items[n:]
		if s.embedder.Available() {
			docs := make([]string, len(batch))
			for i, it := range batch {
				docs[i] = workItemDoc(it.Title, it.Description)
			}
			vecs, err := s.embedder.Generate(ctx, docs)
			if err != nil {
				return err
			}
			for i := range batch {
				batch[i].Embedding = vecs[i]
			}
		}
		if err := s.workItems.UpsertWithVectors(ctx, batch); err != nil {
			return err
		}
		report(SyncProgress{Stage: "workitems", Message: "已写入向量批次"})
		if len(items) > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(s.batchDelay):
			}
		}
	}
	return nil
}

func projectDoc(name, desc string) string {
	return fmt.Sprintf(projectVectorDocFmt, name, desc)
}

func workItemDoc(title, desc string) string {
	return fmt.Sprintf(workItemVectorDocFmt, title, desc)
}

// workItemModel 平台 DTO → 领域模型；kind 由类型组索引推导，projectID 缺失时用列表上下文兜底。
func workItemModel(it port.PlatformWorkItem, listProjectID, kind string) model.WorkItem {
	pid := it.ProjectID
	if pid == "" {
		pid = listProjectID
	}
	return model.WorkItem{
		ID: it.ID, ProjectID: pid, Identifier: it.Identifier,
		Title: it.Title, Description: it.Description,
		Kind: kind, TypeID: it.TypeID, RemoteUpdatedAt: it.UpdatedAt,
	}
}

func max0(v int) int {
	if v < 0 {
		return 0
	}
	return v
}

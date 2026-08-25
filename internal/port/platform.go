package port

import (
	"context"

	"reqflow/internal/domain/model"
)

/* ---- 协作平台 DTO（平台无关形状） ---- */

type PlatformProject struct {
	ID          string
	Name        string
	Description string
	UpdatedAt   string // 平台侧更新时间原样字符串
}

type PlatformWorkItem struct {
	ID          string
	ProjectID   string
	Identifier  string
	Title       string
	Description string
	TypeID      string
	UpdatedAt   string
}

type PlatformMember struct {
	ID          string
	Name        string
	DisplayName string
	Email       string
}

type CreateProjectInput struct {
	Name        string
	Description string
	Identifier  string // 平台内唯一标识
}

type CreateWorkItemInput struct {
	ProjectID         string
	TypeID            string
	Title             string
	Description       string
	PriorityID        string
	StateID           string
	AssigneeID        string
	StartAt           int64 // Unix 秒，0 表示不设置
	EndAt             int64
	EstimatedWorkload float64 // 已按平台工时单位换算
}

type CreatedWorkItem struct {
	ID         string
	Identifier string
}

// PlatformClient 协作平台客户端。
// 第一波只有 PingCode 实现；接入新平台（Jira 等）= 新增 infra 实现，本契约不动。
//
// 已知限制（PingCode 6.13.5）：平台无工作项关联 API，第二波 bug↔需求关联
// 将以「bug 描述内标注需求编号」落地，parent_id 为备选方案。
type PlatformClient interface {
	Name() string
	TestConnection(ctx context.Context) error

	ListProjects(ctx context.Context) ([]PlatformProject, error)
	ListWorkItems(ctx context.Context, projectID string) ([]PlatformWorkItem, error)
	ListProjectMembers(ctx context.Context, projectID string) ([]PlatformMember, error)

	CreateProject(ctx context.Context, in CreateProjectInput) (*PlatformProject, error)
	CreateWorkItem(ctx context.Context, in CreateWorkItemInput) (*CreatedWorkItem, error)

	ListTypes(ctx context.Context, projectID string) ([]model.MetaType, error)
	ListStates(ctx context.Context, projectID, typeID string) ([]model.MetaState, error)
	ListPriorities(ctx context.Context, projectID string) ([]model.MetaPriority, error)
}

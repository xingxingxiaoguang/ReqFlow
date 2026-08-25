package pingcode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"reqflow/internal/port"
)

/* ---- 工作项 ---- */

const (
	workItemPageSize  = 100
	workItemMaxPages  = 100 // 分页参数异常时的保险上限
)

// ListWorkItems 拉取项目全部工作项（自动分页）。
// 双停止条件：本页返回不足一页，或已达 total；空页亦停——避免 total 缺失时死循环。
func (c *Client) ListWorkItems(ctx context.Context, projectID string) ([]port.PlatformWorkItem, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	var all []port.PlatformWorkItem
	for page := 0; page < workItemMaxPages; page++ {
		raw := c.apiBase() + "/project/work_items?project_id=" + url.QueryEscape(projectID) +
			"&page_size=" + strconv.Itoa(workItemPageSize) + "&page_index=" + strconv.Itoa(page)
		body, err := c.doGet(ctx, raw, token)
		if err != nil {
			return nil, err
		}
		var resp struct {
			Values []map[string]any `json:"values"`
			Total  *int             `json:"total"`
			TotalC *int             `json:"total_count"`
		}
		_ = json.Unmarshal(body, &resp)

		items := resp.Values
		for _, m := range items {
			if getStr(m, "id") == "" {
				continue
			}
			// 类型 ID 兼容扁平 type_id 与嵌套 type.id 两种结构
			typeID := getStr(m, "type_id")
			if typeID == "" {
				if tm := asMap(m["type"]); tm != nil {
					typeID = getStr(tm, "id")
				}
			}
			all = append(all, port.PlatformWorkItem{
				ID:          getStr(m, "id"),
				ProjectID:   getStr(m, "project_id"),
				Identifier:  getStr(m, "identifier"),
				Title:       getStr(m, "title", "name"),
				Description: getStr(m, "description"),
				TypeID:      typeID,
				UpdatedAt:   getUpdated(m, "updated_at", "updated_at_at"),
			})
		}

		total := 0
		if resp.Total != nil {
			total = *resp.Total
		} else if resp.TotalC != nil {
			total = *resp.TotalC
		}
		if len(items) < workItemPageSize || (total > 0 && len(all) >= total) || len(items) == 0 {
			break
		}
	}
	return all, nil
}

// CreateWorkItem 创建单个工作项（仅组装非空字段，避免清空平台侧默认值）。
func (c *Client) CreateWorkItem(ctx context.Context, in port.CreateWorkItemInput) (*port.CreatedWorkItem, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{
		"project_id": in.ProjectID,
		"type_id":    in.TypeID,
		"title":      in.Title,
	}
	if in.Description != "" {
		payload["description"] = in.Description
	}
	if in.PriorityID != "" {
		payload["priority_id"] = in.PriorityID
	}
	if in.StateID != "" {
		payload["state_id"] = in.StateID
	}
	if in.AssigneeID != "" {
		payload["assignee_id"] = in.AssigneeID
	}
	if in.StartAt > 0 {
		payload["start_at"] = in.StartAt
	}
	if in.EndAt > 0 {
		payload["end_at"] = in.EndAt
	}
	if in.EstimatedWorkload > 0 {
		payload["estimated_workload"] = in.EstimatedWorkload
	}

	body, err := c.doPost(ctx, c.apiBase()+"/project/work_items", payload, token)
	if err != nil {
		return nil, err
	}
	m := asMap(parseJSON(body))
	if m == nil || getStr(m, "id") == "" {
		return nil, fmt.Errorf("创建工作项响应异常: %s", truncate(string(body), 200))
	}
	return &port.CreatedWorkItem{ID: getStr(m, "id"), Identifier: getStr(m, "identifier")}, nil
}

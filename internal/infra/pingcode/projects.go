package pingcode

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"

	"reqflow/internal/domain/model"
	"reqflow/internal/port"
)

/* ---- 动态 JSON 取值辅助（PingCode 响应字段存在多种形状，防御式读取） ---- */

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func getStr(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch s := v.(type) {
			case string:
				return s
			case float64:
				return strconv.FormatFloat(s, 'f', -1, 64)
			case bool:
				return strconv.FormatBool(s)
			}
		}
	}
	return ""
}

// getUpdated 提取更新时间：字符串原样返回；数字按 Unix 毫秒规范化为字符串。
func getUpdated(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch s := v.(type) {
			case string:
				return s
			case float64:
				return strconv.FormatInt(int64(s), 10)
			}
		}
	}
	return ""
}

func valuesOf(body []byte) []any {
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil
	}
	switch r := root.(type) {
	case []any:
		return r
	case map[string]any:
		if vs, ok := r["values"].([]any); ok {
			return vs
		}
	}
	return nil
}

/* ---- 项目 ---- */

func (c *Client) ListProjects(ctx context.Context) ([]port.PlatformProject, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	body, err := c.doGet(ctx, c.apiBase()+"/project/projects?page_size=100", token)
	if err != nil {
		return nil, err
	}
	var out []port.PlatformProject
	for _, v := range valuesOf(body) {
		m := asMap(v)
		if m == nil || getStr(m, "id") == "" {
			continue
		}
		out = append(out, port.PlatformProject{
			ID:          getStr(m, "id"),
			Name:        getStr(m, "name"),
			Description: getStr(m, "description"),
			UpdatedAt:   getUpdated(m, "updated_at", "updated_at_at"),
		})
	}
	return out, nil
}

func (c *Client) ListProjectMembers(ctx context.Context, projectID string) ([]port.PlatformMember, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	body, err := c.doGet(ctx,
		c.apiBase()+"/project/projects/"+url.PathEscape(projectID)+"/members?page_size=100", token)
	if err != nil {
		return nil, err
	}
	var out []port.PlatformMember
	for _, v := range valuesOf(body) {
		m := asMap(v)
		if m == nil {
			continue
		}
		out = append(out, port.PlatformMember{
			ID: getStr(m, "id", "user_id"),
			Name: getStr(m, "name"),
			DisplayName: getStr(m, "display_name"),
			Email:       getStr(m, "email"),
		})
	}
	return out, nil
}

// CreateProject 创建项目并尽力补充成员：企业授权无用户身份时取组织目录第一个用户；
// 成员添加失败只记录不阻断（私有项目无成员将对所有人不可见，故必须尝试）。
func (c *Client) CreateProject(ctx context.Context, in port.CreateProjectInput) (*port.PlatformProject, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	body, err := c.doPost(ctx, c.apiBase()+"/project/projects", map[string]any{
		"name":       in.Name,
		"type":       "scrum",
		"identifier": in.Identifier,
	}, token)
	if err != nil {
		return nil, err
	}
	created := asMap(parseJSON(body))
	if created == nil || getStr(created, "id") == "" {
		return nil, fmt.Errorf("创建项目响应异常: %s", truncate(string(body), 200))
	}
	c.tryEnsureMember(ctx, token, getStr(created, "id"))

	return &port.PlatformProject{
		ID:          getStr(created, "id"),
		Name:        getStr(created, "name"),
		Description: getStr(created, "description"),
	}, nil
}

func (c *Client) tryEnsureMember(ctx context.Context, token, projectID string) {
	defer func() { recover() }() // 尽力而为，不影响主流程
	var memberUserID string
	if users, err := c.orgUsers(ctx, token); err == nil && len(users) > 0 {
		memberUserID = getStr(users[0], "id")
	}
	if memberUserID == "" {
		return
	}
	_, _ = c.doPost(ctx, c.apiBase()+"/pjm/projects/"+url.PathEscape(projectID)+"/members",
		map[string]any{"user_id": memberUserID}, token)
}

func (c *Client) orgUsers(ctx context.Context, token string) ([]map[string]any, error) {
	body, err := c.doGet(ctx, c.apiBase()+"/directory/users?page_size=100", token)
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for _, v := range valuesOf(body) {
		if m := asMap(v); m != nil {
			out = append(out, m)
		}
	}
	return out, nil
}

func parseJSON(body []byte) any {
	var root any
	_ = json.Unmarshal(body, &root)
	return root
}

/* ---- 元数据 ---- */

func (c *Client) ListTypes(ctx context.Context, projectID string) ([]model.MetaType, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	body, err := c.doGet(ctx,
		c.apiBase()+"/project/work_item/types?project_id="+url.QueryEscape(projectID), token)
	if err != nil {
		return nil, err
	}
	var out []model.MetaType
	for _, v := range valuesOf(body) {
		m := asMap(v)
		if m == nil {
			continue
		}
		out = append(out, model.MetaType{
			ID: getStr(m, "id"), ProjectID: projectID,
			Name: getStr(m, "name"), Group: getStr(m, "group"),
		})
	}
	return out, nil
}

func (c *Client) ListStates(ctx context.Context, projectID, typeID string) ([]model.MetaState, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	body, err := c.doGet(ctx, c.apiBase()+"/project/work_item/states?project_id="+
		url.QueryEscape(projectID)+"&work_item_type_id="+url.QueryEscape(typeID), token)
	if err != nil {
		return nil, err
	}
	var out []model.MetaState
	for _, v := range valuesOf(body) {
		m := asMap(v)
		if m == nil {
			continue
		}
		out = append(out, model.MetaState{
			ID: getStr(m, "id"), ProjectID: projectID, WorkItemTypeID: typeID,
			Name: getStr(m, "name"), Type: getStr(m, "type"), Color: getStr(m, "color"),
		})
	}
	return out, nil
}

func (c *Client) ListPriorities(ctx context.Context, projectID string) ([]model.MetaPriority, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	body, err := c.doGet(ctx,
		c.apiBase()+"/project/work_item/priorities?project_id="+url.QueryEscape(projectID), token)
	if err != nil {
		return nil, err
	}
	var out []model.MetaPriority
	for _, v := range valuesOf(body) {
		m := asMap(v)
		if m == nil {
			continue
		}
		out = append(out, model.MetaPriority{
			ID: getStr(m, "id"), ProjectID: projectID, Name: getStr(m, "name"),
		})
	}
	return out, nil
}

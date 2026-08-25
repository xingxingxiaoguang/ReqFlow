// Package pingcode 实现 port.PlatformClient（PingCode 开放 API，验证基准 6.13.5）。
// 授权：第一波为企业令牌 client_credentials；authorization_code（OAuth 用户授权）
// 预留 TokenSource 扩展点，接入时仅需替换令牌来源，其余调用不变。
package pingcode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Options 客户端参数（由 cmd 从配置注入）。
type Options struct {
	Host         string // 如 https://open.pingcode.com
	ClientID     string
	ClientSecret string
	HTTPTimeout  time.Duration
}

// Client PingCode 开放 API 客户端。
type Client struct {
	opt      Options
	http     *http.Client
	mu       sync.Mutex
	token    string
	tokenExp time.Time
}

// New 构造客户端（未建立连接，首个请求时才取令牌）。
func New(opt Options) *Client {
	if opt.HTTPTimeout <= 0 {
		opt.HTTPTimeout = 30 * time.Second
	}
	return &Client{opt: opt, http: &http.Client{Timeout: opt.HTTPTimeout}}
}

func (c *Client) Name() string { return "pingcode" }

func (c *Client) apiBase() string { return strings.TrimSuffix(c.opt.Host, "/") + "/v1" }

/* ---- 令牌管理 ---- */

const tokenRefreshBuffer = 5 * time.Minute

// token 获取（缓存）企业访问令牌；过期前 5 分钟主动重取。
func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.tokenExp.Add(-tokenRefreshBuffer)) {
		return c.token, nil
	}
	q := url.Values{}
	q.Set("grant_type", "client_credentials")
	q.Set("client_id", c.opt.ClientID)
	q.Set("client_secret", c.opt.ClientSecret)

	body, err := c.doGet(ctx, c.opt.Host+"/v1/auth/token?"+q.Encode(), "")
	if err != nil {
		return "", fmt.Errorf("获取企业令牌失败: %w", err)
	}
	var tok struct {
		AccessToken string  `json:"access_token"`
		ExpiresIn   float64 `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tok); err != nil || tok.AccessToken == "" {
		return "", fmt.Errorf("企业令牌响应异常: %s", truncate(string(body), 200))
	}
	expSec := tok.ExpiresIn
	if expSec <= 0 || expSec > 40*24*3600 { // 异常值回退/裁剪（对齐 PingCraft 经验值）
		expSec = 30 * 24 * 3600
	}
	c.token = tok.AccessToken
	c.tokenExp = time.Now().Add(time.Duration(expSec) * time.Second)
	return c.token, nil
}

// TestConnection 取一次令牌即视为连通。
func (c *Client) TestConnection(ctx context.Context) error {
	_, err := c.accessToken(ctx)
	return err
}

/* ---- 基础请求 ---- */

const maxRetries = 2

func (c *Client) doGet(ctx context.Context, rawURL, token string) ([]byte, error) {
	return c.withRetry(ctx, func() ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return nil, err
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		return c.exec(req)
	})
}

func (c *Client) doPost(ctx context.Context, rawURL string, payload any, token string) ([]byte, error) {
	return c.withRetry(ctx, func() ([]byte, error) {
		var rd io.Reader
		if payload != nil {
			buf, err := json.Marshal(payload)
			if err != nil {
				return nil, err
			}
			rd = strings.NewReader(string(buf))
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, rd)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		return c.exec(req)
	})
}

func (c *Client) exec(req *http.Request) ([]byte, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, &APIError{Status: resp.StatusCode, Body: truncate(string(body), 300)}
	}
	return body, nil
}

// withRetry 网络错误与 5xx/429 退避重试（创建类请求由调用方保证幂等场景）。
func (c *Client) withRetry(ctx context.Context, fn func() ([]byte, error)) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * 500 * time.Millisecond):
			}
		}
		body, err := fn()
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !retryable(err) || ctx.Err() != nil {
			return nil, err
		}
	}
	return nil, lastErr
}

func retryable(err error) bool {
	ae, ok := err.(*APIError)
	if !ok {
		return true // 网络层错误
	}
	return ae.Status >= 500 || ae.Status == http.StatusTooManyRequests
}

// APIError 平台返回的非 2xx 响应。
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("pingcode api http %d: %s", e.Status, e.Body)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

package qdgj

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	MobileUserAgent = "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Mobile Safari/537.36"
	maxResponseSize = 4 << 20
)

var (
	ErrNotAuthenticated       = errors.New("尚未登录或会话缺少用户及令牌")
	ErrCaptchaRequired        = errors.New("登录需要验证码，请提供 CaptchaSolver")
	ErrTaskNotFound           = errors.New("查询不到指定签到任务或批次")
	ErrLoginPublicKeyNotFound = errors.New("页面资源中未找到登录 RSA 公钥")
)

// Client 封装初始化配置及会话状态，通过 Login 或 LoadSession 建立身份。
// 完成登录或会话加载后支持并发查询。
type Client struct {
	base          *url.URL
	http          *http.Client
	config        Config
	mu            sync.RWMutex
	user          User
	authorization string
	loginResponse json.RawMessage
}

type Meta struct {
	Success    bool   `json:"success"`
	StatusCode int    `json:"statusCode"`
	Message    string `json:"message"`
}

// Response 保存业务状态、响应数据和完整的原始 JSON。
type Response struct {
	Code        int             `json:"code"`
	Meta        Meta            `json:"meta"`
	Data        json.RawMessage `json:"data"`
	RawResponse json.RawMessage `json:"-"`
}

// APIError 表示 HTTP 或业务失败，可通过 errors.As 获取状态和错误消息。
type APIError struct {
	Operation  string
	HTTPStatus int
	Code       int
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s失败（HTTP=%d, code=%d, statusCode=%d）：%s", e.Operation, e.HTTPStatus, e.Code, e.StatusCode, e.Message)
}

// NewClient 合并初始化配置并创建 HTTP 客户端和独立的 CookieJar。
// 地址、账号、密码及会话路径的空字段使用对应的环境变量。
func NewClient(config Config) (*Client, error) {
	config = config.withDefaults()
	if config.BaseURL == "" {
		return nil, errors.New("请设置 EASYCHECK_BASE_URL 或 Config.BaseURL")
	}
	base, err := parseBaseURL(config.BaseURL)
	if err != nil {
		return nil, err
	}
	if config.Timeout < 0 {
		return nil, errors.New("Timeout 不能小于零")
	}
	client := http.Client{}
	if config.HTTPClient != nil {
		client = *config.HTTPClient
	}
	if config.Timeout > 0 {
		client.Timeout = config.Timeout
	}
	if client.Timeout == 0 {
		client.Timeout = defaultTimeout
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	client.Jar = jar
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	config.HTTPClient = &client
	return &Client{base: base, http: &client, config: config}, nil
}

func parseBaseURL(value string) (*url.URL, error) {
	u, err := url.Parse(value)
	if err != nil || u == nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, errors.New("BaseURL 必须是无账号、查询参数及片段的 HTTP(S) 根地址")
	}
	if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}
	return u, nil
}

func (c *Client) request(ctx context.Context, operation, method, path string, body []byte, contentType string, authenticated bool, headers http.Header) ([]byte, error) {
	u := c.base.ResolveReference(&url.URL{Path: path})
	if path == "captchaServlet" {
		u.RawQuery = "v=" + strconv.FormatInt(time.Now().UnixMilli(), 10)
	}
	if authenticated {
		u.RawQuery = "_t=" + strconv.FormatInt(time.Now().Unix(), 10)
	}
	return c.requestURL(ctx, operation, method, u, body, contentType, authenticated, headers)
}

func (c *Client) requestURL(ctx context.Context, operation, method string, u *url.URL, body []byte, contentType string, authenticated bool, headers http.Header) ([]byte, error) {
	// 请求体使用一次性流，提交操作发送一次。
	req, err := http.NewRequestWithContext(ctx, method, u.String(), nil)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Body = io.NopCloser(bytes.NewReader(body))
		req.ContentLength = int64(len(body))
	}
	req.Header.Set("User-Agent", c.config.UserAgent)
	req.Header.Set("Sec-CH-UA-Mobile", "?1")
	req.Header.Set("Sec-CH-UA-Platform", `"Android"`)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Referer", c.base.String())
	if u.Path != c.base.Path {
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if method == http.MethodPost {
		req.Header.Set("Origin", c.base.Scheme+"://"+c.base.Host)
	}
	if authenticated {
		c.mu.RLock()
		user, token := c.user, c.authorization
		c.mu.RUnlock()
		if user.UserID == "" || token == "" {
			return nil, ErrNotAuthenticated
		}
		req.Header.Set("Authorization", token)
		req.Header.Set("loginUserId", user.UserID)
		req.Header.Set("loginUserOrgId", user.OrgID)
	}
	for name, values := range headers {
		req.Header[name] = append([]string(nil), values...)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s：%w", operation, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
	if err != nil {
		return nil, fmt.Errorf("读取%s响应：%w", operation, err)
	}
	if len(raw) > maxResponseSize {
		return nil, errors.New("接口响应超过 4 MiB")
	}
	if resp.StatusCode != http.StatusOK {
		var parsed Response
		_ = json.Unmarshal(raw, &parsed)
		return nil, &APIError{Operation: operation, HTTPStatus: resp.StatusCode, Code: parsed.Code, StatusCode: parsed.Meta.StatusCode, Message: parsed.Meta.Message}
	}
	return raw, nil
}

func decodeResponse(raw []byte, operation string, login bool) (*Response, error) {
	var result Response
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, fmt.Errorf("解析%s响应：%w", operation, err)
	}
	ok := result.Code == 200 && (result.Meta.StatusCode == 0 || result.Meta.StatusCode == 200)
	if login {
		ok = result.Meta.StatusCode == 200
	}
	if !ok {
		return nil, &APIError{Operation: operation, HTTPStatus: 200, Code: result.Code, StatusCode: result.Meta.StatusCode, Message: result.Meta.Message}
	}
	result.RawResponse = append(json.RawMessage(nil), raw...)
	return &result, nil
}

func (c *Client) postJSON(ctx context.Context, operation, path string, body any) (*Response, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	raw, err := c.request(ctx, operation, http.MethodPost, path, encoded, "application/json;charset=UTF-8", true, nil)
	if err != nil {
		return nil, err
	}
	return decodeResponse(raw, operation, false)
}

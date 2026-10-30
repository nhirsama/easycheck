package qdgj

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"time"
)

// Session 保存站点地址、用户代理、身份凭据、Cookie 和登录响应。
type Session struct {
	BaseURL       string          `json:"base_url"`
	SavedAt       time.Time       `json:"saved_at"`
	UserAgent     string          `json:"user_agent"`
	Authorization string          `json:"authorization,omitempty"`
	Cookies       []*http.Cookie  `json:"cookies"`
	LoginResponse json.RawMessage `json:"login_response"`
}

type User struct {
	UserID      string `json:"userId"`
	OrgID       string `json:"orgId"`
	UserType    string `json:"userType"`
	TokenID     string `json:"tokenId"`
	AccessToken string `json:"accessToken"`
}

func (s *Session) User() (User, error) {
	if s == nil {
		return User{}, ErrNotAuthenticated
	}
	var response struct {
		Data User `json:"data"`
	}
	if err := json.Unmarshal(s.LoginResponse, &response); err != nil {
		return User{}, fmt.Errorf("解析会话用户：%w", err)
	}
	if response.Data.UserID == "" {
		return User{}, ErrNotAuthenticated
	}
	return response.Data, nil
}

func (s *Session) validate() error {
	if s == nil {
		return ErrNotAuthenticated
	}
	if _, err := parseBaseURL(s.BaseURL); err != nil {
		return err
	}
	if s.UserAgent == "" {
		return errors.New("会话缺少手机 User-Agent")
	}
	user, err := s.User()
	if err != nil {
		return err
	}
	if s.Authorization == "" && user.TokenID == "" && user.AccessToken == "" {
		return ErrNotAuthenticated
	}
	return nil
}

func LoadSession(path string) (*Session, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取会话：%w", err)
	}
	var saved Session
	if err := json.Unmarshal(raw, &saved); err != nil {
		return nil, fmt.Errorf("解析会话：%w", err)
	}
	if err := saved.validate(); err != nil {
		return nil, err
	}
	return &saved, nil
}

// Save 以 0600 权限原子替换指定文件。保存目录须已存在。
func (s *Session) Save(path string) error {
	if err := s.validate(); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return WritePrivateFile(path, append(raw, '\n'))
}

// WritePrivateFile 用 0600 权限原子写入会话、验证码或查询响应等本地文件。
func WritePrivateFile(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".easycheck-session-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

// NewClientFromSession 根据配置创建客户端，并恢复传入的会话状态。
// BaseURL 来自 Config.BaseURL 或 EASYCHECK_BASE_URL，必须与保存的站点一致。
func NewClientFromSession(saved *Session, config Config) (*Client, error) {
	if err := saved.validate(); err != nil {
		return nil, err
	}
	if config.UserAgent == "" {
		config.UserAgent = saved.UserAgent
	}
	c, err := NewClient(config)
	if err != nil {
		return nil, err
	}
	if err := c.restoreSession(saved); err != nil {
		return nil, err
	}
	return c, nil
}

// LoadSession 读取配置的 SessionFile，验证站点并替换当前对象的会话状态。
// 加载失败时保留原会话，加载完成后支持并发查询。
func (c *Client) LoadSession() error {
	saved, err := LoadSession(c.config.SessionFile)
	if err != nil {
		return err
	}
	return c.restoreSession(saved)
}

// SaveSession 将当前对象的会话快照保存到配置的 SessionFile。
func (c *Client) SaveSession() error {
	saved, err := c.Session()
	if err != nil {
		return err
	}
	return saved.Save(c.config.SessionFile)
}

func (c *Client) restoreSession(saved *Session) error {
	if err := saved.validate(); err != nil {
		return err
	}
	sessionBase, _ := parseBaseURL(saved.BaseURL)
	if c.base.String() != sessionBase.String() {
		return errors.New("不能将已保存会话用于不同的 BaseURL")
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		return err
	}
	jar.SetCookies(c.base, saved.Cookies)
	user, _ := saved.User()
	token := saved.Authorization
	if token == "" {
		token = user.TokenID
	}
	if token == "" {
		token = user.AccessToken
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.http.Jar = jar
	c.user, c.authorization = user, token
	c.loginResponse = append(json.RawMessage(nil), saved.LoginResponse...)
	return nil
}

// Session 获取当前会话的独立快照，包括最近响应更新后的 Cookie。
func (c *Client) Session() (*Session, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.user.UserID == "" || c.authorization == "" {
		return nil, ErrNotAuthenticated
	}
	return &Session{BaseURL: c.base.String(), SavedAt: time.Now().UTC(), UserAgent: c.config.UserAgent,
		Authorization: c.authorization, Cookies: c.http.Jar.Cookies(c.base), LoginResponse: append(json.RawMessage(nil), c.loginResponse...)}, nil
}

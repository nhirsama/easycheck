package qdgj

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// CaptchaSolver 在需要验证码时调用；image 是同一 Cookie 会话取得的图片。
type CaptchaSolver func(ctx context.Context, image []byte) (string, error)

type LoginOptions struct {
	Username      string
	Password      string
	CaptchaSolver CaptchaSolver
}

// CaptchaRequired 先访问登录入口建立 Cookie，再检查验证码开关，不提交登录。
func (c *Client) CaptchaRequired(ctx context.Context) (bool, error) {
	if _, err := c.request(ctx, "访问登录入口", http.MethodGet, "", nil, "", false, nil); err != nil {
		return false, err
	}
	raw, err := c.request(ctx, "检查验证码", http.MethodGet, "ng/captcha/status", nil, "", false, http.Header{"Appid": {systemID}})
	if err != nil {
		return false, err
	}
	response, err := decodeResponse(raw, "检查验证码", true)
	if err != nil {
		return false, err
	}
	var required bool
	if err := json.Unmarshal(response.Data, &required); err != nil || bytes.Equal(bytes.TrimSpace(response.Data), []byte("null")) || len(response.Data) == 0 {
		return false, errors.New("验证码状态的 data 字段不是布尔值")
	}
	return required, nil
}

func (c *Client) FetchCaptcha(ctx context.Context) ([]byte, error) {
	raw, err := c.request(ctx, "下载验证码", http.MethodGet, "captchaServlet", nil, "", false, nil)
	if err != nil {
		return nil, err
	}
	if !strings.HasPrefix(http.DetectContentType(raw), "image/") {
		return nil, errors.New("验证码接口没有返回图片")
	}
	return raw, nil
}

// Login 完成入口、验证码和一次登录提交，成功后更新当前客户端并返回会话快照。
// 验证码打开但未提供回调时返回 ErrCaptchaRequired，不提交登录。
func (c *Client) Login(ctx context.Context, options LoginOptions) (*Session, error) {
	if err := validateCredentials(options.Username, options.Password); err != nil {
		return nil, err
	}
	// 先验证本地配置，避免缺少或无效公钥时仍访问站点。
	if _, err := parseLoginPublicKey(c.loginPublicKey); err != nil {
		return nil, err
	}
	required, err := c.CaptchaRequired(ctx)
	if err != nil {
		return nil, err
	}
	captcha := ""
	if required {
		if options.CaptchaSolver == nil {
			return nil, ErrCaptchaRequired
		}
		image, err := c.FetchCaptcha(ctx)
		if err != nil {
			return nil, err
		}
		captcha, err = options.CaptchaSolver(ctx, image)
		if err != nil {
			return nil, fmt.Errorf("获取验证码输入：%w", err)
		}
		captcha = strings.TrimSpace(captcha)
		if captcha == "" {
			return nil, errors.New("验证码不能为空")
		}
	}
	form, err := makeLoginForm(c.loginPublicKey, options.Username, options.Password, captcha, time.Now())
	if err != nil {
		return nil, err
	}
	raw, err := c.request(ctx, "登录", http.MethodPost, "login", []byte(form.Encode()), "application/x-www-form-urlencoded", false, nil)
	if err != nil {
		return nil, err
	}
	response, err := decodeResponse(raw, "登录", true)
	if err != nil {
		return nil, err
	}
	var user User
	if err := json.Unmarshal(response.Data, &user); err != nil || user.UserID == "" {
		return nil, errors.New("登录接口报告成功，但没有返回有效的用户数据")
	}
	// 当前网关使用 tokenId；accessToken 兼容另一网关版本。
	token := user.TokenID
	if token == "" {
		token = user.AccessToken
	}
	if token == "" {
		return nil, errors.New("登录成功响应缺少 tokenId/accessToken")
	}
	c.mu.Lock()
	c.user, c.authorization = user, token
	c.loginResponse = append(json.RawMessage(nil), raw...)
	c.mu.Unlock()
	return c.Session()
}

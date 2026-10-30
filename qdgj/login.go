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

// CaptchaRequired 访问登录入口建立 Cookie，并返回验证码开关状态。
func (c *Client) CaptchaRequired(ctx context.Context) (bool, error) {
	if _, err := c.request(ctx, "访问登录入口", http.MethodGet, "", nil, "", false, nil); err != nil {
		return false, err
	}
	return c.captchaStatus(ctx)
}

func (c *Client) captchaStatus(ctx context.Context) (bool, error) {
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

// Login 使用配置账号读取页面公钥，完成验证码处理和一次登录提交，成功后更新
// 当前客户端并返回会话快照。SaveSession 将当前会话保存到配置路径。
// 验证码开启时调用配置的 CaptchaSolver，回调缺失时返回 ErrCaptchaRequired。
func (c *Client) Login(ctx context.Context) (*Session, error) {
	if err := validateCredentials(c.config.Username, c.config.Password); err != nil {
		return nil, err
	}
	publicKey, err := c.FetchLoginPublicKey(ctx)
	if err != nil {
		return nil, err
	}
	required, err := c.captchaStatus(ctx)
	if err != nil {
		return nil, err
	}
	captcha := ""
	if required {
		if c.config.CaptchaSolver == nil {
			return nil, ErrCaptchaRequired
		}
		image, err := c.FetchCaptcha(ctx)
		if err != nil {
			return nil, err
		}
		captcha, err = c.config.CaptchaSolver(ctx, image)
		if err != nil {
			return nil, fmt.Errorf("获取验证码输入：%w", err)
		}
		captcha = strings.TrimSpace(captcha)
		if captcha == "" {
			return nil, errors.New("验证码不能为空")
		}
	}
	form, err := makeLoginForm(publicKey, c.config.Username, c.config.Password, captcha, time.Now())
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

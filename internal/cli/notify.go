package cli

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"easycheck/qdgj"
)

const (
	telegramChannel = "Telegram"
	feishuChannel   = "飞书"
)

type notificationChannel struct {
	name     string
	endpoint string
	chatID   string
	secret   string
}

type resultNotifier struct {
	client   *http.Client
	channels []notificationChannel
}

// notifierFromEnv 在 login 和 cron 模式下读取；没有配置渠道时不创建 HTTP 客户端。
func notifierFromEnv() (*resultNotifier, error) {
	token := strings.TrimSpace(os.Getenv("EASYCHECK_TG_BOT_TOKEN"))
	chatID := strings.TrimSpace(os.Getenv("EASYCHECK_TG_CHAT_ID"))
	webhook := strings.TrimSpace(os.Getenv("EASYCHECK_FEISHU_WEBHOOK_URL"))
	secret := strings.TrimSpace(os.Getenv("EASYCHECK_FEISHU_SECRET"))
	var channels []notificationChannel
	if token != "" || chatID != "" {
		if token == "" || chatID == "" {
			return nil, errors.New("Telegram 通知须同时配置 EASYCHECK_TG_BOT_TOKEN 和 EASYCHECK_TG_CHAT_ID")
		}
		// Token 进入 URL 路径，拒绝改变请求路径或查询参数的字符。
		if strings.ContainsAny(token, "/?#\\ \t\r\n") {
			return nil, errors.New("EASYCHECK_TG_BOT_TOKEN 含有无效字符")
		}
		channels = append(channels, notificationChannel{
			name: telegramChannel, endpoint: "https://api.telegram.org/bot" + url.PathEscape(token) + "/sendMessage", chatID: chatID,
		})
	}
	if webhook != "" {
		parsed, err := url.Parse(webhook)
		if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil || parsed.Fragment != "" {
			return nil, errors.New("EASYCHECK_FEISHU_WEBHOOK_URL 须为不含用户信息或片段的有效 HTTP(S) 地址")
		}
		channels = append(channels, notificationChannel{name: feishuChannel, endpoint: webhook, secret: secret})
	} else if secret != "" {
		return nil, errors.New("配置 EASYCHECK_FEISHU_SECRET 时须同时配置 EASYCHECK_FEISHU_WEBHOOK_URL")
	}
	if len(channels) == 0 {
		return nil, nil
	}
	timeout := 10 * time.Second
	if value := strings.TrimSpace(os.Getenv("EASYCHECK_NOTIFY_TIMEOUT")); value != "" {
		var err error
		timeout, err = time.ParseDuration(value)
		if err != nil || timeout <= 0 {
			return nil, errors.New("EASYCHECK_NOTIFY_TIMEOUT 须为大于零的时长，例如 10s")
		}
	}
	return &resultNotifier{
		channels: channels,
		// 独立于站点客户端，不携带站点令牌和 Cookie，也不跟随重定向。
		client: &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}},
	}, nil
}

func (a *app) notifyCronResult(ctx context.Context, notifier *resultNotifier, report automaticSignReport, runErr error, at time.Time) {
	if report.Succeeded == 0 && report.Failed == 0 && runErr == nil {
		return
	}
	a.sendNotification(ctx, notifier, cronResultMessage(report, runErr, at), at)
}

func (a *app) notifyLoginFailure(ctx context.Context, notifier *resultNotifier, err error, attempt, remaining int, interval time.Duration, at time.Time) {
	message := fmt.Sprintf("EasyCheck 登录失败\n时间：%s\n第 %d 次尝试失败\n原因：%s\n", at.Format(time.RFC3339), attempt, notificationErrorSummary(err))
	if remaining > 0 {
		message += fmt.Sprintf("将在 %s 后重试，剩余 %d 次重试。", interval, remaining)
	} else {
		message += "重试次数已用尽，不再重试；请检查配置或站点状态。"
	}
	a.sendNotification(ctx, notifier, message, at)
}

func (a *app) sendNotification(ctx context.Context, notifier *resultNotifier, message string, at time.Time) {
	if notifier == nil || ctx.Err() != nil {
		return
	}
	for _, channel := range notifier.channels {
		if ctx.Err() != nil {
			return
		}
		if err := notifier.send(ctx, channel, message, at); err != nil {
			if ctx.Err() == nil {
				fmt.Fprintf(a.diagnostics, "%s 通知失败：%v；执行结果不受影响。\n", channel.name, err)
			}
		} else {
			fmt.Fprintf(a.diagnostics, "%s 通知已发送。\n", channel.name)
		}
	}
}

func cronResultMessage(report automaticSignReport, runErr error, at time.Time) string {
	var text strings.Builder
	status := "成功"
	if report.Failed > 0 || runErr != nil {
		status = "失败"
		if report.Succeeded > 0 {
			status = "部分失败"
		}
	}
	fmt.Fprintf(&text, "EasyCheck 定时签到%s\n时间：%s\n成功 %d 项，失败 %d 项，跳过 %d 项\n", status, at.Format(time.RFC3339), report.Succeeded, report.Failed, report.Skipped)
	if runErr != nil {
		fmt.Fprintf(&text, "本轮执行异常：%s\n", notificationErrorSummary(runErr))
	}
	for _, result := range report.Results {
		switch result.Status {
		case "success":
			fmt.Fprintf(&text, "- 成功：%s（pcid=%s）\n", result.Name, result.BatchID)
		case "failed":
			fmt.Fprintf(&text, "- 失败：%s（pcid=%s），详细原因请查看日志\n", result.Name, result.BatchID)
		}
	}
	// 同时控制 Telegram 字符数和飞书 JSON 请求体大小，不截断 UTF-8 字符。
	const maxRunes = 2000
	runes := []rune(text.String())
	if len(runes) > maxRunes {
		suffix := []rune("\n…（内容过长，已截断）")
		return string(runes[:maxRunes-len(suffix)]) + string(suffix)
	}
	return text.String()
}

// 不转发原始错误文本：服务端消息、网络 URL 和文件路径可能包含敏感信息。
func notificationErrorSummary(err error) string {
	var apiErr *qdgj.APIError
	switch {
	case errors.As(err, &apiErr):
		return fmt.Sprintf("站点接口拒绝请求（HTTP=%d，code=%d，statusCode=%d）", apiErr.HTTPStatus, apiErr.Code, apiErr.StatusCode)
	case errors.Is(err, context.DeadlineExceeded):
		return "请求超时"
	case errors.Is(err, qdgj.ErrLoginPublicKeyNotFound):
		return "未找到登录 RSA 公钥"
	case errors.Is(err, qdgj.ErrCaptchaRequired):
		return "登录需要验证码"
	case errors.Is(err, io.EOF):
		return "标准输入已关闭，无法读取验证码"
	default:
		return "执行失败，详细原因请查看日志"
	}
}

func (n *resultNotifier) send(ctx context.Context, channel notificationChannel, text string, at time.Time) error {
	var payload map[string]any
	switch channel.name {
	case telegramChannel:
		payload = map[string]any{"chat_id": channel.chatID, "text": text}
	case feishuChannel:
		payload = map[string]any{"msg_type": "text", "content": map[string]string{"text": text}}
		if channel.secret != "" {
			timestamp := strconv.FormatInt(at.Unix(), 10)
			// 飞书要求以 timestamp + "\n" + secret 为 HMAC key，对空消息签名。
			mac := hmac.New(sha256.New, []byte(timestamp+"\n"+channel.secret))
			payload["timestamp"], payload["sign"] = timestamp, base64.StdEncoding.EncodeToString(mac.Sum(nil))
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return errors.New("编码通知请求失败")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, channel.endpoint, bytes.NewReader(raw))
	if err != nil {
		return errors.New("创建通知请求失败")
	}
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	response, err := n.client.Do(request)
	if err != nil {
		// 网络错误通常包含带 Token 的 URL；不将原始错误或服务端正文写入日志。
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return errors.New("通知请求超时")
		}
		return errors.New("通知网络请求失败")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("通知接口返回 HTTP %d", response.StatusCode)
	}
	const maxResponseSize = 64 << 10
	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseSize+1))
	if err != nil || len(body) > maxResponseSize {
		return errors.New("读取通知响应失败或响应过大")
	}
	if channel.name == telegramChannel {
		var result struct {
			OK        bool `json:"ok"`
			ErrorCode int  `json:"error_code"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return errors.New("Telegram 通知响应不是有效 JSON")
		}
		if !result.OK {
			return fmt.Errorf("Telegram 拒绝通知（error_code=%d）", result.ErrorCode)
		}
	} else {
		var result struct {
			Code       *int `json:"code"`
			StatusCode *int `json:"StatusCode"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			return errors.New("飞书通知响应不是有效 JSON")
		}
		code := result.Code
		if code == nil {
			code = result.StatusCode
		}
		if code == nil {
			return errors.New("飞书通知响应缺少状态码")
		}
		if *code != 0 {
			return fmt.Errorf("飞书拒绝通知（code=%d）", *code)
		}
	}
	return nil
}

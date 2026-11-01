package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"easycheck/qdgj"
)

func clearNotificationEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"EASYCHECK_TG_BOT_TOKEN", "EASYCHECK_TG_CHAT_ID", "EASYCHECK_FEISHU_WEBHOOK_URL", "EASYCHECK_FEISHU_SECRET", "EASYCHECK_NOTIFY_TIMEOUT"} {
		t.Setenv(key, "")
	}
}

func TestCronNotifierFromEnv(t *testing.T) {
	clearNotificationEnv(t)
	notifier, err := cronNotifierFromEnv()
	if err != nil || notifier != nil {
		t.Fatalf("notification should be disabled by default: %v", err)
	}
	t.Setenv("EASYCHECK_TG_BOT_TOKEN", " 123456:fixture-token ")
	t.Setenv("EASYCHECK_TG_CHAT_ID", " -100123456 ")
	t.Setenv("EASYCHECK_FEISHU_WEBHOOK_URL", " https://open.feishu.cn/open-apis/bot/v2/hook/fixture-hook ")
	t.Setenv("EASYCHECK_FEISHU_SECRET", "fixture-feishu-secret")
	t.Setenv("EASYCHECK_NOTIFY_TIMEOUT", " 2s ")
	notifier, err = cronNotifierFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if len(notifier.channels) != 2 || notifier.client.Timeout != 2*time.Second || notifier.client.Jar != nil || notifier.channels[0].endpoint != "https://api.telegram.org/bot123456:fixture-token/sendMessage" || notifier.channels[0].chatID != "-100123456" || notifier.channels[1].secret != "fixture-feishu-secret" {
		t.Fatal("environment config or isolated HTTP client was not preserved")
	}
	if !errors.Is(notifier.client.CheckRedirect(nil, nil), http.ErrUseLastResponse) {
		t.Fatal("notifications must not follow redirects")
	}
	for _, channel := range []string{telegramChannel, feishuChannel} {
		t.Run("only "+channel, func(t *testing.T) {
			clearNotificationEnv(t)
			if channel == telegramChannel {
				t.Setenv("EASYCHECK_TG_BOT_TOKEN", "123:fixture")
				t.Setenv("EASYCHECK_TG_CHAT_ID", "@fixture_channel")
			} else {
				t.Setenv("EASYCHECK_FEISHU_WEBHOOK_URL", "https://open.feishu.cn/open-apis/bot/v2/hook/fixture")
			}
			notifier, err := cronNotifierFromEnv()
			if err != nil || notifier == nil || len(notifier.channels) != 1 || notifier.channels[0].name != channel || notifier.client.Timeout != 10*time.Second {
				t.Fatalf("single channel failed: %v", err)
			}
		})
	}
}

func TestCronNotifierRejectsIncompleteOrInvalidEnv(t *testing.T) {
	for _, values := range []map[string]string{
		{"EASYCHECK_TG_BOT_TOKEN": "fixture-secret"},
		{"EASYCHECK_TG_CHAT_ID": "123"},
		{"EASYCHECK_TG_BOT_TOKEN": "123:fixture-secret?x=y", "EASYCHECK_TG_CHAT_ID": "123"},
		{"EASYCHECK_TG_BOT_TOKEN": "123:fixture-secret/extra", "EASYCHECK_TG_CHAT_ID": "123"},
		{"EASYCHECK_FEISHU_SECRET": "fixture-secret"},
		{"EASYCHECK_FEISHU_WEBHOOK_URL": "not-a-url-fixture-secret"},
		{"EASYCHECK_FEISHU_WEBHOOK_URL": "file:///fixture-secret"},
		{"EASYCHECK_FEISHU_WEBHOOK_URL": "https://user:fixture-secret@example.invalid/"},
		{"EASYCHECK_FEISHU_WEBHOOK_URL": "https://example.invalid/#fixture-secret"},
		{"EASYCHECK_FEISHU_WEBHOOK_URL": "https://example.invalid/%fixture-secret"},
		{"EASYCHECK_FEISHU_WEBHOOK_URL": "https://example.invalid/", "EASYCHECK_NOTIFY_TIMEOUT": "fixture-secret"},
		{"EASYCHECK_FEISHU_WEBHOOK_URL": "https://example.invalid/", "EASYCHECK_NOTIFY_TIMEOUT": "0s"},
		{"EASYCHECK_FEISHU_WEBHOOK_URL": "https://example.invalid/", "EASYCHECK_NOTIFY_TIMEOUT": "-1s"},
	} {
		clearNotificationEnv(t)
		for key, value := range values {
			t.Setenv(key, value)
		}
		_, err := cronNotifierFromEnv()
		if err == nil || strings.Contains(err.Error(), "fixture-secret") {
			t.Fatalf("invalid config accepted or secret leaked: %v", err)
		}
	}
}

type notificationTransport func(*http.Request) (*http.Response, error)

func (f notificationTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func notificationHTTPResponse(r *http.Request, status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

func successNotificationFixture() automaticSignReport {
	return automaticSignReport{
		Matched: 3, Succeeded: 2, Failed: 1, Skipped: 4,
		Results: []automaticSignResult{
			{BatchID: "first", Name: "早签到 <测试>", Status: "success", Response: json.RawMessage(`{"private":"do-not-forward"}`)},
			{BatchID: "second", Name: "晚签到 😀", Status: "success"},
			{BatchID: "failed-batch", Name: "失败任务", Status: "failed", Error: "do-not-forward"},
		},
	}
}

func TestCronNotificationsSendTelegramAndSignedFeishuSummaries(t *testing.T) {
	clearNotificationEnv(t)
	t.Setenv("EASYCHECK_TG_BOT_TOKEN", "123:fixture-token")
	t.Setenv("EASYCHECK_TG_CHAT_ID", "-100123")
	t.Setenv("EASYCHECK_FEISHU_WEBHOOK_URL", "https://open.feishu.cn/open-apis/bot/v2/hook/fixture-hook")
	t.Setenv("EASYCHECK_FEISHU_SECRET", "fixture-feishu-secret")
	notifier, err := cronNotifierFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	var calls int
	at := time.Unix(1600000000, 0).In(time.FixedZone("CST", 8*3600))
	notifier.client.Transport = notificationTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get("loginUserId") != "" || r.Header.Get("loginUserOrgId") != "" {
			t.Error("wrong notification request or site credentials leaked")
		}
		var payload struct {
			ChatID    string                `json:"chat_id"`
			Text      string                `json:"text"`
			Type      string                `json:"msg_type"`
			Content   struct{ Text string } `json:"content"`
			Timestamp string                `json:"timestamp"`
			Sign      string                `json:"sign"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		text := payload.Text
		if calls == 1 {
			if r.URL.String() != notifier.channels[0].endpoint || payload.ChatID != "-100123" || payload.Type != "" {
				t.Error("wrong Telegram request")
			}
		} else {
			text = payload.Content.Text
			if r.URL.String() != notifier.channels[1].endpoint || payload.Type != "text" || payload.Timestamp != "1600000000" || payload.Sign != "+x3RjcnghvbwlWKSy7B+cvtNFAT8+bTz/qoprLcvtAQ=" {
				t.Error("wrong Feishu request or HMAC signature")
			}
		}
		for _, want := range []string{"成功 2 项，失败 1 项", "早签到 <测试>", "晚签到 😀", "pcid=first", at.Format(time.RFC3339)} {
			if !strings.Contains(text, want) {
				t.Errorf("notification is missing %s", want)
			}
		}
		for _, forbidden := range []string{"do-not-forward", "failed-batch", "fixture-token", "fixture-feishu-secret"} {
			if strings.Contains(text, forbidden) {
				t.Errorf("notification leaked %s", forbidden)
			}
		}
		if calls == 1 {
			return notificationHTTPResponse(r, 200, `{"ok":true,"result":{}}`), nil
		}
		return notificationHTTPResponse(r, 200, `{"code":0,"msg":"success"}`), nil
	})
	var output, diagnostics bytes.Buffer
	a := &app{output: &output, diagnostics: &diagnostics}
	a.notifyCronSuccess(context.Background(), notifier, successNotificationFixture(), at)
	if calls != 2 || output.Len() != 0 || !strings.Contains(diagnostics.String(), "Telegram 通知已发送") || !strings.Contains(diagnostics.String(), "飞书 通知已发送") {
		t.Fatalf("did not send both channels or polluted stdout: calls=%d diagnostics=%s", calls, &diagnostics)
	}
}

func TestCronNotificationResponsesAndFailures(t *testing.T) {
	for _, tc := range []struct {
		name, channel, body string
		status              int
		wantError           bool
	}{
		{"telegram success", telegramChannel, `{"ok":true}`, 200, false},
		{"telegram API rejection", telegramChannel, `{"ok":false,"error_code":400,"description":"fixture-secret"}`, 200, true},
		{"telegram missing ok", telegramChannel, `{}`, 200, true},
		{"feishu modern success", feishuChannel, `{"code":0}`, 200, false},
		{"feishu legacy success", feishuChannel, `{"StatusCode":0,"StatusMessage":"success"}`, 200, false},
		{"feishu API rejection", feishuChannel, `{"code":19021,"msg":"fixture-secret"}`, 200, true},
		{"feishu legacy rejection", feishuChannel, `{"StatusCode":19021}`, 200, true},
		{"feishu missing code", feishuChannel, `{}`, 200, true},
		{"feishu null", feishuChannel, `null`, 200, true},
		{"malformed JSON", feishuChannel, `fixture-secret`, 200, true},
		{"HTTP failure", telegramChannel, `fixture-secret`, 500, true},
		{"redirect", telegramChannel, `fixture-secret`, 302, true},
		{"oversized body", feishuChannel, strings.Repeat("x", (64<<10)+1), 200, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n := &cronNotifier{client: &http.Client{Transport: notificationTransport(func(r *http.Request) (*http.Response, error) {
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if tc.channel == feishuChannel && (payload["sign"] != nil || payload["timestamp"] != nil) {
					t.Error("unsigned Feishu config unexpectedly sent a signature")
				}
				return notificationHTTPResponse(r, tc.status, tc.body), nil
			})}}
			err := n.send(context.Background(), notificationChannel{name: tc.channel, endpoint: "https://example.invalid/fixture-secret", chatID: "123"}, "fixture", time.Now())
			if (err != nil) != tc.wantError || err != nil && strings.Contains(err.Error(), "fixture-secret") {
				t.Fatalf("unexpected result or leaked response: %v", err)
			}
		})
	}
}

func TestCronNotificationFailureDoesNotBlockOtherChannel(t *testing.T) {
	var calls int
	n := &cronNotifier{
		channels: []notificationChannel{
			{name: telegramChannel, endpoint: "https://example.invalid/fixture-secret"},
			{name: feishuChannel, endpoint: "https://example.invalid/fixture-secret"},
		},
		client: &http.Client{Transport: notificationTransport(func(r *http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				return nil, errors.New("request failed with URL and fixture-secret")
			}
			return notificationHTTPResponse(r, 200, `{"code":0}`), nil
		})},
	}
	var diagnostics bytes.Buffer
	a := &app{diagnostics: &diagnostics}
	a.notifyCronSuccess(context.Background(), n, successNotificationFixture(), time.Now())
	if calls != 2 || !strings.Contains(diagnostics.String(), "Telegram 通知失败") || !strings.Contains(diagnostics.String(), "飞书 通知已发送") || strings.Contains(diagnostics.String(), "fixture-secret") {
		t.Fatalf("notification failure blocked other channel or leaked secret: %s", &diagnostics)
	}
}

func TestCronNotificationIgnoresEmptyFailedSkippedAndCanceledRounds(t *testing.T) {
	var calls int
	n := &cronNotifier{channels: []notificationChannel{{name: feishuChannel, endpoint: "https://example.invalid/"}}, client: &http.Client{Transport: notificationTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		return notificationHTTPResponse(r, 200, `{"code":0}`), nil
	})}}
	a := &app{diagnostics: io.Discard}
	for _, report := range []automaticSignReport{{}, {Failed: 1}, {Skipped: 2}} {
		a.notifyCronSuccess(context.Background(), n, report, time.Now())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a.notifyCronSuccess(ctx, n, successNotificationFixture(), time.Now())
	a.notifyCronSuccess(context.Background(), nil, successNotificationFixture(), time.Now())
	if calls != 0 {
		t.Fatalf("sent %d notifications without a successful active cron round", calls)
	}
}

func TestCronSuccessMessageTruncatesUnicode(t *testing.T) {
	report := successNotificationFixture()
	report.Results[0].Name = strings.Repeat("😀签到", 3000)
	text := cronSuccessMessage(report, time.Now())
	if !utf8.ValidString(text) || len([]rune(text)) != 2000 || !strings.HasSuffix(text, "…（内容过长，已截断）") || !strings.Contains(text, "成功 2 项，失败 1 项") {
		t.Fatal("notification text limit or Unicode truncation failed")
	}
}

func TestCronNotificationTimeoutAndCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer server.Close()
	clearNotificationEnv(t)
	t.Setenv("EASYCHECK_FEISHU_WEBHOOK_URL", server.URL)
	t.Setenv("EASYCHECK_NOTIFY_TIMEOUT", "30ms")
	n, err := cronNotifierFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	err = n.send(context.Background(), n.channels[0], "fixture", time.Now())
	if err == nil || !strings.Contains(err.Error(), "超时") || time.Since(started) > 2*time.Second {
		t.Fatalf("notification timeout was not bounded: %v", err)
	}
	n.client.Timeout = 5 * time.Second
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	started = time.Now()
	err = n.send(ctx, n.channels[0], "fixture", time.Now())
	if !errors.Is(err, context.Canceled) || time.Since(started) > 2*time.Second {
		t.Fatalf("notification did not honor shutdown: %v", err)
	}
}

func TestCronNotificationDoesNotFollowRedirects(t *testing.T) {
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	clearNotificationEnv(t)
	t.Setenv("EASYCHECK_FEISHU_WEBHOOK_URL", server.URL)
	n, err := cronNotifierFromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if err := n.send(context.Background(), n.channels[0], "fixture", time.Now()); err == nil || redirected.Load() != 0 {
		t.Fatalf("notification followed redirect: redirected=%d err=%v", redirected.Load(), err)
	}
}

func TestCronNotifiesOnlyActualSuccessAndKeepsSigningAfterNotificationFailure(t *testing.T) {
	clearNotificationEnv(t)
	var notifications, submissions atomic.Int32
	notificationsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if submissions.Load() == 0 {
			t.Error("notification sent before sign submission")
		}
		notifications.Add(1)
		fmt.Fprint(w, `{"code":19021,"msg":"fixture-secret"}`)
	}))
	defer notificationsServer.Close()
	base, session := taskCLIFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/queryQdrw") {
			var query struct {
				BatchID    string          `json:"pcid"`
				Pagination json.RawMessage `json:"pagination"`
			}
			json.NewDecoder(r.Body).Decode(&query)
			tasks := []qdgj.Task{activeTaskFixture("good"), activeTaskFixture("bad")}
			if query.Pagination == nil {
				tasks = []qdgj.Task{activeTaskFixture(query.BatchID)}
			}
			writeTaskPage(t, w, tasks, 1, 1)
			return
		}
		submissions.Add(1)
		var body struct {
			BatchID string `json:"pcid"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.BatchID == "bad" {
			fmt.Fprint(w, `{"code":409,"meta":{"message":"fixture denied"}}`)
		} else {
			fmt.Fprint(w, `{"code":200,"data":{}}`)
		}
	})
	t.Setenv("EASYCHECK_FEISHU_WEBHOOK_URL", notificationsServer.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	var output, diagnostics bytes.Buffer
	warnings := 0
	diagnosticWriter := callbackWriter(func(p []byte) {
		diagnostics.Write(p)
		if bytes.Contains(p, []byte("飞书 通知失败")) {
			warnings++
			if warnings == 2 {
				cancel()
			}
		}
	})
	args := append(automaticSignArgs(base, session), "--cron", "@every 1s")
	err := Run(ctx, args, strings.NewReader(""), &output, diagnosticWriter)
	if !errors.Is(err, context.Canceled) || notifications.Load() != 2 || submissions.Load() != 4 || warnings != 2 || strings.Contains(diagnostics.String(), "fixture-secret") {
		t.Fatalf("cron stopped after notification failure or missed partial success: notifications=%d signs=%d diagnostics=%s err=%v", notifications.Load(), submissions.Load(), &diagnostics, err)
	}
	decoder := json.NewDecoder(&output)
	for i := 0; i < 2; i++ {
		var report automaticSignReport
		if err := decoder.Decode(&report); err != nil || report.Succeeded != 1 || report.Failed != 1 {
			t.Fatalf("notification corrupted cron JSON output: %+v (%v)", report, err)
		}
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		t.Fatal("notification logs polluted JSON stdout")
	}
}

func TestNotificationConfigOnlyAppliesToCron(t *testing.T) {
	clearNotificationEnv(t)
	var notifications atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { notifications.Add(1) }))
	defer server.Close()
	base, session := automaticSignCLIFixture(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"code":200,"data":{}}`)
	})
	t.Setenv("EASYCHECK_FEISHU_WEBHOOK_URL", server.URL)
	// Incomplete Telegram config must be ignored by non-cron commands, including help.
	t.Setenv("EASYCHECK_TG_BOT_TOKEN", "fixture-token")
	for _, extra := range [][]string{nil, {"--pcid", "batch"}, {"--help"}} {
		args := append(automaticSignArgs(base, session), extra...)
		if err := run(args, strings.NewReader(""), io.Discard, io.Discard); err != nil {
			t.Fatalf("notification config broke a non-cron command: %v", err)
		}
	}
	if notifications.Load() != 0 {
		t.Fatal("manual sign must not send notifications")
	}
	var calls atomic.Int32
	base, session = taskCLIFixture(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
	t.Setenv("EASYCHECK_TG_BOT_TOKEN", "fixture-token")
	args := append(automaticSignArgs(base, session), "--cron", "0 8 * * *")
	err := run(args, strings.NewReader(""), io.Discard, io.Discard)
	var usage *usageError
	if !errors.As(err, &usage) || calls.Load() != 0 || !strings.Contains(err.Error(), "EASYCHECK_TG_CHAT_ID") {
		t.Fatalf("invalid notification config was not rejected before cron startup: %v", err)
	}
}

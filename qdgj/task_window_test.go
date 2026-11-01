package qdgj

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTaskSignInAvailable(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-11-01T08:00:00+08:00")
	base := Task{Status: "进行中", CheckInStatus: "未签到", Method: "定位签到", StartTime: "2026-11-01 07:30:00", EndTime: "2026-11-01 08:30:00"}
	for _, tc := range []struct {
		name   string
		change func(*Task)
		want   bool
		bad    bool
	}{
		{name: "inside window", want: true},
		{name: "inclusive start", change: func(task *Task) { task.StartTime = "2026-11-01 08:00:00" }, want: true},
		{name: "exclusive end", change: func(task *Task) { task.EndTime = "2026-11-01 08:00:00" }},
		{name: "before start despite status", change: func(task *Task) { task.StartTime = "2026-11-01 08:01:00" }},
		{name: "after end despite status", change: func(task *Task) { task.EndTime = "2026-11-01 07:59:00" }},
		{name: "already signed", change: func(task *Task) { task.CheckInStatus = "已签到" }},
		{name: "not started", change: func(task *Task) { task.Status = "未开始" }},
		{name: "ended", change: func(task *Task) { task.Status = "已结束" }},
		{name: "unknown status", change: func(task *Task) { task.Status = "" }},
		{name: "QR code", change: func(task *Task) { task.Method = "二维码签到" }},
		{name: "missing method", change: func(task *Task) { task.Method = "" }},
		{name: "minutes", change: func(task *Task) { task.StartTime, task.EndTime = "2026-11-01 07:30", "2026-11-01 08:30" }, want: true},
		{name: "ISO local", change: func(task *Task) { task.StartTime, task.EndTime = "2026-11-01T07:30:00", "2026-11-01T08:30:00" }, want: true},
		{name: "slash dates", change: func(task *Task) { task.StartTime, task.EndTime = "2026/11/01 07:30:00", "2026/11/01 08:30:00" }, want: true},
		{name: "UTC dates", change: func(task *Task) { task.StartTime, task.EndTime = "2026-10-31T23:30:00Z", "2026-11-01T00:30:00Z" }, want: true},
		{name: "offset dates", change: func(task *Task) {
			task.StartTime, task.EndTime = "2026-11-01T08:30:00+09:00", "2026-11-01T09:30:00+09:00"
		}, want: true},
		{name: "fractional seconds", change: func(task *Task) { task.StartTime, task.EndTime = "2026-11-01 07:59:59.9", "2026-11-01 08:00:00.1" }, want: true},
		{name: "whitespace", change: func(task *Task) {
			task.Status, task.CheckInStatus, task.StartTime = " 进行中 ", " 未签到 ", " 2026-11-01 07:30:00 "
		}, want: true},
		{name: "overnight", change: func(task *Task) { task.StartTime, task.EndTime = "2026-10-31 23:00", "2026-11-01 09:00" }, want: true},
		{name: "missing start", change: func(task *Task) { task.StartTime = "" }, bad: true},
		{name: "missing end", change: func(task *Task) { task.EndTime = "" }, bad: true},
		{name: "invalid date", change: func(task *Task) { task.StartTime = "2026-11-31 07:30:00" }, bad: true},
		{name: "invalid format", change: func(task *Task) { task.StartTime = "07:30:00" }, bad: true},
		{name: "reversed window", change: func(task *Task) { task.StartTime, task.EndTime = task.EndTime, task.StartTime }, bad: true},
		{name: "zero window", change: func(task *Task) { task.StartTime = task.EndTime }, bad: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			task := base
			if tc.change != nil {
				tc.change(&task)
			}
			got, err := task.SignInAvailable(now.UTC()) // 与机器时区无关。
			if got != tc.want || (err != nil) != tc.bad {
				t.Fatalf("available=%t err=%v want=%t bad=%t", got, err, tc.want, tc.bad)
			}
		})
	}
}

func TestSignInIfActiveRechecksLatestDetail(t *testing.T) {
	for _, mode := range []string{"active", "already signed", "not started", "ended", "outside window", "missing time", "QR code"} {
		t.Run(mode, func(t *testing.T) {
			queries, submissions := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/qdgj/"+queryPath {
					queries++
					task := Task{TaskID: "task", BatchID: "batch", Status: "进行中", CheckInStatus: "未签到", Method: "定位签到",
						StartTime: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano),
						EndTime:   time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)}
					switch mode {
					case "already signed":
						task.CheckInStatus = "已签到"
					case "not started":
						task.Status = "未开始"
					case "ended":
						task.Status = "已结束"
					case "outside window":
						task.EndTime = time.Now().Add(-time.Second).UTC().Format(time.RFC3339Nano)
					case "missing time":
						task.StartTime = ""
					case "QR code":
						task.Method = "二维码签到"
					}
					json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"list": []Task{task}}})
				} else {
					submissions++
					assertAuth(t, r, signPath)
					fmt.Fprint(w, `{"code":200,"data":{}}`)
				}
			}))
			defer server.Close()
			client := restoredClient(t, server.URL)
			_, err := client.SignInIfActive(context.Background(), SignInRequest{
				// 即便调用方已经知道 ID 和位置，也必须重新查询状态。
				TaskID: "task", BatchID: "batch", Location: Location{Address: "fixture", Longitude: 10.5, Latitude: 20.5},
			})
			if queries != 1 {
				t.Fatalf("did not recheck latest detail: queries=%d", queries)
			}
			if mode == "active" {
				if err != nil || submissions != 1 {
					t.Fatalf("active task not signed: signs=%d err=%v", submissions, err)
				}
			} else if err == nil || submissions != 0 || mode != "missing time" && !errors.Is(err, ErrTaskNotActive) {
				t.Fatalf("inactive task signed or reason lost: signs=%d err=%v", submissions, err)
			}
		})
	}
}

func TestSignInRequestValidateOptionsWithoutBatch(t *testing.T) {
	request := SignInRequest{UseAllowedLocation: true}
	if err := request.ValidateOptions(); err != nil {
		t.Fatal(err)
	}
	if err := request.Validate(); err == nil {
		t.Fatal("immediate request must still require a batch")
	}
}

func TestSignInIfActiveWindowClosesDuringGeocoding(t *testing.T) {
	client := restoredClient(t, "https://fixture.invalid")
	var deadline time.Time
	queries, lookups, submissions := 0, 0, 0
	client.http.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.URL.Path == "/qdgj/"+queryPath:
			queries++
			deadline = time.Now().Add(250 * time.Millisecond)
			task := Task{TaskID: "task", BatchID: "batch", Status: "进行中", CheckInStatus: "未签到", Method: "定位签到",
				StartTime: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano), EndTime: deadline.UTC().Format(time.RFC3339Nano)}
			body, _ := json.Marshal(map[string]any{"code": 200, "data": map[string]any{"list": []Task{task}}})
			return mapJSONResponse(req, http.StatusOK, string(body)), nil
		case req.URL.Path == "/qdgj/"+mapConfigPath:
			return mapJSONResponse(req, http.StatusOK, fixtureMapConfig), nil
		case req.URL.Host == "apis.map.qq.com":
			lookups++
			// 模拟逆地址解析返回时签到窗口已结束。
			timer := time.NewTimer(time.Until(deadline) + time.Millisecond)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-req.Context().Done():
				return nil, req.Context().Err()
			}
			return mapJSONResponse(req, http.StatusOK, `{"status":0,"result":{"address":"fixture address"}}`), nil
		default:
			submissions++
			return mapJSONResponse(req, http.StatusOK, `{"code":200,"data":{}}`), nil
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := client.SignInIfActive(ctx, SignInRequest{BatchID: "batch", Location: Location{Longitude: 10.5, Latitude: 20.5}})
	if !errors.Is(err, ErrTaskNotActive) || queries != 1 || lookups != 1 || submissions != 0 {
		t.Fatalf("expired during geocoding: queries=%d lookups=%d signs=%d err=%v", queries, lookups, submissions, err)
	}
}

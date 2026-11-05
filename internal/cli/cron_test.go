package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"easycheck/qdgj"
)

func TestCronResolution(t *testing.T) {
	now, _ := time.Parse(time.RFC3339, "2026-11-02T07:30:20+08:00") // 周一
	for _, tc := range []struct {
		name, expression, timezone, want string
	}{
		{name: "daily", expression: "0 8 * * *", want: "2026-11-02T08:00:00+08:00"},
		{name: "tomorrow", expression: "0 7 * * *", want: "2026-11-03T07:00:00+08:00"},
		{name: "every five minutes", expression: "*/5 * * * *", want: "2026-11-02T07:35:00+08:00"},
		{name: "multiple hours", expression: "0 8,20 * * *", want: "2026-11-02T08:00:00+08:00"},
		{name: "weekdays", expression: "0 8 * * MON-FRI", want: "2026-11-02T08:00:00+08:00"},
		{name: "weekend", expression: "0 8 * * SAT,SUN", want: "2026-11-07T08:00:00+08:00"},
		{name: "monthly", expression: "0 8 1 * *", want: "2026-12-01T08:00:00+08:00"},
		{name: "UTC", expression: "0 8 * * *", timezone: "UTC", want: "2026-11-02T08:00:00Z"},
		{name: "whitespace", expression: " 0 8 * * * ", timezone: " Asia/Shanghai ", want: "2026-11-02T08:00:00+08:00"},
		{name: "descriptor", expression: "@daily", want: "2026-11-03T00:00:00+08:00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := (&app{}).newCommand(commandSpec{})
			var options cronOptions
			options.bind(c)
			args := []string{"--cron", tc.expression}
			if tc.timezone != "" {
				args = append(args, "--timezone", tc.timezone)
			}
			if err := c.flags.Parse(args); err != nil {
				t.Fatal(err)
			}
			plan, err := options.resolve(c)
			if err != nil {
				t.Fatal(err)
			}
			if got := plan.schedule.Next(now.In(plan.location)).Format(time.RFC3339); got != tc.want {
				t.Fatalf("next=%s want=%s", got, tc.want)
			}
		})
	}
	for _, expression := range []string{"@every 1s", "@every 10m"} {
		c := (&app{}).newCommand(commandSpec{})
		var options cronOptions
		options.bind(c)
		if err := c.flags.Parse([]string{"--cron", expression}); err != nil {
			t.Fatal(err)
		}
		if plan, err := options.resolve(c); err != nil || plan == nil {
			t.Fatalf("valid interval rejected: %s (%v)", expression, err)
		}
	}
}

func TestCronRejectsInvalidOptionsBeforeWaitingOrNetwork(t *testing.T) {
	var calls atomic.Int32
	base, session := taskCLIFixture(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
	for _, options := range [][]string{
		{"--cron", ""}, {"--cron", " "}, {"--cron", "bad"},
		{"--cron", "0 8 * *"}, {"--cron", "0 0 8 * * *"},
		{"--cron", "60 8 * * *"}, {"--cron", "0 24 * * *"},
		{"--cron", "0 0 31 2 *"}, {"--cron", "*/0 * * * *"},
		{"--cron", "TZ="}, {"--cron", "CRON_TZ=UTC 0 8 * * *"},
		{"--cron", "@every 0s"}, {"--cron", "@every -1m"}, {"--cron", "@every 1ms"},
		{"--timezone", "UTC"}, {"--cron", "0 8 * * *", "--timezone", ""},
		{"--cron", "0 8 * * *", "--timezone", "Local"}, {"--cron", "0 8 * * *", "--timezone", "Not/AZone"},
		{"--cron", "0 8 * * *", "--longitude", "NaN", "--latitude", "20"},
		{"--cron", "0 8 * * *", "--outside-range"},
		{"--cron", "0 8 * * *", "--pcid", ""},
		{"--cron", "0 8 * * *", "--retries", "-1"},
		{"--cron", "0 8 * * *", "--retry-interval", "0s"},
		{"--retries", "1"}, {"--retry-interval", "1s"},
		{"--at", "08:00"}, {"--after", "10m"},
	} {
		t.Run(strings.Join(options, " "), func(t *testing.T) {
			args := append([]string{"sign", "--base-url", base, "--session", session}, options...)
			var output, diagnostics bytes.Buffer
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			err := Run(ctx, args, strings.NewReader(""), &output, &diagnostics)
			var usage *usageError
			if !errors.As(err, &usage) || output.Len() != 0 || diagnostics.Len() != 0 {
				t.Fatalf("invalid cron accepted: output=%s diagnostics=%s err=%v", &output, &diagnostics, err)
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid options sent %d requests", calls.Load())
	}
}

type callbackWriter func([]byte)

func (w callbackWriter) Write(p []byte) (int, error) {
	w(p)
	return len(p), nil
}

func TestCronWaitsForTriggerAndCancellationMakesNoRequests(t *testing.T) {
	var calls atomic.Int32
	base, session := taskCLIFixture(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var output bytes.Buffer
	diagnostics := callbackWriter(func(p []byte) { cancel() })
	err := Run(ctx, []string{"sign", "--base-url", base, "--session", session, "--cron", "0 8 * * *"}, strings.NewReader(""), &output, diagnostics)
	if !errors.Is(err, context.Canceled) || calls.Load() != 0 || output.Len() != 0 {
		t.Fatalf("cron ran on startup or after cancellation: calls=%d output=%s err=%v", calls.Load(), &output, err)
	}
}

// fastCronSchedule 仅加速调度器测试；生产环境始终使用 cron 库解析表达式。
type fastCronSchedule time.Duration

func (s fastCronSchedule) Next(now time.Time) time.Time { return now.Add(time.Duration(s)) }

func fastCronPlan() *cronPlan {
	return &cronPlan{expression: "test schedule", location: time.UTC, schedule: fastCronSchedule(20 * time.Millisecond)}
}

func TestCronSkipsOverlappingRuns(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var runs, active, maximum atomic.Int32
	started := make(chan struct{}, 10)
	release := make(chan struct{})
	done := make(chan error, 1)
	a := &app{diagnostics: io.Discard}
	go func() {
		done <- a.runCron(ctx, fastCronPlan(), func(ctx context.Context) error {
			current := active.Add(1)
			defer active.Add(-1)
			if current > maximum.Load() {
				maximum.Store(current)
			}
			run := runs.Add(1)
			started <- struct{}{}
			if run == 1 {
				select {
				case <-release:
				case <-ctx.Done():
				}
			} else {
				cancel()
			}
			return nil
		})
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("cron did not trigger")
	}
	select {
	case <-started:
		t.Error("cron allowed overlapping runs")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-done; !errors.Is(err, context.Canceled) || runs.Load() != 2 || maximum.Load() != 1 {
		t.Fatalf("overlap guard or future trigger failed: runs=%d max=%d err=%v", runs.Load(), maximum.Load(), err)
	}
}

func TestCronCancellationWaitsForRunningJob(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		a := &app{diagnostics: io.Discard}
		done <- a.runCron(ctx, fastCronPlan(), func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			<-release
			return ctx.Err()
		})
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("cron did not trigger")
	}
	cancel()
	select {
	case err := <-done:
		t.Errorf("cron returned before running job finished: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestCronRecoversAndRunsAfterPanic(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var runs int
	var diagnostics bytes.Buffer
	a := &app{diagnostics: &diagnostics}
	err := a.runCron(ctx, fastCronPlan(), func(ctx context.Context) error {
		runs++
		if runs == 1 {
			panic("fixture panic")
		}
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) || runs != 2 || !strings.Contains(diagnostics.String(), "fixture panic") {
		t.Fatalf("panic permanently disabled cron: runs=%d diagnostics=%s err=%v", runs, &diagnostics, err)
	}
}

func TestCronTriggersFreshDiscoveryEachTimeAndContinuesAfterFailure(t *testing.T) {
	var lists, details, submissions atomic.Int32
	var earliest time.Time
	base, session := taskCLIFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if time.Now().Before(earliest) {
			t.Error("cron made a request before the first scheduled trigger")
		}
		if strings.HasSuffix(r.URL.Path, "/queryQdrw") {
			var query struct {
				BatchID    string          `json:"pcid"`
				Pagination json.RawMessage `json:"pagination"`
			}
			if err := json.NewDecoder(r.Body).Decode(&query); err != nil {
				t.Error(err)
			}
			batch := query.BatchID
			if query.Pagination != nil {
				batch = fmt.Sprintf("batch-%d", lists.Add(1))
			} else {
				details.Add(1)
			}
			writeTaskPage(t, w, []qdgj.Task{activeTaskFixture(batch)}, 1, 1)
			return
		}
		run := submissions.Add(1)
		var body struct {
			BatchID string `json:"pcid"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.BatchID != fmt.Sprintf("batch-%d", run) {
			t.Errorf("cron reused a stale batch: %+v (%v)", body, err)
		}
		if run == 1 {
			fmt.Fprint(w, `{"code":409,"meta":{"message":"fixture denied"}}`)
		} else {
			fmt.Fprint(w, `{"code":200,"data":{}}`)
		}
	})
	saved, err := qdgj.LoadSession(session)
	if err != nil {
		t.Fatal(err)
	}
	saved.Authorization = "old-fixture-token"
	if err := saved.Save(session); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	var output, diagnostics bytes.Buffer
	var reports int
	diagnosticWriter := callbackWriter(func(p []byte) {
		diagnostics.Write(p)
		if bytes.Contains(p, []byte("cron 签到已启动")) {
			earliest = time.Now().Truncate(time.Second).Add(time.Second)
			saved.Authorization = "fixture-token"
			if err := saved.Save(session); err != nil {
				t.Error(err)
			}
		}
	})
	outputWriter := callbackWriter(func(p []byte) {
		output.Write(p)
		reports++
		if reports == 2 {
			cancel()
		}
	})
	args := append(automaticSignArgs(base, session), "--cron", "@every 1s")
	err = Run(ctx, args, strings.NewReader(""), outputWriter, diagnosticWriter)
	if !errors.Is(err, context.Canceled) || lists.Load() != 2 || details.Load() != 2 || submissions.Load() != 2 || !strings.Contains(diagnostics.String(), "本轮签到失败") {
		t.Fatalf("cron did not recur after failure: lists=%d details=%d signs=%d diagnostics=%s err=%v", lists.Load(), details.Load(), submissions.Load(), &diagnostics, err)
	}
	decoder := json.NewDecoder(&output)
	for i := 1; i <= 2; i++ {
		var report automaticSignReport
		if err := decoder.Decode(&report); err != nil || report.Matched != 1 || len(report.Results) != 1 || report.Results[0].BatchID != fmt.Sprintf("batch-%d", i) || i == 1 && report.Failed != 1 || i == 2 && report.Succeeded != 1 {
			t.Fatalf("wrong per-trigger report: %+v (%v)", report, err)
		}
	}
}

func TestCronRequiresSessionAndWritableDiagnostics(t *testing.T) {
	var calls atomic.Int32
	base, session := taskCLIFixture(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) })
	for _, validSession := range []bool{false, true} {
		path := session
		if !validSession {
			path = filepath.Join(t.TempDir(), "missing.json")
		}
		args := []string{"sign", "--base-url", base, "--session", path, "--cron", "0 8 * * *"}
		err := run(args, strings.NewReader(""), io.Discard, closedOutput{})
		if err == nil || validSession && !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("invalid startup ignored: validSession=%t err=%v", validSession, err)
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid startup sent %d requests", calls.Load())
	}
}

func TestCronSessionFileErrorDoesNotUseStaleSession(t *testing.T) {
	var calls atomic.Int32
	base, session := taskCLIFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		writeTaskPage(t, w, []qdgj.Task{}, 1, 1)
	})
	before, err := os.ReadFile(session)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	var diagnostics bytes.Buffer
	diagnosticWriter := callbackWriter(func(p []byte) {
		diagnostics.Write(p)
		if bytes.Contains(p, []byte("cron 签到已启动")) {
			if err := os.WriteFile(session, []byte("invalid fixture session"), 0600); err != nil {
				t.Error(err)
			}
		} else if bytes.Contains(p, []byte("cron 加载最新会话")) {
			if err := os.WriteFile(session, before, 0600); err != nil {
				t.Error(err)
			}
		}
	})
	outputWriter := callbackWriter(func(p []byte) { cancel() })
	args := append(automaticSignArgs(base, session), "--cron", "@every 1s")
	err = Run(ctx, args, strings.NewReader(""), outputWriter, diagnosticWriter)
	if !errors.Is(err, context.Canceled) || calls.Load() != 1 || !strings.Contains(diagnostics.String(), "cron 加载最新会话") {
		t.Fatalf("invalid session reused or cron stopped: calls=%d diagnostics=%s err=%v", calls.Load(), &diagnostics, err)
	}
}

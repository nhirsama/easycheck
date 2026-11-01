package cli

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/robfig/cron/v3"
)

const defaultCronTimezone = "Asia/Shanghai"

type cronOptions struct {
	expression string
	timezone   string
}

type cronPlan struct {
	expression string
	location   *time.Location
	schedule   cron.Schedule
}

func (o *cronOptions) bind(c *command) {
	c.flags.StringVar(&o.expression, "cron", "", "常驻按五段 cron `EXPR` 自动签到，例如 '0 8 * * *'（每天 08:00）")
	c.flags.StringVar(&o.timezone, "timezone", defaultCronTimezone, "cron 使用的 `ZONE`，例如 Asia/Shanghai 或 UTC")
}

func (o cronOptions) resolve(c *command) (*cronPlan, error) {
	if !c.flagProvided("cron") {
		if c.flagProvided("timezone") {
			return nil, errors.New("--timezone 须与 --cron 一起使用")
		}
		return nil, nil
	}
	expression, timezone := strings.TrimSpace(o.expression), strings.TrimSpace(o.timezone)
	if expression == "" {
		return nil, errors.New("--cron 不能为空")
	}
	if timezone == "" || timezone == "Local" {
		return nil, errors.New("--timezone 请提供明确的时区名称，例如 Asia/Shanghai 或 UTC")
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("无效的 --timezone %q", timezone)
	}
	// 时区统一由 --timezone 指定，也避免库解析不完整 TZ= 前缀时 panic。
	if strings.HasPrefix(expression, "TZ=") || strings.HasPrefix(expression, "CRON_TZ=") {
		return nil, errors.New("请用 --timezone 指定时区，不要在 --cron 中添加 TZ= 或 CRON_TZ=")
	}
	if strings.HasPrefix(expression, "@every ") {
		duration, err := time.ParseDuration(strings.TrimSpace(strings.TrimPrefix(expression, "@every ")))
		if err != nil || duration < time.Second {
			return nil, errors.New("--cron 的 @every 间隔须至少为 1s")
		}
	}
	schedule, err := cron.ParseStandard(expression)
	if err != nil {
		return nil, fmt.Errorf("无效的 --cron（分 时 日 月 星期，共五段）：%w", err)
	}
	if schedule.Next(time.Now().In(location)).IsZero() {
		return nil, errors.New("--cron 在未来没有有效的执行时间")
	}
	return &cronPlan{expression: expression, location: location, schedule: schedule}, nil
}

func (a *app) runCron(ctx context.Context, plan *cronPlan, run func(context.Context) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(a.diagnostics, "cron 签到已启动：%s（%s），下次执行 %s；Ctrl+C 可停止。\n", plan.expression, plan.location, plan.schedule.Next(time.Now().In(plan.location)).Format(time.RFC3339)); err != nil {
		return fmt.Errorf("输出 cron 提示：%w", err)
	}
	logger := log.New(a.diagnostics, "", log.LstdFlags)
	cronLogger := cron.PrintfLogger(logger)
	scheduler := cron.New(
		cron.WithLocation(plan.location),
		cron.WithLogger(cronLogger),
		// Recover 放在 Skip 内层，确保 panic 后也能释放重叠保护。
		cron.WithChain(cron.SkipIfStillRunning(cronLogger), cron.Recover(cronLogger)),
	)
	scheduler.Schedule(plan.schedule, cron.FuncJob(func() {
		if ctx.Err() != nil {
			return
		}
		logger.Printf("cron 触发：%s", time.Now().In(plan.location).Format(time.RFC3339))
		if err := run(ctx); err != nil && ctx.Err() == nil {
			logger.Printf("本轮签到失败：%v；等待下次 cron 触发", err)
		}
	}))
	scheduler.Start()
	<-ctx.Done()
	// HTTP 请求使用同一 ctx；取消后等待当前任务收尾，不遗留后台请求。
	<-scheduler.Stop().Done()
	return ctx.Err()
}

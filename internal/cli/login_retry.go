package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"easycheck/qdgj"
)

type loginRetryOptions struct {
	retries  int
	interval time.Duration
}

func (o *loginRetryOptions) bind(c *command) {
	c.flags.IntVar(&o.retries, "retries", 3, "登录失败后的重试 `COUNT`，0 表示不重试；默认 3，可由 EASYCHECK_LOGIN_RETRIES 覆盖")
	c.flags.DurationVar(&o.interval, "retry-interval", time.Minute, "登录失败后的重试间隔 `DURATION`；默认 1m，可由 EASYCHECK_LOGIN_RETRY_INTERVAL 覆盖")
}

func (o *loginRetryOptions) resolve(c *command) error {
	if value := strings.TrimSpace(os.Getenv("EASYCHECK_LOGIN_RETRIES")); value != "" && !c.flagProvided("retries") {
		var err error
		o.retries, err = strconv.Atoi(value)
		if err != nil {
			return errors.New("EASYCHECK_LOGIN_RETRIES 须为非负整数")
		}
	}
	if value := strings.TrimSpace(os.Getenv("EASYCHECK_LOGIN_RETRY_INTERVAL")); value != "" && !c.flagProvided("retry-interval") {
		var err error
		o.interval, err = time.ParseDuration(value)
		if err != nil {
			return errors.New("EASYCHECK_LOGIN_RETRY_INTERVAL 须为大于零的时长，例如 1m")
		}
	}
	if o.retries < 0 {
		return errors.New("--retries / EASYCHECK_LOGIN_RETRIES 须为非负整数")
	}
	if o.interval <= 0 {
		return errors.New("--retry-interval / EASYCHECK_LOGIN_RETRY_INTERVAL 须大于零")
	}
	return nil
}

func (a *app) loginWithRetry(ctx context.Context, client *qdgj.Client, options loginRetryOptions, notifier *resultNotifier) error {
	for retry := 0; ; retry++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := client.Login(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			remaining := options.retries - retry
			a.notifyLoginFailure(ctx, notifier, err, retry+1, remaining, options.interval, time.Now())
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if remaining == 0 {
				return fmt.Errorf("登录失败，已重试 %d 次：%w", retry, err)
			}
			fmt.Fprintf(a.diagnostics, "登录失败（第 %d 次尝试）：%v；将在 %s 后重试（剩余 %d 次）。\n", retry+1, err, options.interval, remaining)
			timer := time.NewTimer(options.interval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
			continue
		}
		return nil
	}
}

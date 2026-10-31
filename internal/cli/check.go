package cli

import (
	"context"
	"fmt"
	"strings"

	"easycheck/qdgj"
)

var checkSpec = commandSpec{
	name:     "check",
	summary:  "检查手机端入口和验证码状态",
	syntax:   "[选项]",
	examples: []string{"easycheck check", "easycheck check --timeout 10s"},
	setup: func(a *app, c *command) {
		c.run = func(ctx context.Context) error { return a.check(ctx, c) }
	},
}

func (a *app) check(ctx context.Context, c *command) error {
	client, err := qdgj.NewClient(c.config)
	if err != nil {
		return err
	}
	required, err := client.CaptchaRequired(ctx)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(a.output, "手机端入口可访问：%s#/mobile/login\n需要验证码：%t\n", strings.TrimRight(c.config.BaseURL, "/")+"/", required)
	return err
}

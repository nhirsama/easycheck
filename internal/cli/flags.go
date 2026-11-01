package cli

import (
	"errors"
	"flag"
	"strings"
)

func (c *command) bindCommonFlags() {
	c.stringFlag(&c.config.BaseURL, "base-url", c.config.BaseURL, "站点基础 `URL`，默认读取 EASYCHECK_BASE_URL")
	c.stringFlag(&c.config.SessionFile, "session", c.config.SessionFile, "会话 `FILE`，默认读取 EASYCHECK_SESSION_FILE 或 session.json")
	c.flags.DurationVar(&c.config.Timeout, "timeout", c.config.Timeout, "单次 HTTP 请求超时 `DURATION`")
}

func (c *command) stringFlag(value *string, name, initial, description string) {
	c.flags.StringVar(value, name, initial, description)
	c.flags.Lookup(name).DefValue = ""
}

func (c *command) flagProvided(name string) bool {
	provided := false
	c.flags.Visit(func(f *flag.Flag) {
		if f.Name == name {
			provided = true
		}
	})
	return provided
}

func (c *command) validateConfig() error {
	if strings.TrimSpace(c.config.BaseURL) == "" {
		return errors.New("请通过 --base-url 或 EASYCHECK_BASE_URL 设置站点基础 URL")
	}
	if strings.TrimSpace(c.config.SessionFile) == "" {
		return errors.New("--session 会话文件路径不能为空")
	}
	if c.config.Timeout <= 0 {
		return errors.New("--timeout 必须大于零")
	}
	return nil
}

func isCommonFlag(name string) bool {
	return name == "base-url" || name == "session" || name == "timeout"
}

type responseOptions struct {
	json bool
	out  string
}

func (o *responseOptions) bind(flags *flag.FlagSet) {
	flags.BoolVar(&o.json, "json", false, "输出完整 JSON 响应")
	flags.StringVar(&o.out, "out", "", "将原始响应保存到权限 0600 的 `FILE`")
}

type taskOptions struct {
	batchID string
}

func (o *taskOptions) bind(flags *flag.FlagSet) {
	flags.StringVar(&o.batchID, "pcid", "", "批次 `ID`，从 list 结果取得")
}

func (o taskOptions) validate() error {
	if strings.TrimSpace(o.batchID) == "" {
		return errors.New("请提供 --pcid 批次 ID")
	}
	return nil
}

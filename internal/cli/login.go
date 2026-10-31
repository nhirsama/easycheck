package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"easycheck/qdgj"
)

var loginSpec = commandSpec{
	name:     "login",
	summary:  "登录并保存会话",
	syntax:   "[选项]",
	examples: []string{"easycheck login", "easycheck login --username example-user --password-stdin"},
	setup:    (*app).configureLogin,
}

func (a *app) configureLogin(c *command) {
	c.stringFlag(&c.config.Username, "username", c.config.Username, "登录 `USER`，默认读取 EASYCHECK_USERNAME")
	passwordStdin := c.flags.Bool("password-stdin", false, "从标准输入第一行读取密码")
	captchaPath := c.flags.String("captcha-file", "captcha.png", "将验证码图片保存到 `FILE`")
	check := c.flags.Bool("check", false, "检查入口与验证码状态")
	c.hiddenFlags = map[string]bool{"check": true}
	c.run = func(ctx context.Context) error {
		if *check {
			return a.check(ctx, c)
		}
		reader := bufio.NewReader(a.input)
		if *passwordStdin {
			var err error
			c.config.Password, err = readLine(reader)
			if err != nil {
				return fmt.Errorf("读取密码：%w", err)
			}
		}
		c.config.CaptchaSolver = func(_ context.Context, image []byte) (string, error) {
			if err := qdgj.WritePrivateFile(*captchaPath, image); err != nil {
				return "", fmt.Errorf("保存验证码：%w", err)
			}
			fmt.Fprintf(a.diagnostics, "验证码已保存到 %s，请查看图片后输入验证码：", *captchaPath)
			return readLine(reader)
		}
		client, err := qdgj.NewClient(c.config)
		if err != nil {
			return err
		}
		if _, err := client.Login(ctx); err != nil {
			return err
		}
		if err := client.SaveSession(); err != nil {
			return fmt.Errorf("登录成功，但保存会话失败：%w", err)
		}
		_, err = fmt.Fprintf(a.output, "登录成功，会话已保存到 %s\n", c.config.SessionFile)
		if err != nil {
			return fmt.Errorf("登录成功，但输出结果失败：%w", err)
		}
		return nil
	}
}

func readLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && len(line) > 0) {
		return "", err
	}
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), nil
}

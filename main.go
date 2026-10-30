package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"easycheck/qdgj"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		os.Exit(1)
	}
}

func run(args []string, input io.Reader, output, diagnostics io.Writer) error {
	flags := flag.NewFlagSet("easycheck", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	base := flags.String("base-url", strings.TrimSpace(os.Getenv("EASYCHECK_BASE_URL")), "站点基础 URL，也可设置 EASYCHECK_BASE_URL")
	username := flags.String("username", os.Getenv("EASYCHECK_USERNAME"), "学号/职工号，也可设置 EASYCHECK_USERNAME")
	passwordStdin := flags.Bool("password-stdin", false, "从标准输入第一行读取密码")
	check := flags.Bool("check", false, "只检查页面与验证码状态，不提交登录")
	sessionPath := flags.String("session", "session.json", "成功登录后保存的会话文件")
	captchaPath := flags.String("captcha-file", "captcha.png", "启用验证码时保存的图片")
	timeout := flags.Duration("timeout", 30*time.Second, "单次 HTTP 请求超时")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("不接受位置参数，请使用 -username 等选项")
	}
	if *timeout <= 0 {
		return errors.New("-timeout 必须大于零")
	}
	reader := bufio.NewReader(input)
	password := os.Getenv("EASYCHECK_PASSWORD")
	if !*check && *passwordStdin {
		var err error
		password, err = readLine(reader)
		if err != nil {
			return fmt.Errorf("读取密码：%w", err)
		}
	}
	client, err := qdgj.NewClient(qdgj.Options{BaseURL: *base, Timeout: *timeout})
	if err != nil {
		return err
	}
	ctx := context.Background()
	if *check {
		required, err := client.CaptchaRequired(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(output, "手机端入口可访问：%s#/mobile/login\n需要验证码：%t\n", strings.TrimRight(*base, "/")+"/", required)
		return nil
	}
	saved, err := client.Login(ctx, qdgj.LoginOptions{Username: *username, Password: password,
		CaptchaSolver: func(_ context.Context, image []byte) (string, error) {
			if err := qdgj.WritePrivateFile(*captchaPath, image); err != nil {
				return "", fmt.Errorf("保存验证码：%w", err)
			}
			fmt.Fprintf(diagnostics, "验证码已保存到 %s，请查看图片后输入验证码：", *captchaPath)
			return readLine(reader)
		},
	})
	if err != nil {
		return err
	}
	if err := saved.Save(*sessionPath); err != nil {
		return fmt.Errorf("登录成功，但保存会话失败：%w", err)
	}
	fmt.Fprintf(output, "登录成功，会话已保存到 %s\n", *sessionPath)
	return nil
}

func readLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && len(line) > 0) {
		return "", err
	}
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), nil
}

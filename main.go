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

	"easycheck/qdgj"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		os.Exit(1)
	}
}

func run(args []string, input io.Reader, output, diagnostics io.Writer) error {
	if len(args) > 0 {
		switch args[0] {
		case "help":
			if len(args) == 2 {
				switch args[1] {
				case "login", "check", "list", "detail", "sign":
					return run([]string{args[1], "-help"}, input, output, diagnostics)
				default:
					return fmt.Errorf("未知命令 %q", args[1])
				}
			}
			if len(args) > 2 {
				return errors.New("用法：easycheck help [命令]")
			}
			_, err := fmt.Fprint(output, cliUsage)
			return err
		case "-h", "--help", "-help":
			_, err := fmt.Fprint(output, cliUsage)
			return err
		case "login", "check":
			return runLogin(args[0], args[1:], input, output, diagnostics)
		case "list", "detail", "sign":
			return runTasks(args[0], args[1:], output, diagnostics)
		default:
			if !strings.HasPrefix(args[0], "-") {
				return fmt.Errorf("未知命令 %q，请使用 easycheck help 查看用法", args[0])
			}
		}
	}
	return runLogin("login", args, input, output, diagnostics)
}

const cliUsage = `用法：easycheck <命令> [选项]

命令：
  login   登录并保存会话（默认命令）
  check   检查手机端入口和验证码状态
  list    列出签到任务，支持分页
  detail  查询任务及批次详情
  sign    从任务允许地点生成定位并签到，支持自定义定位

公共选项：
  -base-url URL    应用根地址，默认读取 EASYCHECK_BASE_URL
  -session FILE    会话文件，默认读取 EASYCHECK_SESSION_FILE 或 session.json
  -timeout TIME   单次请求超时，默认 30s

使用 easycheck <命令> -help 查看该命令的选项。
`

func runLogin(command string, args []string, input io.Reader, output, diagnostics io.Writer) error {
	config := qdgj.ConfigFromEnv()
	flags := flag.NewFlagSet("easycheck "+command, flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	flags.StringVar(&config.BaseURL, "base-url", config.BaseURL, "站点基础 URL，也可设置 EASYCHECK_BASE_URL")
	flags.StringVar(&config.Username, "username", config.Username, "账号，也可设置 EASYCHECK_USERNAME")
	passwordStdin := flags.Bool("password-stdin", false, "从标准输入第一行读取密码")
	check := command == "check"
	if command == "login" {
		flags.BoolVar(&check, "check", false, "检查页面与验证码状态")
	}
	flags.StringVar(&config.SessionFile, "session", config.SessionFile, "成功登录后保存的会话文件")
	captchaPath := flags.String("captcha-file", "captcha.png", "启用验证码时保存的图片")
	flags.DurationVar(&config.Timeout, "timeout", config.Timeout, "单次 HTTP 请求超时")
	flags.Lookup("base-url").DefValue = ""
	flags.Lookup("username").DefValue = ""
	flags.Lookup("session").DefValue = ""
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("不接受位置参数，请使用 -username 等选项")
	}
	if config.Timeout <= 0 {
		return errors.New("-timeout 必须大于零")
	}
	reader := bufio.NewReader(input)
	if !check && *passwordStdin {
		var err error
		config.Password, err = readLine(reader)
		if err != nil {
			return fmt.Errorf("读取密码：%w", err)
		}
	}
	config.CaptchaSolver = func(_ context.Context, image []byte) (string, error) {
		if err := qdgj.WritePrivateFile(*captchaPath, image); err != nil {
			return "", fmt.Errorf("保存验证码：%w", err)
		}
		fmt.Fprintf(diagnostics, "验证码已保存到 %s，请查看图片后输入验证码：", *captchaPath)
		return readLine(reader)
	}
	client, err := qdgj.NewClient(config)
	if err != nil {
		return err
	}
	ctx := context.Background()
	if check {
		required, err := client.CaptchaRequired(ctx)
		if err != nil {
			return err
		}
		fmt.Fprintf(output, "手机端入口可访问：%s#/mobile/login\n需要验证码：%t\n", strings.TrimRight(config.BaseURL, "/")+"/", required)
		return nil
	}
	_, err = client.Login(ctx)
	if err != nil {
		return err
	}
	if err := client.SaveSession(); err != nil {
		return fmt.Errorf("登录成功，但保存会话失败：%w", err)
	}
	fmt.Fprintf(output, "登录成功，会话已保存到 %s\n", config.SessionFile)
	return nil
}

func readLine(reader *bufio.Reader) (string, error) {
	line, err := reader.ReadString('\n')
	if err != nil && !(errors.Is(err, io.EOF) && len(line) > 0) {
		return "", err
	}
	return strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r"), nil
}

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"easycheck/qdgj"
)

var signSpec = commandSpec{
	name:        "sign",
	summary:     "自动签到当前任务，支持 cron 周期调度",
	syntax:      "[--pcid ID] [--cron EXPR] [选项]",
	description: "默认立即查询任务列表，为当前签到窗口内未签到的定位任务逐个签到。\n--cron 使用五段表达式常驻调度，配置 EASYCHECK_USERNAME 和 EASYCHECK_PASSWORD 后每次触发先重新登录并保存会话，再查询任务，无需提前登录或知道批次 ID。\n未配置账号密码时，每次触发重新读取已有会话。\n可选 --pcid 只处理指定批次；不带 --cron 时手动立即提交该批次。\n默认在允许地点半径的 90% 内生成坐标，通过原站地图服务自动解析位置名称。\n自定义定位时同时提供 --longitude、--latitude，--address 可选。\ncron 运行时跳过重叠触发，Ctrl+C 可停止。\ncron 登录失败、签到成功或失败时可按 EASYCHECK_TG_BOT_TOKEN/EASYCHECK_TG_CHAT_ID 或 EASYCHECK_FEISHU_WEBHOOK_URL 发送通知。",
	examples: []string{
		"easycheck sign",
		"easycheck sign --pcid '<batch-id>'",
		"easycheck sign --cron '0 8 * * *'",
		"easycheck sign --cron '*/5 * * * *'",
		"easycheck sign --pcid '<batch-id>' --longitude 10.5 --latitude 20.5",
		"easycheck sign --pcid '<batch-id>' --address '<address>' --longitude 10.5 --latitude 20.5",
	},
	setup: (*app).configureSign,
}

func (a *app) configureSign(c *command) {
	var task taskOptions
	var request qdgj.SignInRequest
	var response responseOptions
	var photoID string
	var scheduling cronOptions
	var retry loginRetryOptions
	var plan *cronPlan
	var notifier *resultNotifier
	var autoLogin bool
	task.bind(c.flags)
	c.flags.Lookup("pcid").Usage = "可选批次 `ID`；省略时自动发现当前任务"
	response.bind(c.flags)
	scheduling.bind(c)
	retry.bind(c)
	c.flags.StringVar(&request.Location.Address, "address", "", "签到 `ADDRESS`，与经纬度一起提供时跳过坐标地址解析")
	c.flags.Float64Var(&request.Location.Longitude, "longitude", 0, "签到经度 `NUMBER`，与纬度一起提供")
	c.flags.Float64Var(&request.Location.Latitude, "latitude", 0, "签到纬度 `NUMBER`，与经度一起提供")
	c.flags.StringVar(&photoID, "photo-id", "", "已上传照片的文件 `ID`")
	c.flags.BoolVar(&request.OutsideRange, "outside-range", false, "使用范围外签到分支")
	c.flags.StringVar(&request.Remark, "remark", "", "范围外签到 `TEXT`，最多 100 个 UTF-16 字符")
	c.validate = func() error {
		var err error
		plan, err = scheduling.resolve(c)
		if err != nil {
			return err
		}
		if plan != nil {
			autoLogin = c.config.Username != "" || c.config.Password != ""
			if autoLogin {
				if strings.TrimSpace(c.config.Username) == "" || c.config.Password == "" {
					return errors.New("cron 自动登录须同时设置 EASYCHECK_USERNAME 和 EASYCHECK_PASSWORD")
				}
			}
			if err := retry.resolve(c); err != nil {
				return err
			}
			notifier, err = notifierFromEnv()
			if err != nil {
				return err
			}
		} else if c.flagProvided("retries") || c.flagProvided("retry-interval") {
			return errors.New("签到命令的 --retries 和 --retry-interval 须与 --cron 一起使用")
		}
		if c.flagProvided("pcid") {
			if err := task.validate(); err != nil {
				return err
			}
		}
		longitude, latitude := c.flagProvided("longitude"), c.flagProvided("latitude")
		if !c.flagProvided("address") && !longitude && !latitude {
			request.UseAllowedLocation = true
		} else if !longitude || !latitude {
			return errors.New("自定义定位须同时提供 --longitude 和 --latitude，--address 可选")
		} else if c.flagProvided("address") && strings.TrimSpace(request.Location.Address) == "" {
			return errors.New("--address 不能为空；省略此选项可按坐标自动解析地址")
		}
		request.BatchID = strings.TrimSpace(task.batchID)
		if photoID != "" {
			request.PhotoID = &photoID
		}
		return request.ValidateOptions()
	}
	c.run = func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var client *qdgj.Client
		var err error
		if plan != nil && autoLogin {
			// 启动时仅校验客户端配置，等 cron 触发后再建立会话。
			client, err = qdgj.NewClient(c.config)
		} else {
			client, err = c.sessionClient()
		}
		if err != nil {
			if plan != nil {
				a.notifyCronResult(ctx, notifier, automaticSignReport{}, err, time.Now().In(plan.location))
			}
			return err
		}
		if plan != nil {
			return a.runCron(ctx, plan, func(ctx context.Context) error {
				var report automaticSignReport
				current := client
				var err error
				if autoLogin {
					// 每轮使用独立 CookieJar，不复用等待期间可能过期的会话。
					current, err = qdgj.NewClient(c.config)
					if err == nil {
						err = a.loginWithRetry(ctx, current, retry, notifier)
					}
					if err == nil {
						if saveErr := current.SaveSession(); saveErr != nil {
							err = fmt.Errorf("cron 登录成功，但保存会话失败：%w", saveErr)
						}
					}
				} else if err = current.LoadSession(); err != nil {
					err = fmt.Errorf("cron 加载最新会话：%w；请执行 easycheck login，或配置 EASYCHECK_USERNAME 和 EASYCHECK_PASSWORD 自动登录", err)
				}
				if err == nil {
					report, err = a.signCurrentTasks(ctx, current, request, response)
				}
				// 通知失败不改变签到结果，也不重试已成功的签到。
				a.notifyCronResult(ctx, notifier, report, err, time.Now().In(plan.location))
				return err
			})
		}
		if request.BatchID == "" {
			_, err := a.signCurrentTasks(ctx, client, request, response)
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		result, err := client.SignIn(ctx, request)
		if err != nil {
			return err
		}
		return a.writeResponse("sign", response, result.RawResponse, func(output io.Writer) {
			fmt.Fprintf(output, "签到成功（code=%d）\n", result.Code)
			if result.Meta.Message != "" {
				fmt.Fprintln(output, result.Meta.Message)
			}
		})
	}
}

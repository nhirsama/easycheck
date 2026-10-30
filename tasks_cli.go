package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"easycheck/qdgj"
)

func runTasks(command string, args []string, output, diagnostics io.Writer) error {
	config := qdgj.ConfigFromEnv()
	flags := flag.NewFlagSet("easycheck "+command, flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	if command == "sign" {
		flags.Usage = func() {
			fmt.Fprintln(diagnostics, "用法：easycheck sign -rwid <任务ID> -pcid <批次ID> [选项]")
			fmt.Fprintln(diagnostics, "默认读取允许地点的地址，在其半径的 90% 内随机生成坐标。")
			fmt.Fprintln(diagnostics, "自定义定位时同时传入 -address、-longitude、-latitude。")
			flags.PrintDefaults()
		}
	}
	flags.StringVar(&config.BaseURL, "base-url", config.BaseURL, "站点基础 URL，也可设置 EASYCHECK_BASE_URL")
	flags.StringVar(&config.SessionFile, "session", config.SessionFile, "已保存的登录会话")
	flags.DurationVar(&config.Timeout, "timeout", config.Timeout, "单次 HTTP 请求超时")
	jsonOutput := flags.Bool("json", false, "输出完整 JSON 响应")
	out := flags.String("out", "", "将原始响应保存到权限 0600 的 JSON 文件")
	var options qdgj.ListOptions
	var request qdgj.SignInRequest
	var photoID string
	switch command {
	case "list":
		flags.IntVar(&options.Page, "page", 1, "列表页码，从 1 开始")
		flags.IntVar(&options.PageSize, "page-size", 10, "列表每页数量")
	case "detail", "sign":
		flags.StringVar(&request.TaskID, "rwid", "", "任务 ID，从 list 结果取得")
		flags.StringVar(&request.BatchID, "pcid", "", "批次 ID，从 list 结果取得")
		if command == "sign" {
			flags.StringVar(&request.Location.Address, "address", "", "签到地址，与经纬度一起覆盖任务默认地点")
			flags.Float64Var(&request.Location.Longitude, "longitude", 0, "签到经度，与地址、纬度一起提供")
			flags.Float64Var(&request.Location.Latitude, "latitude", 0, "签到纬度，与地址、经度一起提供")
			flags.StringVar(&photoID, "photo-id", "", "已上传照片的文件标识")
			flags.BoolVar(&request.OutsideRange, "outside-range", false, "使用范围外签到分支")
			flags.StringVar(&request.Remark, "remark", "", "范围外签到备注，最多 100 个 UTF-16 字符")
		}
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("参数须使用选项名称，请使用 -help 查看用法")
	}
	if config.Timeout <= 0 {
		return errors.New("-timeout 必须大于零")
	}
	if command == "list" && (options.Page < 1 || options.PageSize < 1) {
		return errors.New("-page 和 -page-size 必须大于零")
	}
	if command != "list" && strings.TrimSpace(request.TaskID) == "" {
		return errors.New("请提供 -rwid 任务 ID")
	}
	if command == "sign" {
		provided := make(map[string]bool)
		flags.Visit(func(f *flag.Flag) { provided[f.Name] = true })
		if strings.TrimSpace(request.BatchID) == "" {
			return errors.New("签到须提供 -rwid 和 -pcid")
		}
		if !provided["address"] && !provided["longitude"] && !provided["latitude"] {
			request.UseAllowedLocation = true
		} else if strings.TrimSpace(request.Location.Address) == "" || !provided["longitude"] || !provided["latitude"] {
			return errors.New("自定义定位须同时提供 -address、-longitude 和 -latitude")
		}
		if photoID != "" {
			request.PhotoID = &photoID
		}
	}
	client, err := qdgj.NewClient(config)
	if err != nil {
		return err
	}
	if err := client.LoadSession(); err != nil {
		return fmt.Errorf("加载会话：%w；请先执行 easycheck login", err)
	}
	ctx := context.Background()
	var raw []byte
	var display strings.Builder
	switch command {
	case "list":
		result, err := client.ListTasks(ctx, options)
		if err != nil {
			return err
		}
		raw = result.RawResponse
		if !*jsonOutput {
			fmt.Fprintf(&display, "第 %d 页，每页 %d 条；本页 %d 条，共 %d 条，%d 页\n", result.PageNum, result.PageSize, len(result.Tasks), result.Total, result.PageCount)
			for i, task := range result.Tasks {
				fmt.Fprintf(&display, "%d. %s | %s | %s | %s\n   %s ~ %s\n   rwid=%s\n   pcid=%s\n", i+1, task.Name, task.Method, task.Status, task.CheckInStatus, task.StartTime, task.EndTime, task.TaskID, task.BatchID)
			}
		}
	case "detail":
		result, err := client.GetTask(ctx, request.TaskID, request.BatchID)
		if err != nil {
			return err
		}
		raw = result.RawResponse
		if !*jsonOutput {
			printTaskDetail(&display, result.Task)
		}
	case "sign":
		result, err := client.SignIn(ctx, request)
		if err != nil {
			return err
		}
		raw = result.RawResponse
		if !*jsonOutput {
			fmt.Fprintf(&display, "签到成功（code=%d）\n", result.Code)
			if result.Meta.Message != "" {
				fmt.Fprintln(&display, result.Meta.Message)
			}
		}
	}
	if *jsonOutput {
		encoder := json.NewEncoder(&display)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(json.RawMessage(raw)); err != nil {
			return fmt.Errorf("%s 操作成功，但输出响应失败：%w", command, err)
		}
	}
	if _, err := io.WriteString(output, display.String()); err != nil {
		return fmt.Errorf("%s 操作成功，但输出响应失败：%w", command, err)
	}
	if *out != "" {
		if err := qdgj.WritePrivateFile(*out, raw); err != nil {
			return fmt.Errorf("%s 操作成功，但保存响应失败：%w", command, err)
		}
	}
	return nil
}

func printTaskDetail(output io.Writer, task qdgj.Task) {
	fmt.Fprintf(output, "任务：%s\nrwid=%s\npcid=%s\n类型：%s\n签到方式：%s\n频率：%s\n批次状态：%s\n签到状态：%s\n时间：%s ~ %s\n位置规则：%s\n照片要求：%s %s\n", task.Name, task.TaskID, task.BatchID, task.Type, task.Method, task.Frequency, task.Status, task.CheckInStatus, task.StartTime, task.EndTime, task.LocationRule, task.PhotoRequired, task.PhotoRequirement)
	if task.Address != "" || task.Longitude != nil || task.Latitude != nil {
		fmt.Fprintf(output, "签到位置：%s（经度 %s，纬度 %s）\n", task.Address, formatNumber(task.Longitude), formatNumber(task.Latitude))
	}
	if task.PhotoID != nil {
		fmt.Fprintf(output, "照片：%s\n", *task.PhotoID)
	}
	for i, location := range task.AllowedLocations {
		fmt.Fprintf(output, "允许地点 %d：%s %s（经度 %s，纬度 %s，半径 %s 米）\n", i+1, location.Address, location.Center, formatNumber(location.Longitude), formatNumber(location.Latitude), formatNumber(location.Radius))
	}
}

func formatNumber(value *qdgj.Number) string {
	if value == nil {
		return "未知"
	}
	return fmt.Sprintf("%g", float64(*value))
}

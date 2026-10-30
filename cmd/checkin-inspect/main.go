// checkin-inspect 只调用任务查询方法，不提供签到提交操作。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"easycheck/qdgj"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "错误：", err)
		os.Exit(1)
	}
}

func run(args []string, output, diagnostics io.Writer) error {
	flags := flag.NewFlagSet("checkin-inspect", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	sessionPath := flags.String("session", "session.json", "已保存的登录会话")
	taskID := flags.String("rwid", "", "查询指定任务；不提供时查询任务列表")
	batchID := flags.String("pcid", "", "查询指定批次，与 rwid 一起使用")
	page := flags.Int("page", 1, "列表页码，从 1 开始")
	pageSize := flags.Int("page-size", 10, "列表每页数量")
	out := flags.String("out", "", "可选：将原始查询响应保存到权限 0600 的 JSON 文件")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 || *page < 1 || *pageSize < 1 || (*batchID != "" && *taskID == "") {
		return errors.New("请检查参数：页码和每页数量须大于零，pcid 须与 rwid 一起使用")
	}
	saved, err := qdgj.LoadSession(*sessionPath)
	if err != nil {
		return err
	}
	client, err := qdgj.NewClientFromSession(saved, qdgj.Options{})
	if err != nil {
		return err
	}
	var tasks []qdgj.Task
	var raw []byte
	if *taskID == "" {
		result, err := client.ListTasks(context.Background(), qdgj.ListOptions{Page: *page, PageSize: *pageSize})
		if err != nil {
			return err
		}
		tasks, raw = result.Tasks, result.RawResponse
	} else {
		result, err := client.GetTask(context.Background(), *taskID, *batchID)
		if err != nil {
			return err
		}
		tasks, raw = []qdgj.Task{result.Task}, result.RawResponse
	}
	if *out != "" {
		if err := qdgj.WritePrivateFile(*out, raw); err != nil {
			return fmt.Errorf("保存查询结果：%w", err)
		}
	}
	fmt.Fprintf(output, "查询到 %d 条任务/批次（只读，未提交签到）\n", len(tasks))
	for i, item := range tasks {
		fmt.Fprintf(output, "%d. %s | %s | %s | %s\n   %s ~ %s\n   rwid=%s\n   pcid=%s\n", i+1, item.Name, item.Method, item.Status, item.CheckInStatus, item.StartTime, item.EndTime, item.TaskID, item.BatchID)
	}
	return nil
}

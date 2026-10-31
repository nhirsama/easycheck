package cli

import (
	"context"
	"errors"
	"io"
	"strings"

	"easycheck/qdgj"
)

var inspectSpec = commandSpec{
	program:     "checkin-inspect",
	summary:     "查询签到任务和批次详情",
	syntax:      "[--rwid ID] [--pcid ID] [选项]",
	description: "提供 --rwid 时查询详情，其余情况查询列表。",
	examples:    []string{"checkin-inspect --page 1", "checkin-inspect --rwid '<task-id>' --pcid '<batch-id>'"},
	setup:       (*app).configureInspect,
}

// ExecuteInspect 使用统一的查询实现执行独立查询入口。
func ExecuteInspect(ctx context.Context, args []string, input io.Reader, output, diagnostics io.Writer) int {
	a := &app{input: input, output: output, diagnostics: diagnostics}
	return exitCode(a.runCommand(ctx, inspectSpec, args), diagnostics)
}

func (a *app) configureInspect(c *command) {
	var task taskOptions
	var options qdgj.ListOptions
	var response responseOptions
	task.bind(c.flags)
	bindPagination(c, &options)
	response.bind(c.flags)
	c.validate = func() error {
		if err := validatePagination(options); err != nil {
			return err
		}
		if task.batchID != "" && strings.TrimSpace(task.taskID) == "" {
			return errors.New("--pcid 须与 --rwid 一起使用")
		}
		return nil
	}
	c.run = func(ctx context.Context) error {
		if task.taskID != "" {
			return a.detail(ctx, c, task, response)
		}
		return a.list(ctx, c, options, response)
	}
}

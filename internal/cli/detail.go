package cli

import (
	"context"
	"io"
)

var detailSpec = commandSpec{
	name:        "detail",
	summary:     "查看任务和批次详情",
	syntax:      "--pcid ID [选项]",
	description: "按批次 ID 查询详情，展示签到时间、状态、允许地点和照片要求，无需提供任务 ID。",
	examples:    []string{"easycheck detail --pcid '<batch-id>'", "easycheck detail --pcid '<batch-id>' --out tasks-session.json"},
	setup:       (*app).configureDetail,
}

func (a *app) configureDetail(c *command) {
	var task taskOptions
	var response responseOptions
	task.bind(c.flags)
	response.bind(c.flags)
	c.validate = func() error { return task.validate() }
	c.run = func(ctx context.Context) error { return a.detail(ctx, c, task, response) }
}

func (a *app) detail(ctx context.Context, c *command, task taskOptions, response responseOptions) error {
	client, err := c.sessionClient()
	if err != nil {
		return err
	}
	result, err := client.GetTask(ctx, "", task.batchID)
	if err != nil {
		return err
	}
	return a.writeResponse("detail", response, result.RawResponse, func(output io.Writer) {
		printTaskDetail(output, result.Task)
	})
}

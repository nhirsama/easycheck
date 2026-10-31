package cli

import (
	"context"
	"errors"
	"io"

	"easycheck/qdgj"
)

var listSpec = commandSpec{
	name:     "list",
	summary:  "分页列出签到任务",
	syntax:   "[选项]",
	examples: []string{"easycheck list", "easycheck list --page 2 --page-size 20", "easycheck list --json"},
	setup:    (*app).configureList,
}

func (a *app) configureList(c *command) {
	var options qdgj.ListOptions
	var response responseOptions
	bindPagination(c, &options)
	response.bind(c.flags)
	c.validate = func() error { return validatePagination(options) }
	c.run = func(ctx context.Context) error { return a.list(ctx, c, options, response) }
}

func bindPagination(c *command, options *qdgj.ListOptions) {
	c.flags.IntVar(&options.Page, "page", 1, "列表页码 `N`，从 1 开始")
	c.flags.IntVar(&options.PageSize, "page-size", 10, "每页条数 `N`")
}

func validatePagination(options qdgj.ListOptions) error {
	if options.Page < 1 || options.PageSize < 1 {
		return errors.New("--page 和 --page-size 必须大于零")
	}
	return nil
}

func (a *app) list(ctx context.Context, c *command, options qdgj.ListOptions, response responseOptions) error {
	client, err := c.sessionClient()
	if err != nil {
		return err
	}
	result, err := client.ListTasks(ctx, options)
	if err != nil {
		return err
	}
	return a.writeResponse("list", response, result.RawResponse, func(output io.Writer) {
		printTaskList(output, result)
	})
}

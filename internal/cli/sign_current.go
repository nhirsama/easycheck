package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"easycheck/qdgj"
)

type automaticSignResult struct {
	BatchID  string          `json:"pcid"`
	Name     string          `json:"name"`
	Status   string          `json:"status"`
	Response json.RawMessage `json:"response,omitempty"`
	Error    string          `json:"error,omitempty"`
}

type automaticSignReport struct {
	Matched   int                   `json:"matched"`
	Succeeded int                   `json:"succeeded"`
	Skipped   int                   `json:"skipped"`
	Failed    int                   `json:"failed"`
	Results   []automaticSignResult `json:"results"`
}

// loadCurrentTasks 先完整读取列表再签到，避免提交改变列表排序造成翻页漏项。
func loadCurrentTasks(ctx context.Context, client *qdgj.Client) ([]qdgj.Task, error) {
	const pageSize, maxPages = 100, 1000
	var tasks []qdgj.Task
	seen := make(map[string]bool)
	for page := 1; page <= maxPages; page++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result, err := client.ListTasks(ctx, qdgj.ListOptions{Page: page, PageSize: pageSize})
		if err != nil {
			return nil, fmt.Errorf("查询签到任务第 %d 页：%w", page, err)
		}
		if result.PageNum != 0 && result.PageNum != page {
			return nil, fmt.Errorf("任务列表分页异常：请求第 %d 页，返回第 %d 页", page, result.PageNum)
		}
		added := 0
		for _, task := range result.Tasks {
			task.BatchID = strings.TrimSpace(task.BatchID)
			if task.BatchID != "" {
				if seen[task.BatchID] {
					continue
				}
				seen[task.BatchID] = true
			}
			tasks = append(tasks, task)
			added++
		}
		more := len(result.Tasks) > 0
		if result.PageCount > 0 {
			more = page < result.PageCount
		} else if result.Total > 0 {
			more = len(tasks) < result.Total
		}
		if !more {
			return tasks, nil
		}
		if added == 0 {
			return nil, errors.New("任务列表分页未前进，未提交签到；请检查列表接口")
		}
	}
	return nil, errors.New("任务列表超过 1000 页，未提交签到；请检查列表接口")
}

func (a *app) signCurrentTasks(ctx context.Context, client *qdgj.Client, request qdgj.SignInRequest, options responseOptions) (automaticSignReport, error) {
	tasks, err := loadCurrentTasks(ctx, client)
	if err != nil {
		return automaticSignReport{}, err
	}
	report := automaticSignReport{Results: make([]automaticSignResult, 0)}
	var failures []error
	for _, task := range tasks {
		if err := ctx.Err(); err != nil {
			failures = append(failures, err)
			break
		}
		if request.BatchID != "" && task.BatchID != request.BatchID {
			continue
		}
		available, err := task.SignInAvailable(time.Now())
		if err != nil {
			report.Failed++
			report.Results = append(report.Results, automaticSignResult{BatchID: task.BatchID, Name: task.Name, Status: "failed", Error: err.Error()})
			failures = append(failures, fmt.Errorf("批次 %s：%w", task.BatchID, err))
			continue
		}
		if !available {
			report.Skipped++
			continue
		}
		report.Matched++
		item := automaticSignResult{BatchID: task.BatchID, Name: task.Name}
		current := request
		// ID 只在执行时从列表和详情取得，不使用计划创建时的批次或任务信息。
		current.TaskID, current.BatchID = "", task.BatchID
		response, err := client.SignInIfActive(ctx, current)
		switch {
		case errors.Is(err, qdgj.ErrTaskNotActive):
			report.Skipped++
			item.Status = "skipped"
		case err != nil:
			report.Failed++
			item.Status, item.Error = "failed", err.Error()
			failures = append(failures, fmt.Errorf("批次 %s：%w", task.BatchID, err))
		default:
			report.Succeeded++
			item.Status, item.Response = "success", response.RawResponse
		}
		report.Results = append(report.Results, item)
	}
	raw, err := json.Marshal(report)
	if err != nil {
		return report, errors.Join(append(failures, fmt.Errorf("编码自动签到结果：%w", err))...)
	}
	outputErr := a.writeResponse("自动签到", options, raw, func(output io.Writer) {
		fmt.Fprintf(output, "自动签到完成：匹配 %d 项，成功 %d 项，跳过 %d 项，失败 %d 项\n", report.Matched, report.Succeeded, report.Skipped, report.Failed)
		if report.Matched == 0 && report.Failed == 0 {
			fmt.Fprintln(output, "当前没有处于签到窗口且未签到的定位任务。")
		}
		for _, item := range report.Results {
			switch item.Status {
			case "success":
				fmt.Fprintf(output, "签到成功：%s（pcid=%s）\n", item.Name, item.BatchID)
			case "skipped":
				fmt.Fprintf(output, "已跳过：%s（pcid=%s），最新详情已不满足签到条件\n", item.Name, item.BatchID)
			case "failed":
				fmt.Fprintf(output, "签到失败：%s（pcid=%s）：%s\n", item.Name, item.BatchID, item.Error)
			}
		}
	})
	return report, errors.Join(append(failures, outputErr)...)
}

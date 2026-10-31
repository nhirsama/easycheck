package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"easycheck/qdgj"
)

func (a *app) writeResponse(operation string, options responseOptions, raw json.RawMessage, render func(io.Writer)) error {
	var display strings.Builder
	if options.json {
		encoder := json.NewEncoder(&display)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(raw); err != nil {
			return fmt.Errorf("%s 操作成功，但输出响应失败：%w", operation, err)
		}
	} else {
		render(&display)
	}
	if _, err := io.WriteString(a.output, display.String()); err != nil {
		return fmt.Errorf("%s 操作成功，但输出响应失败：%w", operation, err)
	}
	if options.out != "" {
		if err := qdgj.WritePrivateFile(options.out, raw); err != nil {
			return fmt.Errorf("%s 操作成功，但保存响应失败：%w", operation, err)
		}
	}
	return nil
}

func printTaskList(output io.Writer, result *qdgj.TaskList) {
	fmt.Fprintf(output, "第 %d 页，每页 %d 条；本页 %d 条，共 %d 条，%d 页\n", result.PageNum, result.PageSize, len(result.Tasks), result.Total, result.PageCount)
	for i, task := range result.Tasks {
		fmt.Fprintf(output, "%d. %s | %s | %s | %s\n   %s ~ %s\n   rwid=%s\n   pcid=%s\n", i+1, task.Name, task.Method, task.Status, task.CheckInStatus, task.StartTime, task.EndTime, task.TaskID, task.BatchID)
	}
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

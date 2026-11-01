package qdgj

import (
	"errors"
	"fmt"
	"strings"
	"time"
	_ "time/tzdata"
)

// ErrTaskNotActive 表示批次当前不可自动签到，可能已签到、窗口已关闭或不是定位任务。
var ErrTaskNotActive = errors.New("任务当前不在可自动签到的窗口内")

// SignInAvailable 判断是否为当前窗口内未签到的定位任务。
// 原站无偏移量的时间按北京时间解释；窗口包含开始时间，不包含结束时间。
// 缺失或无效的时间返回错误，不猜测窗口。
func (task Task) SignInAvailable(now time.Time) (bool, error) {
	if strings.TrimSpace(task.Status) != "进行中" || strings.TrimSpace(task.CheckInStatus) == "已签到" {
		return false, nil
	}
	if strings.TrimSpace(task.Method) != "定位签到" {
		return false, nil
	}
	start, err := parseTaskTime(task.StartTime)
	if err != nil {
		return false, fmt.Errorf("签到开始时间：%w", err)
	}
	end, err := parseTaskTime(task.EndTime)
	if err != nil {
		return false, fmt.Errorf("签到结束时间：%w", err)
	}
	if !end.After(start) {
		return false, errors.New("签到结束时间必须晚于开始时间")
	}
	return !now.Before(start) && now.Before(end), nil
}

func (task Task) requireActive(now time.Time) error {
	available, err := task.SignInAvailable(now)
	if err != nil {
		return err
	}
	if !available {
		return ErrTaskNotActive
	}
	return nil
}

func parseTaskTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return parsed, nil
	}
	zone, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.Time{}, fmt.Errorf("加载北京时间：%w", err)
	}
	for _, layout := range []string{
		"2006-01-02 15:04:05", "2006-01-02 15:04",
		"2006-01-02T15:04:05", "2006-01-02T15:04",
		"2006/01/02 15:04:05", "2006/01/02 15:04",
	} {
		if parsed, err := time.ParseInLocation(layout, value, zone); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, errors.New("缺失或无效的日期时间")
}

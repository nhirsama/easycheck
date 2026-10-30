package qdgj

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"unicode/utf16"
)

const (
	queryPath = "api/sm-registration/qdgjQdrw/queryQdrw"
	signPath  = "api/sm-registration/qdgjQdrw/startSign"
)

// Number 支持服务端返回的数字、数字字符串及 null。nil 指针表示 null 或缺失。
type Number float64

func (n *Number) UnmarshalJSON(raw []byte) error {
	value := string(raw)
	if len(raw) > 0 && raw[0] == '"' {
		if err := json.Unmarshal(raw, &value); err != nil {
			return err
		}
	}
	f, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return errors.New("坐标或半径不是有效数字")
	}
	*n = Number(f)
	return nil
}

type AllowedLocation struct {
	Address   string  `json:"qddd"`
	Center    string  `json:"qdzx"`
	Longitude *Number `json:"jdzb"`
	Latitude  *Number `json:"wdzb"`
	Radius    *Number `json:"yxbj"`
}

type Task struct {
	TaskID           string            `json:"rwid"`
	BatchID          string            `json:"pcid"`
	Name             string            `json:"qdrw"`
	Type             string            `json:"qdlx"`
	Method           string            `json:"qdfs"`
	Frequency        string            `json:"qdpl"`
	Status           string            `json:"status"`
	CheckInStatus    string            `json:"zt"`
	StartTime        string            `json:"kssj"`
	EndTime          string            `json:"jssj"`
	LocationRule     string            `json:"qdtj"`
	PhotoRequired    string            `json:"sfsctp"`
	PhotoRequirement string            `json:"pzyq"`
	PhotoID          *string           `json:"qdtp"`
	Address          string            `json:"qddd"`
	Longitude        *Number           `json:"jdzb"`
	Latitude         *Number           `json:"wdzb"`
	AllowedLocations []AllowedLocation `json:"qddds"`
}

type ListOptions struct{ Page, PageSize int }

type TaskList struct {
	Tasks       []Task          `json:"list"`
	PageNum     int             `json:"pageNum"`
	PageSize    int             `json:"pageSize"`
	PageCount   int             `json:"pageCount"`
	Total       int             `json:"total"`
	RawResponse json.RawMessage `json:"-"`
}

type TaskDetail struct {
	Task
	RawResponse json.RawMessage `json:"-"`
}

type taskQuery struct {
	UserID     string      `json:"userid"`
	TaskID     string      `json:"rwid,omitempty"`
	BatchID    string      `json:"pcid,omitempty"`
	Pagination *pagination `json:"pagination,omitempty"`
}

type pagination struct {
	Page     int `json:"page"`
	PageSize int `json:"pageSize"`
}

func (c *Client) userID() (string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.user.UserID == "" || c.authorization == "" {
		return "", ErrNotAuthenticated
	}
	return c.user.UserID, nil
}

func (c *Client) queryTasks(ctx context.Context, query taskQuery) (*TaskList, error) {
	userID, err := c.userID()
	if err != nil {
		return nil, err
	}
	query.UserID = userID
	response, err := c.postJSON(ctx, "查询签到任务", queryPath, query)
	if err != nil {
		return nil, err
	}
	var result TaskList
	if err := json.Unmarshal(response.Data, &result); err != nil || bytes.Equal(bytes.TrimSpace(response.Data), []byte("null")) || len(response.Data) == 0 {
		return nil, errors.New("查询响应缺少有效的任务数据")
	}
	result.RawResponse = response.RawResponse
	return &result, nil
}

// ListTasks 查询一页任务，零值分页默认为第 1 页、每页 10 条，不自动翻页。
func (c *Client) ListTasks(ctx context.Context, options ListOptions) (*TaskList, error) {
	if options.Page < 0 || options.PageSize < 0 {
		return nil, errors.New("页码和每页数量不能小于零")
	}
	if options.Page == 0 {
		options.Page = 1
	}
	if options.PageSize == 0 {
		options.PageSize = 10
	}
	return c.queryTasks(ctx, taskQuery{Pagination: &pagination{Page: options.Page, PageSize: options.PageSize}})
}

// GetTask 查询指定任务的批次。batchID 为空时沿用前端只传任务 ID 的查询行为。
func (c *Client) GetTask(ctx context.Context, taskID, batchID string) (*TaskDetail, error) {
	if strings.TrimSpace(taskID) == "" {
		return nil, errors.New("TaskID 不能为空")
	}
	result, err := c.queryTasks(ctx, taskQuery{TaskID: taskID, BatchID: batchID})
	if err != nil {
		return nil, err
	}
	for _, task := range result.Tasks {
		if task.TaskID == taskID && (batchID == "" || task.BatchID == batchID) {
			return &TaskDetail{Task: task, RawResponse: result.RawResponse}, nil
		}
	}
	return nil, ErrTaskNotFound
}

// Location 是调用方提供的实际定位，不从允许地点中心或历史签到位置推算。
type Location struct {
	Address   string
	Longitude float64
	Latitude  float64
}

type SignInRequest struct {
	TaskID       string
	BatchID      string
	Location     Location
	PhotoID      *string // 无照片时传 nil，编码为 JSON null。
	OutsideRange bool
	Remark       string // 仅供 OutsideRange 使用，必填且不超过 100 个 UTF-16 code units。
}

type signPayload struct {
	TaskID       string  `json:"rwid"`
	BatchID      string  `json:"pcid"`
	UserID       string  `json:"userid"`
	Address      string  `json:"qddd"`
	PhotoID      *string `json:"qdtp"`
	Longitude    float64 `json:"jdzb"`
	Latitude     float64 `json:"wdzb"`
	OutsideRange string  `json:"sfxdfwwqd,omitempty"`
	Remark       string  `json:"bz,omitempty"`
}

// SignIn 是唯一会提交签到的方法。仅发送一次请求，不预查询、不自动重试。
// TaskID/BatchID 须从最新任务查询取得；成功条件为服务端 code == 200。
// 若请求超时或连接中断，结果可能不确定，应查询任务状态后再决定后续操作。
func (c *Client) SignIn(ctx context.Context, request SignInRequest) (*Response, error) {
	if strings.TrimSpace(request.TaskID) == "" || strings.TrimSpace(request.BatchID) == "" {
		return nil, errors.New("TaskID 和 BatchID 不能为空")
	}
	loc := request.Location
	if strings.TrimSpace(loc.Address) == "" || math.IsNaN(loc.Longitude) || math.IsInf(loc.Longitude, 0) || math.IsNaN(loc.Latitude) || math.IsInf(loc.Latitude, 0) || loc.Longitude < -180 || loc.Longitude > 180 || loc.Latitude < -90 || loc.Latitude > 90 {
		return nil, errors.New("须提供定位地址及有效的经纬度")
	}
	if request.OutsideRange {
		if strings.TrimSpace(request.Remark) == "" || len(utf16.Encode([]rune(request.Remark))) > 100 {
			return nil, errors.New("范围外签到须提供 1 至 100 个 UTF-16 字符的备注")
		}
	} else if request.Remark != "" {
		return nil, errors.New("Remark 仅用于范围外签到")
	}
	userID, err := c.userID()
	if err != nil {
		return nil, err
	}
	payload := signPayload{TaskID: request.TaskID, BatchID: request.BatchID, UserID: userID,
		Address: loc.Address, PhotoID: request.PhotoID, Longitude: loc.Longitude, Latitude: loc.Latitude}
	if request.OutsideRange {
		payload.OutsideRange = "1"
		payload.Remark = request.Remark
	}
	response, err := c.postJSON(ctx, "签到", signPath, payload)
	if err != nil {
		return nil, fmt.Errorf("提交签到：%w", err)
	}
	return response, nil
}

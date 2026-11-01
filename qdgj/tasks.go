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
	"time"
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

// ListTasks 查询一页任务，零值分页默认为第 1 页、每页 10 条。
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

// GetTask 按任务 ID 或批次 ID 查询详情，至少提供其中一个。
// taskID 为空时仅按 batchID 查询，无需提前取得任务 ID。
func (c *Client) GetTask(ctx context.Context, taskID, batchID string) (*TaskDetail, error) {
	taskID, batchID = strings.TrimSpace(taskID), strings.TrimSpace(batchID)
	if taskID == "" && batchID == "" {
		return nil, errors.New("TaskID 和 BatchID 至少提供一个")
	}
	result, err := c.queryTasks(ctx, taskQuery{TaskID: taskID, BatchID: batchID})
	if err != nil {
		return nil, err
	}
	for _, task := range result.Tasks {
		if (taskID == "" || task.TaskID == taskID) && (batchID == "" || task.BatchID == batchID) {
			return &TaskDetail{Task: task, RawResponse: result.RawResponse}, nil
		}
	}
	return nil, ErrTaskNotFound
}

// Location 保存签到地址、经度和纬度。地址为空时按坐标自动解析。
type Location struct {
	Address   string
	Longitude float64
	Latitude  float64
}

type SignInRequest struct {
	TaskID   string // 可省略；按 BatchID 查询当前批次详情后自动补齐。
	BatchID  string
	Location Location
	// UseAllowedLocation 从任务允许地点的中心和半径生成坐标，再自动解析地址。
	UseAllowedLocation bool
	PhotoID            *string // 照片文件标识，nil 编码为 JSON null。
	OutsideRange       bool
	Remark             string // 范围外签到的必填备注，长度上限为 100 个 UTF-16 字符。
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

// Validate 在不联网的情况下校验批次、定位与备注参数。
// 任务是否允许签到、会话是否过期等状态在实际执行时检查。
func (request SignInRequest) Validate() error {
	if strings.TrimSpace(request.BatchID) == "" {
		return errors.New("BatchID 不能为空")
	}
	return request.ValidateOptions()
}

// ValidateOptions 校验定位与备注，不要求批次 ID，供自动发现批次前检查选项。
func (request SignInRequest) ValidateOptions() error {
	if request.UseAllowedLocation && request.Location != (Location{}) {
		return errors.New("UseAllowedLocation 和 Location 请择一配置")
	}
	if !request.UseAllowedLocation {
		if err := request.Location.validateCoordinates(); err != nil {
			return err
		}
	}
	if request.OutsideRange {
		if strings.TrimSpace(request.Remark) == "" || len(utf16.Encode([]rune(request.Remark))) > 100 {
			return errors.New("范围外签到须提供 1 至 100 个 UTF-16 字符的备注")
		}
	} else if request.Remark != "" {
		return errors.New("Remark 仅用于范围外签到")
	}
	return nil
}

// SignIn 根据任务、批次和定位信息发送一次签到请求。
// TaskID 为空或 UseAllowedLocation 为 true 时先读取批次详情。
// BatchID 须从最新任务查询取得；成功条件为服务端 code == 200。
// 请求超时或连接中断后，可通过 GetTask 核对任务状态。
func (c *Client) SignIn(ctx context.Context, request SignInRequest) (*Response, error) {
	return c.signIn(ctx, request, false)
}

// SignInIfActive 重新查询批次，仅为当前窗口内未签到的定位任务提交一次签到。
// 任务已签到、窗口已关闭或方式不支持时返回 ErrTaskNotActive，不提交。
func (c *Client) SignInIfActive(ctx context.Context, request SignInRequest) (*Response, error) {
	return c.signIn(ctx, request, true)
}

func (c *Client) signIn(ctx context.Context, request SignInRequest, onlyActive bool) (*Response, error) {
	request.TaskID, request.BatchID = strings.TrimSpace(request.TaskID), strings.TrimSpace(request.BatchID)
	if err := request.Validate(); err != nil {
		return nil, err
	}
	userID, err := c.userID()
	if err != nil {
		return nil, err
	}
	loc := request.Location
	var currentTask *Task
	if request.TaskID == "" || request.UseAllowedLocation || onlyActive {
		task, err := c.GetTask(ctx, request.TaskID, request.BatchID)
		if err != nil {
			return nil, fmt.Errorf("读取签到批次详情：%w", err)
		}
		request.TaskID = strings.TrimSpace(task.TaskID)
		if request.TaskID == "" {
			return nil, errors.New("签到批次详情缺少任务 ID")
		}
		if onlyActive {
			if err := task.requireActive(time.Now()); err != nil {
				return nil, err
			}
			currentTask = &task.Task
		}
		if request.UseAllowedLocation {
			loc, err = randomAllowedLocation(task.AllowedLocations)
			if err != nil {
				return nil, err
			}
		}
	}
	loc.Address = strings.TrimSpace(loc.Address)
	if loc.Address == "" {
		loc.Address, err = c.ReverseGeocode(ctx, loc.Longitude, loc.Latitude)
		if err != nil {
			return nil, fmt.Errorf("解析签到位置名称：%w", err)
		}
	}
	if err := loc.validate(); err != nil {
		return nil, err
	}
	if currentTask != nil {
		// 定位和地址解析可能耗时，避免窗口在此期间结束后仍然提交。
		if err := currentTask.requireActive(time.Now()); err != nil {
			return nil, err
		}
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

func (loc Location) validate() error {
	if strings.TrimSpace(loc.Address) == "" || loc.validateCoordinates() != nil {
		return errors.New("须提供定位地址及有效的经纬度")
	}
	return nil
}

func (loc Location) validateCoordinates() error {
	if math.IsNaN(loc.Longitude) || math.IsInf(loc.Longitude, 0) || math.IsNaN(loc.Latitude) || math.IsInf(loc.Latitude, 0) || loc.Longitude < -180 || loc.Longitude > 180 || loc.Latitude < -90 || loc.Latitude > 90 {
		return errors.New("须提供有效的经纬度，经度范围 [-180, 180]，纬度范围 [-90, 90]")
	}
	return nil
}

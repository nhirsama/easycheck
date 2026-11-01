package qdgj

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

const mapConfigPath = "api/zhxg-xtgl/openapi/getCsszBatch"

type mapSetting struct {
	Name    string `json:"csbz"`
	Value   string `json:"csz"`
	Enabled string `json:"zt"`
}

// ReverseGeocode 使用站点地图配置，将地图坐标解析为地址。
// 当前支持腾讯地图（配置类型 3），输入坐标须与站点地图坐标系一致。
// 地图请求使用独立的无 Cookie 客户端，不发送站点身份凭据。
func (c *Client) ReverseGeocode(ctx context.Context, longitude, latitude float64) (string, error) {
	if err := (Location{Longitude: longitude, Latitude: latitude}).validateCoordinates(); err != nil {
		return "", err
	}
	response, err := c.postJSON(ctx, "读取地图配置", mapConfigPath, []string{
		"ZHXG_XTGL_DTKEY", "ZHXG_XTGL_DTLX", "ZHXG_QDGJ_DTKEY", "ZHXG_QDGJ_DTLX",
	})
	if err != nil {
		return "", err
	}
	var settings []mapSetting
	if err := json.Unmarshal(response.Data, &settings); err != nil {
		return "", errors.New("地图配置不是有效的配置列表")
	}
	active := make(map[string]string)
	for _, setting := range settings {
		if setting.Enabled == "1" && strings.TrimSpace(setting.Value) != "" {
			active[setting.Name] = strings.TrimSpace(setting.Value)
		}
	}
	key := active["ZHXG_QDGJ_DTKEY"]
	if key == "" {
		key = active["ZHXG_XTGL_DTKEY"]
	}
	mode := active["ZHXG_QDGJ_DTLX"]
	if mode == "" {
		mode = active["ZHXG_XTGL_DTLX"]
	}
	if mode != "3" {
		return "", errors.New("自动解析地址目前仅支持站点配置的腾讯地图（类型 3），请显式提供 --address")
	}
	if key == "" {
		return "", errors.New("站点未配置有效的腾讯地图 Key")
	}
	return c.tencentAddress(ctx, longitude, latitude, key)
}

func (c *Client) tencentAddress(ctx context.Context, longitude, latitude float64, key string) (string, error) {
	query := url.Values{
		"key":      {key},
		"location": {strconv.FormatFloat(latitude, 'f', -1, 64) + "," + strconv.FormatFloat(longitude, 'f', -1, 64)},
		"output":   {"json"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://apis.map.qq.com/ws/geocoder/v1/?"+query.Encode(), nil)
	if err != nil {
		return "", errors.New("无法创建坐标地址查询请求")
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.config.UserAgent)
	req.Header.Set("Referer", c.base.String())
	client := *c.http
	client.Jar = nil
	response, err := client.Do(req)
	if err != nil {
		// url.Error 包含带地图 Key 的 URL，仅保留底层错误和取消链。
		var requestError *url.Error
		if errors.As(err, &requestError) {
			err = requestError.Err
		}
		return "", fmt.Errorf("查询坐标地址：%w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("查询坐标地址失败（HTTP=%d）", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxResponseSize+1))
	if err != nil {
		return "", fmt.Errorf("读取坐标地址响应：%w", err)
	}
	if len(raw) > maxResponseSize {
		return "", errors.New("坐标地址响应超过 4 MiB")
	}
	var result struct {
		Status  *int   `json:"status"`
		Message string `json:"message"`
		Result  struct {
			Address string `json:"address"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &result); err != nil || result.Status == nil {
		return "", errors.New("坐标地址响应缺少有效的状态码")
	}
	if *result.Status != 0 {
		message := strings.ReplaceAll(result.Message, key, "<redacted>")
		return "", fmt.Errorf("查询坐标地址失败（status=%d）：%s", *result.Status, message)
	}
	address := strings.TrimSpace(result.Result.Address)
	if address == "" {
		return "", errors.New("地图服务未返回该坐标的地址")
	}
	return address, nil
}

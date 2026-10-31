package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"easycheck/qdgj"
)

var signSpec = commandSpec{
	name:        "sign",
	summary:     "根据任务和批次提交一次签到",
	syntax:      "--rwid ID --pcid ID [选项]",
	description: "默认读取允许地点的地址，在其半径的 90% 内随机生成坐标。\n自定义定位时同时传入 --address、--longitude、--latitude。",
	examples: []string{
		"easycheck sign --rwid '<task-id>' --pcid '<batch-id>'",
		"easycheck sign --rwid '<task-id>' --pcid '<batch-id>' --address '<address>' --longitude 10.5 --latitude 20.5",
	},
	setup: (*app).configureSign,
}

func (a *app) configureSign(c *command) {
	var task taskOptions
	var request qdgj.SignInRequest
	var response responseOptions
	var photoID string
	task.bind(c.flags)
	response.bind(c.flags)
	c.flags.StringVar(&request.Location.Address, "address", "", "签到 `ADDRESS`，与经纬度一起覆盖任务默认地点")
	c.flags.Float64Var(&request.Location.Longitude, "longitude", 0, "签到经度 `NUMBER`，与地址、纬度一起提供")
	c.flags.Float64Var(&request.Location.Latitude, "latitude", 0, "签到纬度 `NUMBER`，与地址、经度一起提供")
	c.flags.StringVar(&photoID, "photo-id", "", "已上传照片的文件 `ID`")
	c.flags.BoolVar(&request.OutsideRange, "outside-range", false, "使用范围外签到分支")
	c.flags.StringVar(&request.Remark, "remark", "", "范围外签到 `TEXT`，最多 100 个 UTF-16 字符")
	c.validate = func() error {
		if err := task.validate(true); err != nil {
			return err
		}
		provided := make(map[string]bool)
		c.flags.Visit(func(f *flag.Flag) { provided[f.Name] = true })
		if !provided["address"] && !provided["longitude"] && !provided["latitude"] {
			request.UseAllowedLocation = true
		} else if strings.TrimSpace(request.Location.Address) == "" || !provided["longitude"] || !provided["latitude"] {
			return errors.New("自定义定位须同时提供 --address、--longitude 和 --latitude")
		}
		request.TaskID, request.BatchID = task.taskID, task.batchID
		if photoID != "" {
			request.PhotoID = &photoID
		}
		return nil
	}
	c.run = func(ctx context.Context) error {
		client, err := c.sessionClient()
		if err != nil {
			return err
		}
		result, err := client.SignIn(ctx, request)
		if err != nil {
			return err
		}
		return a.writeResponse("sign", response, result.RawResponse, func(output io.Writer) {
			fmt.Fprintf(output, "签到成功（code=%d）\n", result.Code)
			if result.Meta.Message != "" {
				fmt.Fprintln(output, result.Meta.Message)
			}
		})
	}
}

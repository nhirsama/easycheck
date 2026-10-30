# Go 包调用说明

包路径为 `easycheck/qdgj`，需要 Go 1.24+，仅依赖标准库。

## 导入

尚未发布远程仓库时，在调用方 `go.mod` 中加入本地模块映射：

```go
require easycheck v0.0.0

replace easycheck => /path/to/easycheck
```

将占位路径替换为项目实际目录，再导入 `easycheck/qdgj`。

## 客户端配置

```go
client, err := qdgj.NewClient(qdgj.Options{})
```

默认从环境变量读取：

| 环境变量 | 用途 |
| --- | --- |
| `EASYCHECK_BASE_URL` | 应用根 URL，必须包含部署路径，没有内置默认站点 |
| `EASYCHECK_PUBLIC_KEY` | 登录用 Base64 DER/SPKI RSA 公钥，只有登录需要 |
| `EASYCHECK_USERNAME` | 登录命令使用的账号，包调用时由 `LoginOptions` 传入 |
| `EASYCHECK_PASSWORD` | 登录命令使用的密码，包调用时由 `LoginOptions` 传入 |

`Options.BaseURL`、`Options.LoginPublicKey` 非空时覆盖相应环境变量。另可配置 `UserAgent`、`Timeout` 和 `HTTPClient`；默认使用 Android 手机 UA 和 30 秒超时。自定义 `HTTPClient` 被复制，客户端会建立独立 CookieJar 并禁用重定向。自定义 Transport 的重试行为由调用方负责。

## 登录及验证码

```go
saved, err := client.Login(ctx, qdgj.LoginOptions{
    Username: username,
    Password: password,
    CaptchaSolver: func(ctx context.Context, image []byte) (string, error) {
        return askUserForCaptcha(ctx, image)
    },
})
if err != nil {
    return err
}
return saved.Save("session.json")
```

`askUserForCaptcha` 由应用实现。验证码关闭时不会调用回调；验证码开启且回调为空时，返回 `ErrCaptchaRequired`，不提交登录。

`Login` 会访问入口、检查验证码开关、按需下载图片，并在同一 CookieJar 内提交一次登录。公钥缺少或格式不正确时先在本地返回错误。密码不保存在客户端或会话中。

`CaptchaRequired(ctx)` 只检查入口和验证码开关；`FetchCaptcha(ctx)` 下载当前会话的验证码图片。

## 会话与任务查询

以下示例只查询任务：

```go
package main

import (
    "context"
    "fmt"
    "log"
    "time"

    "easycheck/qdgj"
)

func main() {
    saved, err := qdgj.LoadSession("session.json")
    if err != nil {
        log.Fatal(err)
    }
    client, err := qdgj.NewClientFromSession(saved, qdgj.Options{})
    if err != nil {
        log.Fatal(err)
    }
    ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
    defer cancel()
    result, err := client.ListTasks(ctx, qdgj.ListOptions{Page: 1, PageSize: 10})
    if err != nil {
        log.Fatal(err)
    }
    for _, task := range result.Tasks {
        fmt.Println(task.Name, task.Status, task.CheckInStatus, task.BatchID)
    }
}
```

`NewClientFromSession` 同样使用 `Options.BaseURL` 或 `EASYCHECK_BASE_URL`；它必须与会话保存的基础 URL 一致，避免把会话发送到不同站点。加载与恢复会话不联网，也不覆盖文件。

`ListTasks` 返回 `Tasks`、`PageNum`、`PageSize`、`PageCount`、`Total` 和完整 `RawResponse`。零值分页选项默认第一页、每页 10 条，不自动翻页。

```go
detail, err := client.GetTask(ctx, taskID, batchID)
```

`TaskID`、`BatchID` 对应接口字段 `rwid`、`pcid`，应从最新列表取得。`TaskDetail` 嵌入 `Task`，包含签到方式、批次状态、签到状态、时间、位置范围、照片要求等，并保留完整 `RawResponse`。

服务端的坐标和半径支持数字、数字字符串及 `null`，统一解码为 `*Number`：nil 表示缺失，否则用 `float64(*value)` 读取。时间保留原始字符串。

`client.Session()` 可取得含最新 Cookie 的独立快照；`snapshot.Save(path)` 以 `0600` 权限原子写入指定文件。目录须已存在。`Session.User()` 返回用户结构；完整登录响应仍保留在 `LoginResponse`。

## 显式定位签到

`SignIn` 是唯一提交签到的方法，现有命令行工具不调用它。调用方明确提交时使用：

```go
response, err := client.SignIn(ctx, qdgj.SignInRequest{
    TaskID: taskID,
    BatchID: batchID,
    Location: actualLocation,
    PhotoID: nil,
})
```

`actualLocation` 是调用方获得的实际定位，类型为 `qdgj.Location`，包含 `Address`、`Longitude`、`Latitude`。包不使用允许地点中心或历史坐标自动填充。无照片时 `PhotoID: nil` 编码为 JSON null；需要照片时传入已取得的有效文件值。

范围外分支使用 `OutsideRange: true` 及必填 `Remark`，备注最多 100 个 UTF-16 字符；是否允许该分支取决于服务端任务规则。普通提交不附加范围外字段。

方法不隐式查询，不自动重试或跟随重定向。超时或连接中断时，调用方可先用 `GetTask` 核对服务器状态。二维码签到和照片上传尚未封装。

## 错误处理

`APIError` 包含 `HTTPStatus`、`Code`、`StatusCode` 和 `Message`，支持 `errors.As`。本地缺少身份、缺少验证码回调和任务不存在，分别返回 `ErrNotAuthenticated`、`ErrCaptchaRequired`、`ErrTaskNotFound`，支持 `errors.Is`。远端会话过期以相应 `APIError` 返回。

网络方法均接受 `context.Context`，取消错误保留包装链。完成登录后可并发查询；不要在同一客户端登录期间并发进行其他操作。

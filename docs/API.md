# 客户端与协议参考

命令行用法见 [CLI 使用说明](CLI.md)。

## Go 包调用

包路径为 `easycheck/qdgj`，需要 Go 1.24+。`qdgj` 包仅依赖标准库，CLI 的 cron 调度使用 `github.com/robfig/cron/v3`。

### 导入

尚未发布远程仓库时，在调用方 `go.mod` 中加入本地模块映射：

```go
require easycheck v0.0.0

replace easycheck => /path/to/easycheck
```

将占位路径替换为项目实际目录，再导入 `easycheck/qdgj`。

### 一次配置，使用同一个对象

```go
config := qdgj.Config{
    BaseURL: baseURL,
    Username: username,
    Password: password,
    SessionFile: "session.json",
    CaptchaSolver: func(ctx context.Context, image []byte) (string, error) {
        return askUserForCaptcha(ctx, image)
    },
}
client, err := qdgj.NewClient(config)
if err != nil {
    return err
}
if _, err := client.Login(ctx); err != nil {
    return err
}
if err := client.SaveSession(); err != nil {
    return err
}
tasks, err := client.ListTasks(ctx, qdgj.ListOptions{})
```

`askUserForCaptcha` 由应用实现；不需要验证码时可省略回调。配置只传入构造函数一次，`Login(ctx)` 不再接收账号、密码或验证码配置。对象内部管理登录协议、Cookie 和令牌，查询方法自动使用当前登录用户。页码、任务 ID 和签到位置等每次操作的数据仍传给对应方法。

也可以直接从环境变量创建对象，无需在应用中逐项调用 `os.Getenv`：

```go
client, err := qdgj.NewClient(qdgj.ConfigFromEnv())
```

`ConfigFromEnv()` 集中读取配置。`NewClient(Config{})` 也会为未填写的字段补齐环境变量及默认值：

| 环境变量 | 用途 |
| --- | --- |
| `EASYCHECK_BASE_URL` | 应用根 URL，必须包含部署路径，没有内置默认站点 |
| `EASYCHECK_USERNAME` | `Config.Username`，对象登录使用的账号 |
| `EASYCHECK_PASSWORD` | `Config.Password`，对象登录使用的密码 |
| `EASYCHECK_SESSION_FILE` | `Config.SessionFile`，默认 `session.json` |

显式配置的非空字段优先于环境变量。另可配置 `UserAgent`、`Timeout` 和 `HTTPClient`；默认使用 Android 手机 UA 和 30 秒超时。自定义 `HTTPClient` 被复制，客户端会建立独立 CookieJar 并禁用重定向。自定义 Transport 的重试行为由调用方负责。

构造函数不联网、不读写会话文件，并保存配置副本。对象创建后改变原配置或环境变量不会改变它的登录信息及会话路径；切换账号或站点时创建新对象。`Options` 暂时作为 `Config` 的兼容别名保留。

### 登录及验证码

```go
if _, err := client.Login(ctx); err != nil {
    return err
}
return client.SaveSession()
```

登录使用 `Config` 中的账号、密码及 `CaptchaSolver`，RSA 公钥由客户端从页面资源自动获取。验证码关闭时不会调用回调；验证码开启且回调为空时，返回 `ErrCaptchaRequired`，不提交登录。

`Login` 会访问入口、提取公钥、检查验证码开关、按需下载图片，并在同一 CookieJar 内提交一次登录。每次调用都会重新读取公钥，提取失败时返回错误。配置密码仅保留在客户端内存中，不写入会话。成功后返回独立的会话快照；若不使用返回值，可以像上面一样忽略它。会话仅在显式调用 `SaveSession` 时写入文件。

`FetchLoginPublicKey(ctx)` 可单独获取公钥，返回 Base64 DER/SPKI 字符串。支持内联的 `setPublicKey` 调用、公钥字段、静态脚本引用和 `/mobile/login` 路由的 Webpack 数字分包，接受 Base64 DER 或 PEM 格式。脚本地址限定在同站点应用根路径下，最多读取 16 个脚本，累计页面资源上限为 16 MiB，每份资源上限为 4 MiB。资源结构变化导致查找失败时返回 `ErrLoginPublicKeyNotFound`，可用 `errors.Is` 判断。

`CaptchaRequired(ctx)` 只检查入口和验证码开关；`FetchCaptcha(ctx)` 下载当前会话的验证码图片。

### 会话与任务查询

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
    client, err := qdgj.NewClient(qdgj.ConfigFromEnv())
    if err != nil {
        log.Fatal(err)
    }
    if err := client.LoadSession(); err != nil {
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

`LoadSession()` 使用创建对象时配置的 `SessionFile`，校验会话站点与对象的基础 URL 一致，失败时保留原会话。加载不联网、不重新登录，也不覆盖文件。只恢复会话并查询时无需提供账号和密码。

`ListTasks` 返回 `Tasks`、`PageNum`、`PageSize`、`PageCount`、`Total` 和完整 `RawResponse`。零值分页选项默认第一页、每页 10 条，不自动翻页。

```go
detail, err := client.GetTask(ctx, "", batchID)
```

`TaskID`、`BatchID` 对应接口字段 `rwid`、`pcid`。按批次查询时只需从最新列表取得 `BatchID`，将 `taskID` 留空；也保留按任务 ID 查询的用法，两种 ID 至少提供一个。`TaskDetail` 嵌入 `Task`，包含签到方式、批次状态、签到状态、时间、位置范围、照片要求等，并保留完整 `RawResponse`。

服务端的坐标和半径支持数字、数字字符串及 `null`，统一解码为 `*Number`：nil 表示缺失，否则用 `float64(*value)` 读取。时间保留原始字符串。

`client.SaveSession()` 将当前会话以 `0600` 权限原子写入配置路径；目录须已存在。`client.Session()` 可取得含最新 Cookie 的独立快照，`Session.User()` 返回用户结构；完整登录响应仍保留在 `LoginResponse`。底层 `LoadSession(path)`、`Session.Save(path)` 和 `NewClientFromSession` 仍保留，供需要自定义存储或恢复内存会话的调用方使用。

### 定位签到

`SignIn` 提交签到，CLI 的 `sign` 子命令调用该方法。使用任务允许地点的示例：

```go
response, err := client.SignIn(ctx, qdgj.SignInRequest{
    BatchID: batchID,
    UseAllowedLocation: true,
    PhotoID: nil,
})
```

`SignInRequest.Validate()` 可在不联网的情况下预先校验批次、定位和备注；`SignIn` 内部同样执行此校验。`ValidateOptions()` 仅校验定位和备注，允许在自动发现批次 ID 前检查配置。

CLI 的 `sign --cron '0 8 * * *'` 通过 `robfig/cron/v3` 常驻周期调度，不要求提前提供批次 ID。每次触发重新加载会话、翻页查询列表，为当前窗口内未签到的定位批次逐个调用 `SignInIfActive`；前一轮未完成时跳过重叠触发，本轮失败记录日志后等待下一次触发。`sign` 不带 `--cron`、`--pcid` 时立即自动执行一轮。Go 包的签到方法本身保持立即执行，不隐式等待或重试。

`task.SignInAvailable(now)` 判断是否为“进行中”、尚未“已签到”的定位任务，并满足 `kssj <= now < jssj`。原站无时区日期按 `Asia/Shanghai` 解释，也支持 RFC3339；时间缺失或无效时返回错误。

`client.SignInIfActive(ctx, request)` 与 `SignIn` 使用相同参数，但总是重新查询批次详情并核对状态和时间窗口；地址解析后也会再次检查窗口尚未关闭。任务当前已签到、不在窗口内或方式不支持时返回 `ErrTaskNotActive`，不发送签到请求。`SignIn` 的手动立即提交行为保持不变。

`TaskID` 可省略，客户端按当前用户和 `BatchID` 查询详情并自动补齐。`UseAllowedLocation: true` 复用这次详情查询，从第一个有效的允许地点读取中心经纬度和正数半径。坐标每次在半径的 90% 内生成，随后用当前站点配置的腾讯地图解析该坐标的地址，不再拼接固定地点名称。允许地点无需提供文字地址。

传入 `Location: qdgj.Location{Longitude: longitude, Latitude: latitude}` 可使用自定义坐标并自动解析地址，此时 `UseAllowedLocation` 保持 false。显式提供非空 `Address` 时跳过地图解析。两种坐标来源择一使用。自定义坐标必须为有限数字，经度范围为 `[-180, 180]`、纬度范围为 `[-90, 90]`，坐标系须与原站地图一致。无照片时 `PhotoID: nil` 编码为 JSON null；需要照片时传入已取得的有效文件值。

也可以只解析地址而不签到：

```go
address, err := client.ReverseGeocode(ctx, longitude, latitude)
```

`ReverseGeocode` 使用当前会话读取 `api/zhxg-xtgl/openapi/getCsszBatch` 的地图配置，优先使用 `ZHXG_QDGJ_DTKEY` / `ZHXG_QDGJ_DTLX`，缺省时使用全局配置。目前支持类型 `3`（腾讯地图），通过逆地址解析接口返回的 `result.address` 填充 `qddd`。地图请求使用独立的无 Cookie 客户端，不附加站点令牌、用户或组织身份；地图 Key 不写入会话文件。

范围外分支使用 `OutsideRange: true` 及必填 `Remark`，备注最多 100 个 UTF-16 字符；是否允许该分支取决于服务端任务规则。普通提交不附加范围外字段。

默认地点模式或省略 `TaskID` 时执行一次详情查询。需要自动地址时另执行一次地图配置查询和一次逆地址解析；同时指定 `TaskID` 和含地址的自定义定位时直接提交。查询失败、批次不匹配、响应缺少任务 ID 或地址解析失败时不提交；地址解析失败不会回退到固定地点名称，签到提交均为一次。超时或连接中断时，调用方可先用 `GetTask` 核对服务器状态。二维码签到和照片上传尚未封装。

### 错误处理

`APIError` 包含 `HTTPStatus`、`Code`、`StatusCode` 和 `Message`，支持 `errors.As`。本地缺少身份、缺少验证码回调和任务不存在，分别返回 `ErrNotAuthenticated`、`ErrCaptchaRequired`、`ErrTaskNotFound`，支持 `errors.Is`。`SignInIfActive` 发现当前任务不可自动签到时返回 `ErrTaskNotActive`。远端会话过期以相应 `APIError` 返回。

网络方法均接受 `context.Context`，取消错误保留包装链。完成登录或会话加载后可并发查询；不要在同一客户端登录或加载会话期间并发进行其他操作。

## 移动签到协议

本文仅包含通用协议字段及占位词。应用根地址记作 `<BASE_URL>`，来自 `EASYCHECK_BASE_URL` 或显式 `Config.BaseURL`，例如占位地址 `https://example.invalid/qdgj/`。接口相对于应用根路径拼接，地址未以 `/` 结尾时自动补齐。

### 登录

| 步骤 | 相对路径 | 说明 |
| --- | --- | --- |
| 初始化 | `GET <BASE_URL>` | 建立 Cookie 会话 |
| 登录公钥 | `GET` 页面引用的登录脚本 | 自动提取 RSA 公钥 |
| 验证码开关 | `GET <BASE_URL>ng/captcha/status` | `appId` 为协议应用标识 |
| 验证码图片 | `GET <BASE_URL>captchaServlet?v=<timestamp-ms>` | 与登录共用 CookieJar |
| 登录 | `POST <BASE_URL>login` | `application/x-www-form-urlencoded` |

表单字段为 `username`、`password`、`encryptTransfer`、`timestamp`、`digest`、`systemId`，验证码开启时附加 `captcha`。密码使用页面资源中自动提取的 RSA 公钥做 PKCS#1 v1.5 加密，再以 Base64 编码。摘要原文为：

```text
username=<username>,password=<encrypted-password>,timestamp=<timestamp-ms>
```

摘要按兼容的前端 SHA-1 编码计算。登录成功判断使用 `meta.statusCode == 200`。

### 已登录请求

公共头由客户端自动生成：

```http
Content-Type: application/json;charset=UTF-8
Accept: application/json
Authorization: <session-token>
loginUserId: <user-id>
loginUserOrgId: <organization-id>
X-Requested-With: XMLHttpRequest
Sec-CH-UA-Mobile: ?1
Sec-CH-UA-Platform: "Android"
```

Cookie 从当前会话加载；Origin、Referer 与基础 URL 一致。当前兼容协议中令牌值不带 Bearer 前缀，优先使用 `tokenId`，缺失时使用 `accessToken`。请求附加 `_t=<timestamp-seconds>`。

### 任务列表及详情

```text
POST <BASE_URL>api/sm-registration/qdgjQdrw/queryQdrw
```

列表 JSON：

```json
{
  "userid": "<user-id>",
  "pagination": {"pageSize": 10, "page": 1}
}
```

详情 JSON：

```json
{
  "userid": "<user-id>",
  "pcid": "<batch-id>"
}
```

任务位于 `data.list`。`rwid` 是任务 ID，`pcid` 是批次 ID；按批次查询不必提供 `rwid`，签到时从该批次详情自动取得。也可在查询中附加 `rwid` 以限定任务。循环任务可共用任务 ID，但不能据历史批次推定当前批次。`status` 表示批次状态，`zt` 表示当前用户签到状态，`kssj`、`jssj` 表示起止时间。

`qdfs` 为签到方式，`qdtj` 为范围条件，`qddds` 为允许地点列表，`sfsctp`、`pzyq` 为照片要求。历史位置及照片字段可能为 null。查询成功判断为 `code == 200`，同时检查非零的 `meta.statusCode` 是否为 200。

### 定位签到

```text
POST <BASE_URL>api/sm-registration/qdgjQdrw/startSign
```

| 字段 | 值 |
| --- | --- |
| `rwid` / `pcid` | 最新查询得到的任务 / 批次 ID |
| `userid` | 当前会话用户 ID |
| `qddd` | 按坐标解析得到的地址，或显式提供的自定义地址 |
| `jdzb` / `wdzb` | 签到经度 / 纬度，JSON 数字 |
| `qdtp` | 照片文件值，无照片时为 null |
| `sfxdfwwqd` / `bz` | 范围外分支附加 `"1"` 和备注 |

包通过 `SignIn` 方法调用此接口。`UseAllowedLocation: true` 时先查询批次详情，在允许地点半径的 90% 内生成坐标，再按坐标解析地址并填充 `qddd`。CLI 省略地址和经纬度时使用此模式；同时提供经度和纬度时使用自定义坐标，`--address` 为可选的文字地址覆盖。签到提交为一次，服务端允许的时间、范围与照片条件以当前任务为准。

二维码结果路由 `#/mobile/student/qrResult` 在兼容前端中可能自动触发提交，不属于只读详情页面。该分支尚未封装。

### 仓库数据

仓库不保留真实账号、会话、任务 ID、定位地址、学校名称、域名、截图、抓取记录或下载的部署资源。协议测试全部使用合成数据和本地模拟服务。

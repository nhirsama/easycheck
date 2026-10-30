# EasyCheck

通用的移动签到协议 Go 客户端，提供登录、验证码、会话持久化、任务列表、批次详情和显式定位签到。需要 Go 1.24+，仅依赖标准库。

客户端包为 `easycheck/qdgj`。根目录 CLI 提供登录、入口检查、任务列表、批次详情和定位签到；`cmd/checkin-inspect` 提供独立的任务查询入口。

创建对象时一次传入配置，之后直接调用对象的方法：

```go
client, err := qdgj.NewClient(qdgj.ConfigFromEnv())
if err != nil {
    return err
}
if _, err := client.Login(ctx); err != nil {
    return err
}
tasks, err := client.ListTasks(ctx, qdgj.ListOptions{})
```

地址、账号、密码、验证码回调和会话文件路径都属于 `qdgj.Config`。`Login(ctx)` 自动从页面资源读取公钥；`client.LoadSession()`、`client.SaveSession()` 使用配置中的会话路径。

## 配置

站点基础 URL 从 `EASYCHECK_BASE_URL` 读取，没有内置目标地址。填写应用根地址，并保留部署路径，例如：

```sh
export EASYCHECK_BASE_URL='https://example.invalid/qdgj/'
```

上面的域名是占位词，须替换为实际部署地址。包内所有接口、Origin 和 Referer 均由同一基础 URL 构造。`-base-url` 或 `Config.BaseURL` 可显式覆盖环境变量。

配置登录账号：

```sh
export EASYCHECK_USERNAME='example-user'
```

`.env.example` 列出了配置项。程序不会自动加载 `.env`；如需使用文件，可复制为 `.env`、填写本地值，然后手动导出：

```sh
set -a
. ./.env
set +a
```

`.env`、会话、验证码和原始抓取目录均已加入忽略规则。

## 命令行

查看命令及选项：

```sh
go run . help
go run . sign -help
```

检查入口与验证码开关：

```sh
go run . check
```

以 zsh 隐藏输入密码，再提交一次登录：

```zsh
read -rs 'EASYCHECK_PASSWORD?密码：'
echo
export EASYCHECK_PASSWORD
go run . login
unset EASYCHECK_PASSWORD
```

也可使用 `-username`、`-password-stdin`、`-session`、`-captcha-file`、`-timeout` 等选项，详见 `go run . login -help`。验证码开启时，命令保存图片并等待用户输入，在同一 Cookie 会话内提交登录。原有的 `go run .` 和 `go run . -check` 用法继续可用。

登录成功后以 `0600` 权限保存 `session.json`，不保存明文密码。会话仅适用于保存时的站点，恢复时要求当前配置的基础 URL 与之相同。

`EASYCHECK_SESSION_FILE` 可指定默认会话路径，`-session` 可覆盖它。账号等环境变量统一由包读取，创建客户端后不再随环境变量变化。

登录后使用保存的会话查询签到任务和指定批次：

```sh
go run . list
go run . list -page 2 -page-size 20
go run . detail -rwid '<task-id>' -pcid '<batch-id>'
go run . list -json
go run . detail -rwid '<task-id>' -pcid '<batch-id>' -out tasks-session.json
```

`list` 展示分页、任务状态和 `rwid`/`pcid`；`detail` 展示时间、位置规则、允许地点及照片要求。`detail` 的 `-pcid` 可省略，此时按任务 ID 查询。`list`、`detail`、`sign` 均支持 `-json` 输出完整 JSON、`-out` 以 `0600` 权限保存原始响应。所有子命令都支持 `-base-url`、`-session` 和 `-timeout`。

传入列表中的任务、批次以及实际定位，提交一次签到：

```sh
go run . sign \
  -rwid '<task-id>' -pcid '<batch-id>' \
  -address '<actual-address>' \
  -longitude '<actual-longitude>' -latitude '<actual-latitude>'
```

经度、纬度必须显式传入，范围分别为 `[-180, 180]` 和 `[-90, 90]`。需要照片时添加 `-photo-id '<uploaded-photo-id>'`，使用已上传的照片标识。范围外签到使用 `-outside-range -remark '<remark>'`，是否允许该分支以服务端任务规则为准。

`sign` 调用包的 `SignIn`，每次执行提交一次请求。超时或连接中断后，可用 `detail` 核对批次状态。查询和签到命令加载已有会话，会话过期时重新执行 `login`。

原独立查询入口仍可使用：

```sh
go run ./cmd/checkin-inspect
go run ./cmd/checkin-inspect -rwid '<task-id>' -pcid '<batch-id>'
```

## 包调用与协议

- [PACKAGE.md](PACKAGE.md)：外部项目导入、客户端 API、验证码回调及错误处理。
- [CHECKIN_API.md](CHECKIN_API.md)：使用占位值的接口协议说明。

登录时自动读取入口页面及同站点登录 JavaScript 分包中的 RSA 公钥，每次登录重新获取。支持页面内联公钥、静态脚本引用和登录路由的 Webpack 数字分包；资源结构变化导致提取失败时返回错误。

登录加密按兼容协议执行 RSA PKCS#1 v1.5 和前端摘要编码，不修改调用方的全局 `GODEBUG`。仓库中的测试公钥均为合成数据。

## 验证

```sh
go test -race ./...
go vet ./...
```

测试仅使用本地模拟服务、占位数据和合成公钥，不访问实际部署，不提交真实签到。

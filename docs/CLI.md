# CLI 使用说明

标准入口为 `cmd/easycheck`，根目录入口使用同一套 CLI 实现。需要 Go 1.24+。

```sh
go run ./cmd/easycheck --help
go build -o easycheck ./cmd/easycheck
./easycheck help sign
```

以下示例使用构建后的 `./easycheck`，也可替换为 `go run ./cmd/easycheck` 或 `go run .`。

## 命令与帮助

```text
easycheck <命令> [选项]
easycheck help [命令]
```

| 命令 | 功能 |
| --- | --- |
| `login` | 登录并保存会话 |
| `check` | 检查手机端入口和验证码状态 |
| `list` | 分页列出签到任务 |
| `detail` | 查看任务和批次详情 |
| `sign` | 根据任务和批次提交一次签到 |
| `help` | 查看总帮助或指定命令的帮助 |

无参数运行显示总帮助。`help sign`、`sign --help` 和 `sign -h` 显示相同的命令帮助。

## 公共选项

所有选项放在命令之后。文档统一使用双横线，原有单横线写法继续支持，例如 `--session` 和 `-session` 等价。

| 选项 | 配置来源与默认值 |
| --- | --- |
| `--base-url URL` | `EASYCHECK_BASE_URL`，填写应用根地址并保留部署路径 |
| `--session FILE` | `EASYCHECK_SESSION_FILE`，默认 `session.json` |
| `--timeout DURATION` | 单次 HTTP 请求超时，默认 `30s`，须大于零 |
| `-h, --help` | 查看帮助 |

配置优先级：**命令行参数 > 环境变量 > 默认值**。省略参数时读取对应的环境变量；显式传入空的必填值时返回错误。环境配置项见根目录的 `.env.example`。使用 `.env` 文件时，填写本地配置后手动导出：

```sh
set -a
. ./.env
set +a
```

帮助显示环境变量名称，配置中的账号、站点和会话路径保持隐藏。

```sh
./easycheck list --session local-session.json --timeout 10s
```

## 登录与入口检查

```sh
./easycheck check
./easycheck login
```

`check` 显示入口地址和验证码开关。`login` 支持直接传入地址、账号和密码，以 `0600` 权限保存会话：

```sh
./easycheck login --base-url 'https://example.invalid/qdgj/' --username example-user --password '<password>'
```

省略账号或密码参数时，分别读取 `EASYCHECK_USERNAME`、`EASYCHECK_PASSWORD`。

| 登录选项 | 功能 |
| --- | --- |
| `--username USER` | 覆盖 `EASYCHECK_USERNAME` 中的账号 |
| `--password PASSWORD` | 覆盖 `EASYCHECK_PASSWORD` 中的密码 |
| `--password-stdin` | 从标准输入第一行读取密码，覆盖环境中的密码 |
| `--captcha-file FILE` | 保存验证码图片，默认 `captcha.png` |

`--password` 与 `--password-stdin` 择一使用。以 zsh 隐藏输入密码，再登录：

```zsh
read -rs 'EASYCHECK_PASSWORD?密码：'
echo
export EASYCHECK_PASSWORD
./easycheck login
unset EASYCHECK_PASSWORD
```

验证码开启时，CLI 保存图片并在标准错误中提示输入，随后从标准输入读取验证码。密码和验证码共用标准输入时，密码占第一行，验证码占第二行。

会话文件保存令牌、Cookie 和登录响应，恢复时要求当前基础 URL 与保存时一致。会话过期后执行 `login` 更新会话。

## 列表与详情

```sh
./easycheck list
./easycheck list --page 2 --page-size 20
./easycheck detail --rwid '<task-id>' --pcid '<batch-id>'
```

`list` 的 `--page` 默认为 `1`，`--page-size` 默认为 `10`，两者须大于零。结果包含分页、任务状态、任务 ID（`rwid`）和批次 ID（`pcid`）。

`detail` 要求 `--rwid`，`--pcid` 可选。提供批次 ID 时查询该批次，省略时按任务查询。结果包含签到时间、状态、位置规则、允许地点和照片要求。

## 签到

使用任务允许地点提交一次签到：

```sh
./easycheck sign --rwid '<task-id>' --pcid '<batch-id>'
```

`sign` 要求同时提供 `--rwid` 和 `--pcid`。默认先查询当前批次详情，选择第一个具有有效地址、经纬度和正数半径的允许地点，将地点名称与地址合并为签到地址。客户端在该地点半径的 90% 内随机生成坐标，保留 10% 的边界余量；地点信息缺失时返回错误。

自定义定位时，同时提供地址、经度和纬度：

```sh
./easycheck sign \
  --rwid '<task-id>' --pcid '<batch-id>' \
  --address '<address>' \
  --longitude 10.5 --latitude 20.5
```

| 签到选项 | 功能 |
| --- | --- |
| `--address ADDRESS` | 与经纬度一起覆盖任务默认地点 |
| `--longitude NUMBER` | 经度，有限数字，范围 `[-180, 180]` |
| `--latitude NUMBER` | 纬度，有限数字，范围 `[-90, 90]` |
| `--photo-id ID` | 已上传照片的文件标识 |
| `--outside-range` | 使用范围外签到分支 |
| `--remark TEXT` | 范围外签到备注，最多 100 个 UTF-16 字符 |

范围外签到使用 `--outside-range --remark '<remark>'`，是否允许以服务端任务规则为准。定位生成、坐标校验、时间和照片要求均由客户端包及服务端处理。

默认定位执行一次详情查询和一次签到提交，自定义定位直接提交一次签到。超时或连接中断后，可用 `detail` 核对批次状态。操作成功后发生输出或文件保存错误时，错误消息会明确标记操作已经成功。

## 输出与导出

`list`、`detail` 和 `sign` 支持以下选项：

| 选项 | 功能 |
| --- | --- |
| `--json` | 将完整 JSON 响应写入标准输出 |
| `--out FILE` | 将原始响应以 `0600` 权限保存到指定文件 |

```sh
./easycheck list --json
./easycheck detail --rwid '<task-id>' --out tasks-session.json
```

命令结果和帮助使用标准输出；交互提示、错误使用标准错误。`--json` 的标准输出可直接用于管道处理。详情和原始响应会包含实际地点、坐标及账号相关业务信息。

| 退出码 | 含义 |
| --- | --- |
| `0` | 命令成功或帮助已显示 |
| `1` | 执行失败，包括客户端、网络、会话及输出错误 |
| `2` | 命令、参数解析或 CLI 参数检查失败 |

`Ctrl+C` 和 `SIGTERM` 会取消正在进行的 HTTP 请求。通过 `go run` 启动时，Go 启动器可能将程序的非零退出码转为自己的退出码；脚本可使用构建后的可执行文件读取上述退出码。

## 兼容入口

根目录仍支持 `go run . <命令>`。原有带选项的默认登录方式和 `-check` 也继续可用：

```sh
go run . -username example-user -password-stdin
go run . -check
```

独立查询入口复用 `list` 和 `detail` 的参数、输出及导出逻辑：

```sh
go run ./cmd/checkin-inspect
go run ./cmd/checkin-inspect --rwid '<task-id>' --pcid '<batch-id>'
go run ./cmd/checkin-inspect --help
```

提供 `--rwid` 时查询详情，其余情况查询列表。该入口也支持公共选项、`--page`、`--page-size`、`--json` 和 `--out`。

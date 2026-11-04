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
| `list` | 分页列出签到任务 |
| `detail` | 查看任务和批次详情 |
| `sign` | 自动签到当前任务，支持 cron 周期调度 |
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

## 登录

```sh
./easycheck login
```

`login` 支持直接传入地址、账号和密码，以 `0600` 权限保存会话：

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
| `--retries COUNT` | 登录失败后的重试次数，默认 `3`（首次尝试之外最多再试三次）；`0` 关闭重试；也可设 `EASYCHECK_LOGIN_RETRIES` |
| `--retry-interval DURATION` | 两次登录尝试之间的等待时间，默认 `1m`；也可设 `EASYCHECK_LOGIN_RETRY_INTERVAL` |

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

登录失败会先发送已配置的 Telegram / 飞书通知，再等待指定间隔重试；重试等待响应 `Ctrl+C` / `SIGTERM`。最终失败会返回非零退出码，不保存会话。登录命令读取并校验通知渠道配置；未配置渠道时不会发送通知。

## 列表与详情

```sh
./easycheck list
./easycheck list --page 2 --page-size 20
./easycheck detail --pcid '<batch-id>'
```

`list` 的 `--page` 默认为 `1`，`--page-size` 默认为 `10`，两者须大于零。结果包含分页、任务状态、任务 ID（`rwid`）和批次 ID（`pcid`）。

`detail` 只需提供 `--pcid`，按当前登录用户和批次 ID 查询，无需提供 `rwid`。结果包含签到时间、状态、位置规则、允许地点和照片要求。

## 签到

自动查询当前可签到任务并执行一轮：

```sh
./easycheck sign

# 手动提交指定批次。
./easycheck sign --pcid '<batch-id>'
```

省略 `--pcid` 时自动翻页查询并筛选当前签到窗口内未签到的定位任务。提供 `--pcid` 时手动提交指定批次，从详情自动取得 `rwid`。默认选择第一个具有有效中心经纬度和正数半径的允许地点，在其半径的 90% 内生成坐标，保留 10% 的边界余量，再通过原站配置的腾讯地图按坐标解析位置名称。名称不再固定使用允许地点的名称；中心坐标或半径缺失时返回错误。

自定义坐标时，同时提供经度和纬度，地址自动解析：

```sh
./easycheck sign --pcid '<batch-id>' --longitude 10.5 --latitude 20.5
```

坐标须使用与原站地图一致的坐标系。也可以显式提供地址，跳过地图地址解析：

```sh
./easycheck sign \
  --pcid '<batch-id>' \
  --address '<address>' \
  --longitude 10.5 --latitude 20.5
```

| 签到选项 | 功能 |
| --- | --- |
| `--pcid ID` | 可选，省略时自动发现当前任务；cron 模式提供时只处理该批次 |
| `--address ADDRESS` | 可选，与经纬度一起提供时跳过自动地址解析；不能显式传空值 |
| `--longitude NUMBER` | 经度，有限数字，范围 `[-180, 180]` |
| `--latitude NUMBER` | 纬度，有限数字，范围 `[-90, 90]` |
| `--photo-id ID` | 已上传照片的文件标识 |
| `--outside-range` | 使用范围外签到分支 |
| `--remark TEXT` | 范围外签到备注，最多 100 个 UTF-16 字符 |
| `--cron EXPR` | 常驻按五段 cron 表达式周期执行，例如 `'0 8 * * *'` |
| `--timezone ZONE` | cron 的时区，默认 `Asia/Shanghai`（北京时间），须与 `--cron` 一起使用 |

范围外签到使用 `--outside-range --remark '<remark>'`，是否允许以服务端任务规则为准。定位生成、坐标校验、时间和照片要求均由客户端包及服务端处理。

默认定位和自定义定位均执行一次详情查询以取得任务 ID。需要自动地址时，另读取一次原站地图配置并查询一次腾讯地图逆地址解析接口。地图 Key 从当前站点读取，不需手动填写或写入会话；站点 Cookie 和身份令牌不会发送给地图服务。当前支持原站地图类型 `3`（腾讯地图），其他类型可显式指定地址。

查询、地址解析失败或响应缺少任务 ID 时不提交，也不会回退到固定地点名称；签到提交为一次。超时或连接中断后，可用 `detail` 核对批次状态。操作成功后发生输出或文件保存错误时，错误消息会明确标记操作已经成功。

## 定时签到

使用 `github.com/robfig/cron/v3` 在程序内常驻调度，无需安装系统 cron，也不需要提前知道批次 ID：

```sh
# 每天北京时间 08:00。
./easycheck sign --cron '0 8 * * *'

# 每天北京时间 08:00 和 20:00。
./easycheck sign --cron '0 8,20 * * *'

# 每 5 分钟检查一次当前可签到任务。
./easycheck sign --cron '*/5 * * * *'

# 工作日 08:00。
./easycheck sign --cron '0 8 * * MON-FRI'

# 指定其他时区。
./easycheck sign --cron '0 8 * * *' --timezone UTC

# 不常驻：立即自动签到一轮后退出。
./easycheck sign
```

表达式顺序为 `分 时 日 月 星期`，共五段，不包含秒字段；须加引号，避免 `*` 被 shell 展开。支持 cron 库的 `@daily`、`@hourly`、`@every 10m` 等简写，`@every` 间隔至少为 `1s`。时区统一通过 `--timezone` 指定，不使用表达式内的 `TZ=` / `CRON_TZ=`。程序内置时区数据，Docker 和 Windows 下不依赖主机默认时区。旧的 `--at`、`--after` 已移除。

启动时先校验表达式和选项、读取本地会话，输出下次执行时间；不会启动后立即补签，空闲等待时不联网。每次 cron 触发后：

1. 重新加载会话文件，自动翻页完整查询最新任务列表，再开始提交；同一 `pcid` 去重。
2. 筛选 `status` 为“进行中”、`zt` 不是“已签到”、`qdfs` 为“定位签到”，且当前时间满足 `kssj <= 当前时间 < jssj` 的任务。原站不带时区的日期按北京时间解释，与定时器的 `--timezone` 无关。时间缺失或格式无效时报告错误，不猜测窗口。
3. 多个符合条件的批次按列表顺序逐个处理；每项重新查询最新详情，再次核对窗口、方式与签到状态，随后生成定位、解析地址并提交一次。列表查询后已签到或已结束的任务跳过。
4. 没有符合条件的任务时输出提示并等待下次触发。单个批次失败不立即重试，继续处理其余批次；本轮失败记录到标准错误，调度器继续运行。下一轮重新查询最新状态，已签到的不再提交。列表查询失败时不提交任何批次。

每次触发重新加载会话，可在另一个终端执行 `login` 更新会话；会话失效时不使用旧令牌继续提交，不自动重新登录。前一轮仍在运行时跳过重叠触发，不并发签到或排队补执行。

`--json` 在每轮结束后输出一个完整汇总 JSON 对象，包含 `matched`、`succeeded`、`skipped`、`failed` 和逐项 `results`；成功项保留服务端原始 `response`，失败项记录 `error`。常驻运行会依次输出多个 JSON 对象，可用流式 JSON 解析；`--out` 原子覆盖为最新一轮结果，权限为 `0600`。标准输出不混入调度日志。手动 `--pcid` 立即签到的原始响应格式保持不变。

需保持进程或容器运行。`Ctrl+C`、`SIGTERM` 停止调度并取消当前请求，等待正在运行的一轮收尾。程序退出后不保留后台任务，不会补跑停机期间的计划。二维码签到不自动处理，照片要求仍须提供已上传的 `--photo-id`。

### 结果通知

登录和 `sign --cron ...` 使用以下通知渠道配置，默认不发送通知；Telegram 和飞书可单独启用，也可同时启用：

| 环境变量 | 用途 |
| --- | --- |
| `EASYCHECK_TG_BOT_TOKEN` | Telegram Bot Token，须与 Chat ID 一起填写 |
| `EASYCHECK_TG_CHAT_ID` | Telegram 目标私聊、群组或频道的 Chat ID，也支持频道 `@username` |
| `EASYCHECK_FEISHU_WEBHOOK_URL` | 飞书群自定义机器人的 Webhook 地址，填写后启用飞书通知 |
| `EASYCHECK_FEISHU_SECRET` | 可选，飞书机器人开启签名校验时填写密钥 |
| `EASYCHECK_NOTIFY_TIMEOUT` | 每个通知 HTTP 请求的超时，默认 `10s`，须大于零；独立于站点请求的 `--timeout` |

在 `.env` 中填入实际配置，未使用的渠道留空：

```dotenv
EASYCHECK_TG_BOT_TOKEN='your-bot-token'
EASYCHECK_TG_CHAT_ID='-1001234567890'
EASYCHECK_FEISHU_WEBHOOK_URL='https://open.feishu.cn/open-apis/bot/v2/hook/your-webhook-id'
EASYCHECK_FEISHU_SECRET=''
EASYCHECK_NOTIFY_TIMEOUT=10s
```

Telegram 通过 BotFather 创建机器人并取得 Token，目标会话需允许机器人发言；首次私聊先向机器人发送 `/start`。飞书使用群自定义机器人 Webhook；启用签名校验时填写对应密钥，程序自动生成时间戳与 HMAC-SHA256 签名。若飞书启用了关键词校验，可将 `EasyCheck` 设为允许关键词。

本机运行须先将配置导出到环境（见本文公共选项中的 `.env` 导出方式），再执行 `./easycheck sign --cron '*/5 * * * *'`；Compose 自动传入这些配置，修改 `.env` 后运行 `docker compose up -d` 生效。配置在登录或 cron 启动时读取并校验，填写不完整或超时无效会返回错误；其他非 cron 命令不读取通知配置。

每次登录尝试失败时，每个已配置渠道发送通知，包含时间、尝试次数和下一步重试计划；登录最终成功不额外通知。每轮 cron 在存在实际签到成功或失败时各发送一次汇总，包含执行时间（带时区）、任务名称、批次 ID 和成功/失败/跳过数量；部分成功仍通知，全部失败也会通知，消息过长时截断。没有任务、只有跳过时不通知。不带 `--cron` 的立即签到不发送。通知不包含原始接口响应、坐标、站点 Cookie 或登录令牌。

通知使用独立 HTTP 客户端，不跟随重定向；HTTP 成功后还会检查渠道业务状态码。任一渠道失败只记录到标准错误，仍尝试其他渠道，不改变登录/签到结果、不自动重试通知或签到，后续登录重试或 cron 调度继续。通知错误摘要不转发站点原始错误消息；日志不打印 Token、签名密钥、Webhook 地址或通知接口响应正文，标准输出/`--json` 格式保持不变。停止进程时会取消正在发送的通知及登录重试等待，不保留待发队列。

### Docker Compose

仓库提供 `compose.yaml`，自动登录后在后台常驻运行 cron：

```sh
git clone https://github.com/nhirsama/easycheck.git
cd easycheck
cp .env.example .env
# 编辑 .env，填写实际站点、账号、密码。
docker compose up -d
docker compose logs -f
```

Compose 自动读取 `.env`，无需手动导出变量。定时配置为：

```dotenv
# 默认每 5 分钟检查一次；每天 08:00 可改为 '0 8 * * *'。
EASYCHECK_CRON='*/5 * * * *'
EASYCHECK_TIMEZONE=Asia/Shanghai
```

上述两个变量由 Compose 转为 `sign --cron ... --timezone ...` 参数，本机 CLI 仍使用命令行选项。密码建议用单引号包裹，避免 `.env` 中的 `$` 被当作变量插值。

默认运行 `ghcr.io/nhirsama/easycheck:latest`；需要运行当前源码时使用 `docker compose up -d --build`。每次启动先执行 `login`：失败后默认间隔 1 分钟重试 3 次（`EASYCHECK_LOGIN_RETRIES`、`EASYCHECK_LOGIN_RETRY_INTERVAL` 可改），全部失败后才退出且不启动调度；配置通知渠道时每次失败都会发通知。登录成功后进入 `sign --cron ...`，启动时不会立即签到。容器内会话路径固定为 `/data/session.json`，不挂载卷，不需要持久化会话或提前运行登录命令，删除或重建容器后会话丢弃。

通过 `docker compose logs -f` 查看计划和执行结果。修改 `.env` 后运行 `docker compose up -d` 更新容器；会话过期时运行 `docker compose restart` 重新登录。`docker compose down` 停止调度并移除容器。

无人值守启动要求站点未开启登录验证码；开启时仍需交互输入，本配置不自动识别验证码。

## 输出与导出

`list`、`detail` 和 `sign` 支持以下选项：

| 选项 | 功能 |
| --- | --- |
| `--json` | 将完整 JSON 响应写入标准输出 |
| `--out FILE` | 将原始响应以 `0600` 权限保存到指定文件 |

```sh
./easycheck list --json
./easycheck detail --pcid '<batch-id>' --out tasks-session.json
```

命令结果和帮助使用标准输出；交互提示、错误使用标准错误。`--json` 的标准输出可直接用于管道处理。详情和原始响应会包含实际地点、坐标及账号相关业务信息。

| 退出码 | 含义 |
| --- | --- |
| `0` | 命令成功或帮助已显示 |
| `1` | 执行失败，包括客户端、网络、会话及输出错误 |
| `2` | 命令、参数解析或 CLI 参数检查失败 |

`Ctrl+C` 和 `SIGTERM` 会取消正在进行的 HTTP 请求。通过 `go run` 启动时，Go 启动器可能将程序的非零退出码转为自己的退出码；脚本可使用构建后的可执行文件读取上述退出码。

## 兼容入口

根目录支持 `go run . <命令>`，原有带选项的默认登录方式继续可用。入口检查命令 `check` 和旧的 `-check` 选项已移除：

```sh
go run . -username example-user -password-stdin
go run . detail --pcid '<batch-id>'
```

独立查询入口复用 `list` 和 `detail` 的参数、输出及导出逻辑：

```sh
go run ./cmd/checkin-inspect
go run ./cmd/checkin-inspect --pcid '<batch-id>'
go run ./cmd/checkin-inspect --help
```

提供 `--pcid` 时查询详情，其余情况查询列表。该入口也支持公共选项、`--page`、`--page-size`、`--json` 和 `--out`。

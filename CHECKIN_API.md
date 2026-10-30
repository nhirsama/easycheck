# 移动签到协议

本文仅包含通用协议字段及占位词。应用根地址记作 `<BASE_URL>`，来自 `EASYCHECK_BASE_URL` 或显式 `Options.BaseURL`，例如占位地址 `https://example.invalid/qdgj/`。接口相对于应用根路径拼接，地址未以 `/` 结尾时自动补齐。

## 登录

| 步骤 | 相对路径 | 说明 |
| --- | --- | --- |
| 初始化 | `GET <BASE_URL>` | 建立 Cookie 会话 |
| 验证码开关 | `GET <BASE_URL>ng/captcha/status` | `appId` 为协议应用标识 |
| 验证码图片 | `GET <BASE_URL>captchaServlet?v=<timestamp-ms>` | 与登录共用 CookieJar |
| 登录 | `POST <BASE_URL>login` | `application/x-www-form-urlencoded` |

表单字段为 `username`、`password`、`encryptTransfer`、`timestamp`、`digest`、`systemId`，验证码开启时附加 `captcha`。密码使用配置的 RSA 公钥做 PKCS#1 v1.5 加密，再以 Base64 编码。摘要原文为：

```text
username=<username>,password=<encrypted-password>,timestamp=<timestamp-ms>
```

摘要按兼容的前端 SHA-1 编码计算。登录成功判断使用 `meta.statusCode == 200`。

## 已登录请求

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

## 任务列表及详情

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
  "rwid": "<task-id>",
  "pcid": "<batch-id>"
}
```

任务位于 `data.list`。`rwid` 是任务 ID，`pcid` 是批次 ID；循环任务可共用任务 ID，但不能据历史批次推定当前批次。`status` 表示批次状态，`zt` 表示当前用户签到状态，`kssj`、`jssj` 表示起止时间。

`qdfs` 为签到方式，`qdtj` 为范围条件，`qddds` 为允许地点列表，`sfsctp`、`pzyq` 为照片要求。历史位置及照片字段可能为 null。查询成功判断为 `code == 200`，同时检查非零的 `meta.statusCode` 是否为 200。

## 定位签到

```text
POST <BASE_URL>api/sm-registration/qdgjQdrw/startSign
```

| 字段 | 值 |
| --- | --- |
| `rwid` / `pcid` | 最新查询得到的任务 / 批次 ID |
| `userid` | 当前会话用户 ID |
| `qddd` | 实际定位地址 |
| `jdzb` / `wdzb` | 实际定位经度 / 纬度，JSON 数字 |
| `qdtp` | 照片文件值，无照片时为 null |
| `sfxdfwwqd` / `bz` | 范围外分支附加 `"1"` 和备注 |

包只通过显式 `SignIn` 方法调用此接口，不自动重试、不自动取用允许地点中心坐标。服务端允许的时间、范围与照片条件以当前任务为准。

二维码结果路由 `#/mobile/student/qrResult` 在兼容前端中可能自动触发提交，不属于只读详情页面。该分支尚未封装。

## 仓库数据

仓库不保留真实账号、会话、任务 ID、定位地址、学校名称、域名、截图、抓取记录或下载的部署资源。协议测试全部使用合成数据和本地模拟服务。

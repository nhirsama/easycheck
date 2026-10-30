package qdgj

import (
	"net/http"
	"os"
	"strings"
	"time"
)

const defaultTimeout = 30 * time.Second

// Config 集中定义客户端的站点、登录、会话和 HTTP 配置。
// 客户端保存配置副本，供 Login、LoadSession 和 SaveSession 等方法复用。
type Config struct {
	BaseURL       string
	Username      string
	Password      string
	CaptchaSolver CaptchaSolver
	// SessionFile 是 LoadSession 和 SaveSession 使用的文件路径，默认为 session.json。
	SessionFile string
	UserAgent   string
	Timeout     time.Duration
	// HTTPClient 会被复制，其 Transport 可用于代理或本地测试。
	// 客户端使用独立 CookieJar，并将重定向响应作为 HTTP 结果处理。Timeout 非零时覆盖其超时。
	HTTPClient *http.Client
}

// Options 是 Config 的兼容名称。新代码使用 Config。
// Deprecated: use Config.
type Options = Config

// ConfigFromEnv 从 EASYCHECK_ 环境变量构造配置并填入默认值。
// 调用方可在创建对象前覆盖返回配置中的任意字段。
func ConfigFromEnv() Config {
	sessionFile := strings.TrimSpace(os.Getenv("EASYCHECK_SESSION_FILE"))
	if sessionFile == "" {
		sessionFile = "session.json"
	}
	return Config{
		BaseURL:     strings.TrimSpace(os.Getenv("EASYCHECK_BASE_URL")),
		Username:    os.Getenv("EASYCHECK_USERNAME"),
		Password:    os.Getenv("EASYCHECK_PASSWORD"),
		SessionFile: sessionFile,
		UserAgent:   MobileUserAgent,
		Timeout:     defaultTimeout,
	}
}

func (config Config) withDefaults() Config {
	defaults := ConfigFromEnv()
	if config.BaseURL == "" {
		config.BaseURL = defaults.BaseURL
	}
	if config.Username == "" {
		config.Username = defaults.Username
	}
	if config.Password == "" {
		config.Password = defaults.Password
	}
	if config.SessionFile == "" {
		config.SessionFile = defaults.SessionFile
	}
	if config.UserAgent == "" {
		config.UserAgent = defaults.UserAgent
	}
	return config
}

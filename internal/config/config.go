// Package config Web GUI 的运行时配置：JSON 文件 + WBGUI_* 环境变量覆盖。
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config GUI 侧全部可配置项。
//
// 设计取态：GUI 是 workbuddy2api 网关的**外部管理面板**，只通过 HTTP API 与该网关
// 通信；凭证与 config.json 直读磁盘（因为网关自身没有暴露配置读写接口）。
type Config struct {
	// Listen GUI 自身监听地址。
	Listen string `json:"listen"`

	// GatewayURL workbuddy2api 网关地址（OpenAI 兼容端点，默认同机 7863）。
	// 留空则探测常见位置。
	GatewayURL string `json:"gateway_url"`

	// GatewayAPIKey 网关 api_key：用于 /status、/v1/models、聊天测试台鉴权。
	// 留空时自动从上游 config.json 的 api_key 字段读取（单机部署零配置）。
	GatewayAPIKey string `json:"gateway_api_key"`

	// UpstreamAuthDir 网关账号凭证目录（./auths），GUI 在此直读/落盘 workbuddy-*.json。
	UpstreamAuthDir string `json:"auth_dir"`

	// UpstreamConfigFile 网关配置文件（config.json），供「配置」页在线读写。
	UpstreamConfigFile string `json:"config_file"`

	// BackupDir 配置备份目录。默认 ./data/backups。
	//
	// 为什么需要它：Docker 单文件挂载 config.json 时，容器内它的父目录属于容器
	// 可写层而非宿主机目录，备份写在那里会随容器重建丢失。此时备份改写入本目录
	// （应挂载到宿主机持久化路径）。
	BackupDir string `json:"backup_dir"`

	// CredentialsFile 面板登录凭据的持久化文件（JSON：{username,password}）。
	//
	// 为什么需要独立文件：config.json 在 Docker 里通常以 :ro 只读方式挂载，
	// 无法从网页改密码后写回。此文件放在可写、持久化的挂载目录（如 /data），
	// 修改密码时写这里，重启后生效且不被 config.json 覆盖。
	// 为空 = 不改密码持久化能力（仍可用环境变量 WBGUI_PASSWORD 控制）。
	CredentialsFile string `json:"credentials_file"`

	// DockerContainer 网关容器名；「系统」页的重启操作用它执行 docker restart。
	// 留空 = 关闭重启能力。
	DockerContainer string `json:"docker_container"`

	// DangerousOps 解锁高危操作（解禁账号 / 删除凭证 / 容器重启）。
	// 默认 false：这些动作不可通过上游 API 撤销，误点代价高。
	DangerousOps bool `json:"dangerous_ops"`

	// ReadOnly 全局只读模式：关闭一切写操作（登录、签到、改配置、落盘）。
	ReadOnly bool `json:"read_only"`

	// RefreshConfigOnLogin 登录成功落盘新凭证后自动重启网关容器以加载。
	// 对应 login.sh 的行为；需要 DockerContainer 非空且 DangerousOps 打开。
	RefreshConfigOnLogin bool `json:"refresh_config_on_login"`

	// TimeoutSeconds 调用网关与上游的单次 HTTP 超时（秒）。
	TimeoutSeconds int `json:"timeout_seconds"`

	// UI 面板登录凭据。
	UI UIConfig `json:"ui"`
}

// UIConfig 面板自身的访问凭据。
//
// 为什么强制默认口令而非留空放行：GUI 能读到 accessToken / refreshToken，
// 等同于账号完全控制权；留空 = 无鉴权会把风险直接暴露到网络上。
type UIConfig struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	SessionTTL string `json:"session_ttl"`

	TTL time.Duration `json:"-"`
}

// Default 默认配置（覆盖单机默认部署布局，即 /root/workbuddy2api）。
func Default() *Config {
	// 自动探测网关部署目录：优先 /root/workbuddy2api，其次工作目录。
	base := detectDeployDir()
	return &Config{
		Listen:               ":8787",
		GatewayURL:           "http://127.0.0.1:7863",
		GatewayAPIKey:        "",
		UpstreamAuthDir:      base + "/auths",
		UpstreamConfigFile:   base + "/config.json",
		BackupDir:            "./data/backups",
		CredentialsFile:      "",
		DockerContainer:      "workbuddy2api",
		DangerousOps:         false,
		ReadOnly:             false,
		RefreshConfigOnLogin: true,
		TimeoutSeconds:       120,
		UI: UIConfig{
			Username:   "admin",
			Password:   "workbuddy",
			SessionTTL: "12h",
		},
	}
}

// detectDeployDir 探测 workbuddy2api 网关部署目录。
func detectDeployDir() string {
	for _, p := range []string{"/root/workbuddy2api", "./workbuddy2api", ".."} {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			if _, err := os.Stat(p + "/config.json"); err == nil {
				return p
			}
		}
	}
	// 都探测不到时用相对路径，让用户改 config.json。
	return "."
}

// Load 读取配置：默认值 → JSON 文件（可选）→ 环境变量覆盖 → 校验。
func Load(path string) (*Config, error) {
	c := Default()
	if path != "" {
		raw, err := os.ReadFile(path)
		switch {
		case err == nil:
			// 防呆：把「网关的 config.json」当成面板配置读进来是最容易犯的部署错误
			// （两者字段名不同，静默解析会让面板监听到网关端口、用错凭证目录）。
			// 这里直接拒绝并给出明确指引，而不是带着错误配置启动。
			if err := rejectGatewayConfig(path, raw); err != nil {
				return nil, err
			}
			if err := json.Unmarshal(raw, c); err != nil {
				return nil, fmt.Errorf("解析配置 %s: %w", path, err)
			}
		case os.IsNotExist(err):
			// 配置文件缺失不是错误：纯默认 + 环境变量也能跑起来。
		default:
			return nil, fmt.Errorf("读取配置 %s: %w", path, err)
		}
	}
	applyEnv(c)
	if err := c.normalize(); err != nil {
		return nil, err
	}
	return c, nil
}

// rejectGatewayConfig 拒绝把 workbuddy2api 网关的配置文件当作面板配置加载。
//
// 判别依据：网关配置带有 schedule（签到排程）与 pool（账号池调优）这两个面板不存在的
// 顶层键，且监听端口就是网关端口。面板自己的配置只有 listen/gateway_url/ui 等字段。
func rejectGatewayConfig(path string, raw []byte) error {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil // 非对象/非法 JSON 交给后续 Unmarshal 报错
	}
	_, hasSchedule := probe["schedule"]
	_, hasPool := probe["pool"]
	if !hasSchedule || !hasPool {
		return nil
	}
	return fmt.Errorf(
		"%s 看起来是 workbuddy2api 网关的配置文件，而不是本面板的配置。\n"+
			"面板与网关的配置是两份不同的文件：\n"+
			"  · 面板配置（本文件）应包含 listen / gateway_url / ui 等字段\n"+
			"  · 网关配置请通过配置里的 \"config_file\" 字段指向（供「网关配置」页在线编辑）\n"+
			"请用 config.example.json 作为面板配置模板，或改用 WBGUI_* 环境变量启动。",
		path,
	)
}

func applyEnv(c *Config) {
	str := func(key string, dst *string) {
		if v := os.Getenv(key); v != "" {
			*dst = v
		}
	}
	boolean := func(key string, dst *bool) {
		if v := os.Getenv(key); v != "" {
			if b, err := strconv.ParseBool(v); err == nil {
				*dst = b
			}
		}
	}
	str("WBGUI_LISTEN", &c.Listen)
	str("WBGUI_GATEWAY_URL", &c.GatewayURL)
	str("WBGUI_GATEWAY_API_KEY", &c.GatewayAPIKey)
	str("WBGUI_AUTH_DIR", &c.UpstreamAuthDir)
	str("WBGUI_CONFIG_FILE", &c.UpstreamConfigFile)
	str("WBGUI_BACKUP_DIR", &c.BackupDir)
	str("WBGUI_CONTAINER", &c.DockerContainer)
	str("WBGUI_CREDENTIALS_FILE", &c.CredentialsFile)
	str("WBGUI_USERNAME", &c.UI.Username)
	str("WBGUI_PASSWORD", &c.UI.Password)
	str("WBGUI_SESSION_TTL", &c.UI.SessionTTL)
	boolean("WBGUI_DANGEROUS_OPS", &c.DangerousOps)
	boolean("WBGUI_READ_ONLY", &c.ReadOnly)
	boolean("WBGUI_REFRESH_CONFIG_ON_LOGIN", &c.RefreshConfigOnLogin)
	if v := os.Getenv("WBGUI_TIMEOUT_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.TimeoutSeconds = n
		}
	}
}

func (c *Config) normalize() error {
	if c.Listen == "" {
		c.Listen = ":8787"
	}
	// listen 容错：纯端口号补冒号（语义与 workbuddy2api 的 config 一致）。
	if !strings.Contains(c.Listen, ":") {
		c.Listen = ":" + c.Listen
	}
	c.GatewayURL = strings.TrimRight(strings.TrimSpace(c.GatewayURL), "/")
	if c.GatewayURL == "" {
		c.GatewayURL = "http://127.0.0.1:7863"
	}
	if c.TimeoutSeconds <= 0 {
		c.TimeoutSeconds = 120
	}
	if c.BackupDir == "" {
		c.BackupDir = "./data/backups"
	}
	if c.UI.Username == "" {
		c.UI.Username = "admin"
	}
	if c.UI.Password == "" {
		c.UI.Password = "workbuddy"
	}
	if c.UI.SessionTTL == "" {
		c.UI.SessionTTL = "12h"
	}
	ttl, err := time.ParseDuration(c.UI.SessionTTL)
	if err != nil {
		return fmt.Errorf("ui.session_ttl: %w", err)
	}
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	c.UI.TTL = ttl
	return nil
}

// Timeout 返回配置的超时 Duration。
func (c *Config) Timeout() time.Duration {
	return time.Duration(c.TimeoutSeconds) * time.Second
}

// UsingDefaultPassword 报告是否仍在用内置默认口令（用于前端安全横幅）。
func (c *Config) UsingDefaultPassword() bool {
	return c.UI.Username == "admin" && c.UI.Password == "workbuddy"
}

// ReadOnlyReason 返回只读原因文案；可写时返回空串。
func (c *Config) ReadOnlyReason() string {
	if c.ReadOnly {
		return "服务端已开启只读模式（read_only=true），所有写操作已禁用"
	}
	return ""
}

// ---------------------------------------------------------------------------
// 面板登录凭据的运行时改写（网页改密码用）
// ---------------------------------------------------------------------------

// StoredCredentials 凭据持久化文件的格式。
type StoredCredentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// LoadStoredCredentials 从凭据持久化文件读取用户名/密码（存在且合法时覆盖内存配置）。
// 任何读取/解析失败都不报错 —— 该文件是可选的，缺失时回退 config.json / 环境变量。
func (c *Config) LoadStoredCredentials() {
	if c.CredentialsFile == "" {
		return
	}
	raw, err := os.ReadFile(c.CredentialsFile)
	if err != nil {
		return
	}
	var sc StoredCredentials
	if err := json.Unmarshal(raw, &sc); err != nil {
		return
	}
	if sc.Username != "" {
		c.UI.Username = sc.Username
	}
	if sc.Password != "" {
		c.UI.Password = sc.Password
	}
}

// SaveStoredCredentials 原子写回凭据文件（供网页改密码）。
// 只写 username/password 两个字段；该文件由面板专用，不混入其他配置。
func (c *Config) SaveStoredCredentials(username, password string) error {
	if c.CredentialsFile == "" {
		return fmt.Errorf("未配置凭据持久化路径（credentials_file），无法保存新密码")
	}
	if strings.TrimSpace(username) == "" || password == "" {
		return fmt.Errorf("用户名和密码都不能为空")
	}
	sc := StoredCredentials{Username: strings.TrimSpace(username), Password: password}
	raw, err := json.MarshalIndent(sc, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化凭据失败: %w", err)
	}
	raw = append(raw, '\n')

	dir := filepath.Dir(c.CredentialsFile)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("创建凭据目录失败: %w", err)
	}
	// 原子写：临时文件 + rename，避免并发读读到半截。
	tmp := c.CredentialsFile + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return fmt.Errorf("写凭据文件失败: %w", err)
	}
	if err := os.Rename(tmp, c.CredentialsFile); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("替换凭据文件失败: %w", err)
	}
	return nil
}

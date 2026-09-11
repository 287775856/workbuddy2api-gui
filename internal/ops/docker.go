// docker.go Docker 集成：查询网关容器状态、重启容器以加载新账号/新配置。
//
// 为什么需要重启：workbuddy2api 只在启动时扫描 auths/ 目录（SyncToDir）并读取
// config.json；新增账号或改配置后必须重启进程才会生效。GUI 通过 `docker restart`
// 复刻 login.sh 的行为，免去用户登录服务器敲命令。
package ops

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ContainerInfo 网关容器状态快照。
type ContainerInfo struct {
	Name      string    `json:"name"`
	Available bool      `json:"available"` // docker 命令是否可用
	Exists    bool      `json:"exists"`
	Running   bool      `json:"running"`
	Status    string    `json:"status"`
	Health    string    `json:"health"`
	Image     string    `json:"image"`
	StartedAt time.Time `json:"started_at,omitempty"`
	Error     string    `json:"error,omitempty"`
	Disabled  bool      `json:"disabled"` // 未配置容器名
}

// DockerAvailable 报告 docker CLI 是否可用（GUI 可能跑在无 docker 的环境里，
// 或容器内未挂载 docker.sock —— 此时重启能力自动降级，不影响其他功能）。
func (s *Service) DockerAvailable() bool {
	if s.cfg.DockerContainer == "" {
		return false
	}
	return exec.Command("docker", "version", "--format", "{{.Server.Version}}").Run() == nil
}

// ContainerStatus 查询网关容器状态。
func (s *Service) ContainerStatus(ctx context.Context) *ContainerInfo {
	info := &ContainerInfo{Name: s.cfg.DockerContainer}
	if s.cfg.DockerContainer == "" {
		info.Disabled = true
		info.Error = "未配置容器名（docker_container），重启能力已关闭"
		return info
	}
	if _, err := exec.LookPath("docker"); err != nil {
		info.Error = "环境中找不到 docker 命令，重启能力不可用"
		return info
	}
	info.Available = true

	// 用 docker inspect 一次拿到状态/健康/镜像/启动时间。
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "inspect", "--format",
		"{{.State.Running}}|{{.State.Status}}|{{if .State.Health}}{{.State.Health.Status}}{{else}}-{{end}}|{{.Config.Image}}|{{.State.StartedAt}}",
		s.cfg.DockerContainer).Output()
	if err != nil {
		info.Error = "容器不存在或查询失败：" + firstLine(err.Error())
		return info
	}
	info.Exists = true
	parts := strings.Split(strings.TrimSpace(string(out)), "|")
	if len(parts) >= 5 {
		info.Running = parts[0] == "true"
		info.Status = parts[1]
		info.Health = parts[2]
		info.Image = parts[3]
		if t, perr := time.Parse(time.RFC3339Nano, parts[4]); perr == nil {
			info.StartedAt = t
		}
	}
	return info
}

// RestartContainer 重启网关容器（高危操作）。
func (s *Service) RestartContainer(ctx context.Context) (string, error) {
	if err := s.ensureDangerous(); err != nil {
		return "", err
	}
	if s.cfg.DockerContainer == "" {
		return "", fmt.Errorf("未配置容器名（docker_container），无法重启")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return "", fmt.Errorf("环境中找不到 docker 命令，无法重启")
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "restart", s.cfg.DockerContainer).CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return "", fmt.Errorf("重启容器失败: %s", firstLine(text))
	}
	return fmt.Sprintf("容器 %s 已重启", s.cfg.DockerContainer), nil
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	if len(s) > 300 {
		return s[:300]
	}
	return s
}

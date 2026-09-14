// configfile.go 读写 workbuddy2api 网关的 config.json。
//
// 安全取态：网关自身不提供配置读写接口，GUI 直接改磁盘文件。因此必须做到
// 「写入前先备份 + 先校验 JSON + 原子替换」，避免一次误输入把网关配置写坏导致起不来。
package ops

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"workbuddy2api-gui/internal/fsutil"
)

// UpstreamConfigMeta 配置文件元信息。
type UpstreamConfigMeta struct {
	Path        string    `json:"path"`
	Exists      bool      `json:"exists"`
	Size        int64     `json:"size"`
	ModTime     time.Time `json:"mod_time,omitempty"`
	BackupPath  string    `json:"backup_path,omitempty"`
	BackupAt    time.Time `json:"backup_at,omitempty"`
	ParseError  string    `json:"parse_error,omitempty"`
	IsValidJSON bool      `json:"is_valid_json"`
	RestartNote string    `json:"restart_note"`
}

// ReadUpstreamConfig 读取网关 config.json，返回解析后的对象与元信息。
// 文件不存在时不报错：返回空对象 + Exists=false，让前端从默认模板开始编辑。
func (s *Service) ReadUpstreamConfig() (map[string]any, *UpstreamConfigMeta, error) {
	path := s.cfg.UpstreamConfigFile
	if path == "" {
		return nil, nil, fmt.Errorf("未配置网关配置文件路径（config_file）")
	}
	meta := &UpstreamConfigMeta{
		Path:        path,
		RestartNote: "修改 config.json 后必须重启网关容器才会生效",
	}
	st, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{}, meta, nil
		}
		return nil, meta, fmt.Errorf("读取配置失败: %w", err)
	}
	meta.Exists = true
	meta.Size = st.Size()
	meta.ModTime = st.ModTime()

	// 备份文件信息（若存在）。
	if bp, bt := s.backupInfo(path); bp != "" {
		meta.BackupPath = bp
		meta.BackupAt = bt
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, meta, fmt.Errorf("读取配置失败: %w", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		meta.ParseError = err.Error()
		return nil, meta, fmt.Errorf("config.json 不是合法 JSON: %w", err)
	}
	meta.IsValidJSON = true
	return doc, meta, nil
}

// WriteUpstreamConfig 写入网关 config.json。
//
// 流程：JSON 序列化 → 首次写入时备份原文件 → 原子替换（单文件 bind mount 下自动回退原地写）。
// 任何一步失败都不会让原文件处于半截状态（见 fsutil.WriteFileAtomic 的说明）。
func (s *Service) WriteUpstreamConfig(doc map[string]any) error {
	_, err := s.WriteUpstreamConfigDetailed(doc)
	return err
}

// WriteUpstreamConfigDetailed 同 WriteUpstreamConfig，但额外告知是否走了
// 「单文件挂载回退路径」——回退写不是原子的，值得在响应里提示用户。
func (s *Service) WriteUpstreamConfigDetailed(doc map[string]any) (fallback bool, err error) {
	if err := s.ensureWritable(); err != nil {
		return false, err
	}
	path := s.cfg.UpstreamConfigFile
	if path == "" {
		return false, fmt.Errorf("未配置网关配置文件路径（config_file）")
	}
	// 先做一次校验性序列化，确保对象本身可编码。
	raw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return false, fmt.Errorf("配置无法序列化: %w", err)
	}
	raw = append(raw, '\n')

	// 首次写入前备份原始文件（不覆盖已有备份，保留「最初的样子」）。
	if err := s.ensureBackup(path); err != nil {
		return false, err
	}

	return fsutil.WriteFileAtomic(path, raw, 0o600)
}

// backupSuffix 备份文件后缀。
const backupSuffix = ".gui.bak"

// ensureBackup 确保存在一份「首次保存前」的原始配置备份；已存在则不覆盖。
//
// 为什么备份位置要特别处理：Docker 部署时 config.json 常以**单文件**方式挂载，
// 此时容器内 /gateway 目录是容器自己的可写层，而不是宿主机目录 —— 备份写在那里
// 会随容器重建而消失（用户以为有回退点，实际没有）。因此：
//   - 若配置文件所在目录本身是挂载进来的（目录可写且与文件同设备），直接写旁边；
//   - 否则回退到面板自己的数据目录（配置里的 backup_dir / 默认 ./data），
//     并在元信息里把真实路径告诉前端。
func (s *Service) ensureBackup(path string) error {
	// 优先：与目标文件同目录（同设备说明目录真被挂载进来了）。
	if dest := path + backupSuffix; sameDevice(path, filepath.Dir(path)) {
		if _, err := os.Stat(dest); err == nil {
			return nil // 已有备份，保留最初版本
		}
		if orig, err := os.ReadFile(path); err == nil {
			if werr := os.WriteFile(dest, orig, 0o600); werr != nil {
				return fmt.Errorf("写入备份失败（已中止，未改动原文件）: %w", werr)
			}
			return nil
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("读取原文件失败（已中止，未改动原文件）: %w", err)
		}
		return nil
	}

	// 回退：写到面板数据目录，保证备份能跨容器重建存活。
	dest := s.backupPathFor(path)
	if _, err := os.Stat(dest); err == nil {
		return nil
	}
	orig, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 目标还不存在（首次创建），无需备份
		}
		return fmt.Errorf("读取原文件失败（已中止，未改动原文件）: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		return fmt.Errorf("创建备份目录失败: %w", err)
	}
	if err := os.WriteFile(dest, orig, 0o600); err != nil {
		return fmt.Errorf("写入备份失败: %w", err)
	}
	return nil
}

// backupPathFor 返回回退备份的绝对路径（面板数据目录下的固定文件名）。
func (s *Service) backupPathFor(path string) string {
	dir := s.cfg.BackupDir
	if dir == "" {
		dir = "./data/backups"
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	// 用目标文件名做区分，避免将来支持多路径时互相覆盖。
	return filepath.Join(dir, filepath.Base(path)+backupSuffix)
}

// backupInfo 返回实际生效的备份路径与其修改时间（供元信息展示）。
func (s *Service) backupInfo(path string) (string, time.Time) {
	candidates := []string{path + backupSuffix, s.backupPathFor(path)}
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil {
			return c, st.ModTime()
		}
	}
	return "", time.Time{}
}

// sameDevice 报告 path 与其所在目录是否在同一文件系统上。
// 不同设备号 = 单文件 bind mount（或跨设备挂载），此时目录不反映宿主机目录。
func sameDevice(path, dir string) bool {
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	di, err := os.Stat(dir)
	if err != nil {
		return false
	}
	fsDev, ok1 := fsutil.DeviceID(fi)
	dsDev, ok2 := fsutil.DeviceID(di)
	if !ok1 || !ok2 {
		// 非 Unix：无法判断设备，按"同目录可写"处理。
		return true
	}
	return fsDev == dsDev
}

// ResetUpstreamConfig 从备份恢复网关 config.json（高危：需 dangerous_ops）。
func (s *Service) ResetUpstreamConfig() error {
	if err := s.ensureDangerous(); err != nil {
		return err
	}
	path := s.cfg.UpstreamConfigFile
	backup, _ := s.backupInfo(path)
	if backup == "" {
		return fmt.Errorf("没有可用的备份文件（保存过一次配置后才会生成）")
	}
	raw, err := os.ReadFile(backup)
	if err != nil {
		return err
	}
	var probe map[string]any
	if err := json.Unmarshal(raw, &probe); err != nil {
		return fmt.Errorf("备份文件不是合法 JSON，拒绝恢复: %w", err)
	}
	if _, err := fsutil.WriteFileAtomic(path, raw, 0o600); err != nil {
		return err
	}
	return nil
}

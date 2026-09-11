// Package fsutil 原子文件写入，兼容 Docker 单文件 bind mount。
//
// 背景（真实踩坑）：面板部署时通常把网关的 config.json 以**单文件**方式挂进容器
// （docker-compose 的 `- /host/config.json:/gateway/config.json`）。这种挂载点的
// 本质是把宿主机的那个 inode 绑到容器内路径上，父目录也是只读语义的一部分，
// 因此经典做法 `写 tmp → rename 覆盖目标` 会因为 rename 无法跨挂载点替换而失败：
//
//	mv: can't rename '/gateway/config.json.gui.tmp': Resource busy
//	rename ...: device or resource busy   (EBUSY)
//
// 只在单文件挂载下才会发生；目录挂载（如整目录挂到 /app/auths）不受影响。
// 所以这里保留「优先原子替换」，仅在遇到 EBUSY/EXDEV 时回退为「原地重写」，
// 并在回退路径上做三件事降低风险：先写完 tmp 验证可写 → 再 truncate 原地写 →
// fsync 落盘。调用方在写之前都会先备份原文件，因此最坏情况也能人工恢复。
package fsutil

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// WriteFileAtomic 原子写入文件（0600 权限）。
//
// 行为：
//  1. 先写 <path>.tmp-<random> 再 rename 覆盖 —— 正常路径完全原子，不会出现半截文件。
//  2. 若 rename 因 EBUSY/EXDEV 失败（Docker 单文件 bind mount），回退为原地重写。
//
// 返回的 fallback 为 true 表示走了回退路径（非原子），供调用方在日志/响应中提示。
func WriteFileAtomic(path string, raw []byte, perm os.FileMode) (fallback bool, err error) {
	if err := os.MkdirAll(dirOf(path), 0o700); err != nil {
		return false, fmt.Errorf("创建目录: %w", err)
	}

	tmp := path + ".tmp"
	// 先写临时文件：同时也验证了目标目录可写，避免"回退路径写到一半才发现不可写"。
	if err := os.WriteFile(tmp, raw, perm); err != nil {
		// tmp 都写不了（目录只读/权限不足）：直接报错，不要动原文件。
		return false, fmt.Errorf("写临时文件: %w", err)
	}
	if err := os.Rename(tmp, path); err == nil {
		return false, nil
	} else if !isBindMountRenameErr(err) {
		_ = os.Remove(tmp)
		return false, fmt.Errorf("替换文件: %w", err)
	}

	// ── 回退：单文件 bind mount，rename 无法替换目标 inode ──────────────
	// 此时 tmp 已写好，把它内容原地灌进目标文件。
	if err := overwriteInPlace(path, raw, perm); err != nil {
		_ = os.Remove(tmp)
		return false, fmt.Errorf("原地写入失败（目标文件可能是 Docker 单文件挂载）: %w", err)
	}
	_ = os.Remove(tmp)
	return true, nil
}

// overwriteInPlace 截断并原地重写文件内容，完成后 fsync 确保落盘。
func overwriteInPlace(path string, raw []byte, perm os.FileMode) error {
	// 目标不存在（首次创建）时先建出来。
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := f.Write(raw); err != nil {
			return err
		}
		return f.Sync()
	}

	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(raw); err != nil {
		return err
	}
	// fsync：单文件挂载下 rename 语义不可用，落盘保证只能靠 sync。
	return f.Sync()
}

// isBindMountRenameErr 判定 rename 失败是否由 bind mount 引起。
//
// EBUSY：Docker 单文件挂载最典型的表现（"device or resource busy"）。
// EXDEV：跨设备 rename（tmp 与目标不在同一文件系统）。
func isBindMountRenameErr(err error) bool {
	return errors.Is(err, syscall.EBUSY) || errors.Is(err, syscall.EXDEV)
}

func dirOf(path string) string {
	for i := len(path) - 1; i >= 0; i-- {
		if path[i] == '/' || path[i] == '\\' {
			if i == 0 {
				return "/"
			}
			return path[:i]
		}
	}
	return "."
}

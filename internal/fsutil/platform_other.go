//go:build !unix

package fsutil

import "os"

// DeviceID 在非 Unix 平台没有设备号概念，返回 ok=false 由调用方按默认处理。
func DeviceID(os.FileInfo) (uint64, bool) { return 0, false }

// FileOwner 在非 Unix 平台没有 uid/gid 概念，返回 ok=false 由调用方降级。
func FileOwner(os.FileInfo) (int, int, bool) { return 0, 0, false }

// PermBitsEnforced 在非 Unix 平台恒为 false：Windows 用 ACL 而非 mode 位，
// 传给 os.OpenFile 的 0600 不会被内核强制，因此权限断言在该平台无意义。
func PermBitsEnforced() bool { return false }

//go:build !windows

package main

import "suiyi/internal/config"

// killExisting 非 Windows 平台不做启动前进程清理（端口冲突由 fail 明确报错）
func killExisting(*config.Config) []string { return nil }

// listeningPIDs 非 Windows 平台不查询端口占用者（诊断信息由系统工具给出）
func listeningPIDs(int) []uint32 { return nil }

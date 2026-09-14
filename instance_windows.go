//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"

	"suiyi/internal/config"
)

// th32csSnapProcess 进程快照（Toolhelp32）
const th32csSnapProcess = 0x00000002

// processQueryLimitedInformation OpenProcess 所需权限
const processQueryLimitedInformation = 0x1000

// killExisting 启动前清理：结束已在运行的旧实例与本地推理进程，释放 API / 引擎端口，
// 然后由调用方重新启动（避免「端口被占用 / 引擎起不来」）。
//
// 只清理确属本程序的进程，避免误伤无关服务：
//   - 与自身同名的进程（旧实例，可能尚未绑定端口）
//   - 占用 API / 引擎端口的进程中：名为 llama-server / hy-mt，或可执行文件位于应用目录树下
//
// 结束方式为 taskkill /F /T（连同子进程），返回被结束进程的可读描述。
func killExisting(cfg *config.Config) []string {
	self := uint32(os.Getpid())
	selfExe, err := os.Executable()
	if err != nil {
		return nil
	}
	selfName := strings.ToLower(filepath.Base(selfExe))
	appDir := strings.ToLower(filepath.Dir(selfExe))
	rootDir := strings.ToLower(filepath.Dir(appDir))

	targets := map[uint32]string{} // pid → 描述

	// 1) 同名主程序（旧实例可能还没监听端口）
	for _, pid := range processesByName(selfName) {
		if pid != self {
			targets[pid] = fmt.Sprintf("%s (PID %d)", selfName, pid)
		}
	}

	// 2) 占用 API / 引擎端口的进程（残留的 llama-server / 旧实例）
	for _, port := range []int{cfg.APIPort, cfg.EnginePort} {
		for _, pid := range listeningPIDs(port) {
			if pid == self {
				continue
			}
			if _, seen := targets[pid]; seen {
				continue
			}
			path := processPath(pid)
			base := strings.ToLower(filepath.Base(path))
			ours := base == selfName ||
				base == "llama-server.exe" || base == "hy-mt.exe" ||
				underDir(path, appDir) || underDir(path, rootDir)
			if ours {
				targets[pid] = fmt.Sprintf("%s (PID %d, 端口 %d)", base, pid, port)
			}
		}
	}
	if len(targets) == 0 {
		return nil
	}

	// 顺序：先杀主程序（taskkill /T 会连同子进程一起结束，不留 llama-server），
	// 再补杀仍在监听的推理进程。
	// 不能反过来先杀引擎：旧实例的守候 goroutine 会在引擎退出后立刻重启它，
	// 与 /T 的进程树枚举竞争，会留下孤儿 llama-server。
	type target struct {
		pid  uint32
		desc string
		app  bool // 是否为主程序（同名 exe）
	}
	list := make([]target, 0, len(targets))
	for pid, desc := range targets {
		list = append(list, target{pid: pid, desc: desc, app: strings.HasPrefix(desc, selfName)})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].app != list[j].app {
			return list[i].app
		}
		return list[i].pid < list[j].pid
	})

	var killed []string
	live := processSet()
	for _, t := range list {
		if !live[t.pid] {
			continue // 已被上一步连带结束
		}
		if err := exec.Command("taskkill", "/F", "/T", "/PID",
			strconv.FormatUint(uint64(t.pid), 10)).Run(); err != nil {
			// taskkill 的报错文本随系统语言本地化，不直接回显；用存活性判断真实结果
			if processSet()[t.pid] {
				fmt.Fprintf(os.Stderr, "结束进程失败 %s: %v\n", t.desc, err)
				continue
			}
		}
		killed = append(killed, t.desc)
	}
	return killed
}

// processesByName 按可执行文件名列出进程 PID（大小写不敏感）
func processesByName(name string) []uint32 {
	snap, err := windows.CreateToolhelp32Snapshot(th32csSnapProcess, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	var pids []uint32
	for err = windows.Process32First(snap, &entry); err == nil; err = windows.Process32Next(snap, &entry) {
		if strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), name) {
			pids = append(pids, entry.ProcessID)
		}
	}
	return pids
}

// processSet 取当前全部进程 PID 集合（判断进程是否仍存活）
func processSet() map[uint32]bool {
	snap, err := windows.CreateToolhelp32Snapshot(th32csSnapProcess, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	set := map[uint32]bool{}
	for err = windows.Process32First(snap, &entry); err == nil; err = windows.Process32Next(snap, &entry) {
		set[entry.ProcessID] = true
	}
	return set
}

// listeningPIDs 解析 netstat，返回监听指定端口的进程 PID
func listeningPIDs(port int) []uint32 {
	out, err := exec.Command("netstat", "-ano", "-p", "tcp").Output()
	if err != nil {
		return nil
	}
	suffix := ":" + strconv.Itoa(port)
	var pids []uint32
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 5 || !strings.Contains(strings.ToUpper(fields[3]), "LISTEN") {
			continue
		}
		if !strings.HasSuffix(fields[1], suffix) { // fields[1] = 本地地址
			continue
		}
		if pid, err := strconv.ParseUint(fields[4], 10, 32); err == nil {
			pids = append(pids, uint32(pid))
		}
	}
	return pids
}

// processPath 取进程可执行文件完整路径（失败返回空串）
func processPath(pid uint32) string {
	h, err := windows.OpenProcess(processQueryLimitedInformation, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)

	buf := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(h, 0, &buf[0], &size); err != nil {
		return ""
	}
	return windows.UTF16ToString(buf[:size])
}

// underDir 判断路径是否位于指定目录之下
func underDir(path, dir string) bool {
	if path == "" || dir == "" {
		return false
	}
	return strings.HasPrefix(strings.ToLower(path), dir+string(os.PathSeparator))
}

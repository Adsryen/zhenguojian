// Package main —— app.log：绑定到达日志（第九十六轮）。
//
// 为什么必须有这份日志（alpha.40 真机数据逼出来的）：
//   徽标 8 个字段互相矛盾——前端自认 setBounds 发了 3 次且「上报异常=none」，
//   Go 侧却 applyCount=0（一次 ApplyBounds 都没执行）。当时唯一能区分
//   「IPC 没到 Go」和「到了但没执行」的证据是让用户按 Ctrl+Shift+F12
//   跑控制台探针，而用户没回。从这一版起产品自己留现场：
//
//     DataDir 下 app.<pid>.log（追加写，单行文本不依赖解析器）
//
//   - 启动行：exe 身份自证（version/revision）——version=? 之谜不用再靠猜；
//   - PlayerSetBounds 每次**到达** Go 记一行（入参 + 锁等待耗时）：
//     前端说发了 3 次而这里 0 行 = IPC 投递层丢消息/挂起（前端看门狗会同时
//     亮 pending>8s，两侧读数对撞）；这里 3 行但徽标 applyCount=0 =
//     到了 Go 却没执行（锁被别的持锁者卡住，waitMs 字段给出排队时长）。
//
// 采样策略（防膨胀：拖动/窗口缩放期间前端高频上报）：前 10 条全记、
// show 翻转（false→true 是可见性的关键时刻）必记、其余每 50 条记 1 条。
// 诊断目标是「起手那几次 + 时序」，稳态刷几百行没有信息量。
//
// 失败语义：日志写失败**永不**影响播放路径（DataDir 不可写/磁盘满都吞掉，
// 只把错误记进内存——这是诊断工具不是产品功能）。

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"zhenguojian/client-v2/internal/native"
)

type appLog struct {
	mu      sync.Mutex
	path    string
	lastErr string // 最近一次打开失败的错误（空=健康）
	sent    int    // 已收到的 setBounds 次数（采样前的原始计数）
	show    bool   // 上一次的 show（翻转检测）
	bytes   int64  // 本进程累计写入字节（上限防膨胀）
}

var appLogger = &appLog{}

// logByteCap 单进程 app.log 写入上限：诊断日志不允许反过来撑爆用户磁盘。
// Wails 库的 TRACE 级别在窗口创建期会有一阵子量，上限兜底。
const logByteCap = int64(2 << 20) // 2 MiB

// lineCap 单行截断：Wails 的 "json call result data" 会把整个 IPC 回执
// 打进 TRACE（弹幕窗体这类大载荷），截断后保留诊断价值又不上 MB。
const lineCap = 2000

// appLogStartup 启动行：exe 身份自证。buildVersion 由 -ldflags 注入，
// revision 取自 exe 同级 build-info.json（发布流程写入）；开发构建两者都空。
func appLogStartup(version, revision string) {
	appLogger.mu.Lock()
	defer appLogger.mu.Unlock()
	v := version
	if v == "" {
		v = "(未注入)"
	}
	r := revision
	if r == "" {
		r = "(unknown)"
	}
	appLogger.writeLine(fmt.Sprintf("startup version=%s revision=%s pid=%d t=%s",
		v, r, os.Getpid(), time.Now().Format(time.RFC3339)))
}

// appLogSetBoundsArrived 在 PlayerSetBounds **函数体一进入、拿锁之前**调用。
// arrived 从此=「IPC 投递成功、进了函数体」的真值——与锁状态无关。
// alpha.41 的计数器压在锁后，arrived=0 分不清「没到」还是「卡锁」。
func appLogSetBoundsArrived() {
	appLogger.mu.Lock()
	appLogger.sent++
	appLogger.mu.Unlock()
}

// appLogSetBounds 记一次 setBounds 执行（在拿到 embedMu 之后）。
// waitMs=拿到锁前等了多久（>1000ms 的排队会原样留在行里——
// 「到了 Go 但锁被别人占」的直接证据；arrived 已含本次，这里不再计数）。
func appLogSetBounds(x, y, w, h int, show bool, waitMs int64) {
	appLogger.mu.Lock()
	defer appLogger.mu.Unlock()
	n := appLogger.sent
	flip := show != appLogger.show
	appLogger.show = show
	// 采样：起手 10 条全记 + show 翻转必记 + 稳态每 50 条 1 条。
	if n > 10 && !flip && n%50 != 0 {
		return
	}
	appLogger.writeLine(fmt.Sprintf("setbounds n=%d x=%d y=%d w=%d h=%d show=%v waitMs=%d",
		n, x, y, w, h, show, waitMs))
}

// appLogEvent 自由事件行（attach/open/detach 等关键生命周期点），不采样。
func appLogEvent(f string, a ...any) {
	appLogger.mu.Lock()
	defer appLogger.mu.Unlock()
	appLogger.writeLine(fmt.Sprintf(f, a...))
}

// appLogSetBoundsCount 诊断绑定用：已累计的 setBounds 到达次数（0=从未到达）。
func appLogSetBoundsCount() int {
	appLogger.mu.Lock()
	defer appLogger.mu.Unlock()
	return appLogger.sent
}

// appLoggerFail 最近一次日志打开错误（空=健康）；诊断绑定用。
func (l *appLog) fail() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lastErr
}

/* ── Wails 进程级日志的 app.log 出口 ──

   为什么必须有（alpha.41 真机数据逼出来的）：到达计数在 embedMu 锁**前**，
   但它仍然只覆盖 PlayerSetBounds 一个绑定。真机指纹是「0 参数绑定
   （attach/diag/Ping）全到达、5 参数 setBounds 全没到」——这个不对称发生在
   **Wails dispatcher 层**（ParseArgs/Call），在绑定函数体外面。唯一的取证口
   是 Wails 自己的日志：dispatcher.go 的 panic-recover 会打
   "process message error: %s -> %s"（含被吞的报错原文），ParseArgs 失败会打
   "error parsing arguments"——但默认日志级别生产=ERROR、且打向 exe 的 stderr
   （-H=windowsgui 下根本看不见）。
   所以把 Wails 的 logger（options.App.Logger 公开字段）接到 app.<pid>.log，
   级别 TRACE（dispatcher 的 "json call result data" 也在 TRACE，能看全每条
   IPC 的回执）。噪音控制：只放行白名单前缀，字节上限+单行截断兜底。 */

var wailsLogKeep = []string{
	"process message error:",          // dispatcher panic-recover 出口（alpha.41 指纹的头号嫌疑）
	"Unknown message from front end:", // 未知消息类型
	"method '",                        // "method 'X' not registered"
	"error parsing arguments",         // ParseArgs 失败（含原文）
	"unknown Browser message",
	"cannot get value at",
	"json call result data:", // TRACE：每条 'C' 调用的回执（定案用，截断兜底）
}

type wailsLogToAppLog struct{}

// wailsLogLine 是唯一的写入点：先过白名单（TRACE 级别下 Wails 启动期有海量
// 窗口/资源流水，全放进来日志就没法读了），再 app.log + stdout 双写
// （stdout 保 wails dev 的控制台习惯；生产 exe -H=windowsgui 无控制台，
// os.Stdout 写失败也无害）。
func wailsLogLine(level, s string) {
	if level == "TRACE" {
		// TRACE 里只放行 IPC 回执行；其余 TRACE 是启动期流水噪音。
		if !hasPrefixAny(s, []string{"json call result data:"}) {
			return
		}
	} else if !hasPrefixAny(s, wailsLogKeep) {
		return
	}
	appLogger.writeLine(fmt.Sprintf("wails.%s %s", level, s))
	fmt.Fprintf(os.Stdout, "[wails:%s] %s\n", level, s)
}

func (wailsLogToAppLog) Print(s string)   { wailsLogLine("PRINT", s) }
func (wailsLogToAppLog) Trace(s string)   { wailsLogLine("TRACE", s) }
func (wailsLogToAppLog) Debug(s string)   { wailsLogLine("DEBUG", s) }
func (wailsLogToAppLog) Info(s string)    { wailsLogLine("INFO", s) }
func (wailsLogToAppLog) Warning(s string) { wailsLogLine("WARN", s) }
func (wailsLogToAppLog) Error(s string)   { wailsLogLine("ERROR", s) }
func (wailsLogToAppLog) Fatal(s string)   { wailsLogLine("FATAL", s) }

// 注：internal/logger 的 Fatal 在调完本方法后自己 os.Exit(1)——与默认日志
// 行为一致，这里不重复实现。

func hasPrefixAny(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if len(s) >= len(p) && s[:len(p)] == p {
			return true
		}
	}
	return false
}

func (l *appLog) writeLine(s string) {
	path, err := l.pathLocked()
	if err != nil {
		l.lastErr = err.Error()
		return // 诊断失败永不阻塞产品路径
	}
	if l.bytes >= logByteCap {
		return // 超上限：宁可不写也不撑爆磁盘（诊断不是产品功能）
	}
	if len(s) > lineCap {
		s = s[:lineCap] + "…(truncated)"
	}
	line := fmt.Sprintf("%s %s\n", time.Now().Format("2006-01-02T15:04:05.000"), s)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		l.lastErr = err.Error()
		return
	}
	defer f.Close()
	n, _ := f.WriteString(line)
	l.bytes += int64(n)
}

// pathLocked 惰性解析日志路径（DataDir 每次现算：ZHENGUO_DATA 变更/测试隔离）。
// 文件名带 pid：进程崩溃不关文件也不影响下一次的追加。
func (l *appLog) pathLocked() (string, error) {
	if l.path != "" {
		return l.path, nil
	}
	dir, err := native.DataDir()
	if err != nil {
		return "", err
	}
	l.path = filepath.Join(dir, fmt.Sprintf("app.%d.log", os.Getpid()))
	return l.path, nil
}

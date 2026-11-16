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
}

var appLogger = &appLog{}

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

// appLogSetBounds 记一次 setBounds 到达。waitMs=拿到 embedMu 前等了多久
// （>1000ms 的排队会原样留在行里——那是「到了 Go 但锁被别人占」的直接证据）。
func appLogSetBounds(x, y, w, h int, show bool, waitMs int64) {
	appLogger.mu.Lock()
	defer appLogger.mu.Unlock()
	appLogger.sent++
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

func (l *appLog) writeLine(s string) {
	path, err := l.pathLocked()
	if err != nil {
		l.lastErr = err.Error()
		return // 诊断失败永不阻塞产品路径
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		l.lastErr = err.Error()
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format("2006-01-02T15:04:05.000"), s)
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

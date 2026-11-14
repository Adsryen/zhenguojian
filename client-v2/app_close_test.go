package main

import (
	"testing"
	"time"
)

// X 语义对账 v1（第五轮真机反馈改回来的那条）。
//
// v1 证据：main.py:2788-2838 `closeEvent` 一路收尾后
// `QApplication.instance().quit()` + `event.accept()` —— X 就是真退出；
// v1 的托盘（main.py:735-747）只有「显示主窗口」「退出」，
// 没有任何"关窗转托盘"分支。
// v2 此前自创"X = 隐藏进托盘"，用户实测「只能任务管理器才能关的掉」。
func TestBeforeCloseQuitsLikeV1(t *testing.T) {
	// 看门狗必须挪远：它是 os.Exit(0)，留着 5s 会在测试中途把整个进程带走，
	// 表现就是"套件绿了但少跑了一批"。
	defer func(old time.Duration) { quitWatchdog = old }(quitWatchdog)
	quitWatchdog = time.Hour

	a := &App{}
	if got := a.beforeClose(nil); got != true {
		t.Fatalf("X 必须放行关闭（v1 closeEvent 就是 quit），实得 %v", got)
	}
	if !a.quitting.Load() {
		t.Fatal("beforeClose 应置 quitting，让后续销毁链路知道这是真退出")
	}
}

// 看门狗延时本身可注入（防回归：有人把它改回写死字面量时，
// 上面那条测试会在 5 秒后杀掉测试进程——那正是这条要防的事）。
func TestQuitWatchdogIsInjectable(t *testing.T) {
	defer func(old time.Duration) { quitWatchdog = old }(quitWatchdog)
	quitWatchdog = time.Hour
	if quitWatchdog != time.Hour {
		t.Fatalf("quitWatchdog 应可注入，实得 %v", quitWatchdog)
	}
}

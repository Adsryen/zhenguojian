//go:build zgj_sidecar

// sidecar_main.go —— stdio 协议入口（与 main.go 的 Wails 入口互为对偶）。
//
// 构建：go build -tags "zgj_sidecar v2overlay" -o sidecar .
// 协议（v1 zhenguojian/sidecar/main.go 的直系后继，同一思想、v2 的方法面）：
//
//	上行（Electron 主进程 → sidecar，stdin，一行一个 JSON）：
//	  {"id":"<任意>","method":"Ping","args":[]}
//	下行（sidecar → Electron，stdout，一行一个 JSON）：
//	  {"id":"<同上>","result":"<string>"}            成功（绑定返回的原始字符串）
//	  {"id":"<同上>","error":"<message [code]>"}     失败（Go 错误原文，code 尾约定不变）
//	  {"event":"player:tick","data":{...}}          事件下行（无 id，前端按名字路由）
//
// 为什么方法表用反射构建而不是手写清单：本仓 4 次实锤「手写 -run/方法白名单
// 会静默漏项」（CI gate 的 unique-executed==declared 就是为那族教训立的）。
// Wails 的 Bind:[]interface{}{app} 本来就是「暴露 App 上所有导出的兼容方法」，
// 这里照同一语义用反射枚举——新增绑定方法自动进表，漏项在类型层面不可能。
// 兼容签名 = 导出方法且返回 (string, error)（与 Wails 绑定实际暴露的集合一致，
// 测试 sidecar_protocol_test.go 对账）。
//
// stdout 纪律：协议独占 stdout。sidecar 进程内任何顺带打印（core 库的
// 「访客会话未能保存」、startup 的 initialize 失败行、shutdown 日志）都走
// stderr——runStdioSidecar 开头把 os.Stdout 变量换成 os.Stderr（协议写手持有
// 原始 fd），且读端对非 JSON 行保持容忍（v1 Python 客户端同款容错）。
// 子进程 stdio 已逐一核过：mpv=io.Discard+stderr 捕获（proc.go:114-117）、
// ffmpeg=pipe（executor.go:108）、taskkill/explorer 无害。
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"

	"zhenguojian/client-v2/internal/native"
)

// protoMethod 一个绑定方法的反射调用器。
//
// kind 对应返回形状（第九十八轮反射实证：App 导出方法恰好落在三种）：
//
//	0 = (string, error)   81 个（绝大多数绑定）
//	1 = (string,)          4 个：GetBuildInfo/Ping/TrayStatus/UpdateStartup
//	2 = (error,)           3 个：ClipboardWrite/OpenDownloadPath/PlayerHitBands
//
// 只收这三种：0 返回或别的形状（shutdownSteps/beforeClose 等内部方法）不是
// 前端绑定，自动排除。
type protoMethod struct {
	name       string
	fn         reflect.Value
	kind       int
	paramTypes []reflect.Type
}

func isExportedName(name string) bool {
	r := rune(name[0])
	return r >= 'A' && r <= 'Z'
}

// buildDispatchTable 反射枚举 App 上所有「导出 + 绑定兼容返回形状」方法。
// 与 Wails Bind 的暴露语义一致；private 方法（getDownloadSvc 等）与特殊签名
// （shutdownSteps/beforeClose 等内部方法）自动排除。
func buildDispatchTable() map[string]protoMethod {
	appType := reflect.TypeOf(&App{})
	stringType := reflect.TypeOf("")
	errorType := reflect.TypeOf((*error)(nil)).Elem()
	table := make(map[string]protoMethod)
	for i := 0; i < appType.NumMethod(); i++ {
		m := appType.Method(i)
		if !isExportedName(m.Name) {
			continue
		}
		t := m.Type
		var kind int
		switch {
		case t.NumOut() == 2 && t.Out(0) == stringType && t.Out(1) == errorType:
			kind = 0
		case t.NumOut() == 1 && t.Out(0) == stringType:
			kind = 1
		case t.NumOut() == 1 && t.Out(0) == errorType:
			kind = 2
		default:
			continue // 非绑定形状，排除
		}
		n := t.NumIn() - 1 // 去掉 receiver（Type.Method 的 Func 含 receiver）
		params := make([]reflect.Type, n)
		for j := 0; j < n; j++ {
			params[j] = t.In(j + 1)
		}
		table[m.Name] = protoMethod{name: m.Name, fn: m.Func, kind: kind, paramTypes: params}
	}
	return table
}

// call 按参数位置把 JSON 值转成方法实参。参数类型面（string/int/float64/bool）
// 全部是 JSON 原生可解码的——若将来出现别的参数类型，这里会给出明确的
// 「参数 N 类型不符」而不是静默错值。
//
// receiver：Type.Method(i).Func 的方法集函数第一个参数是 *App 本身，
// 必须把 appRef 排进 Call 实参第一位（第九十八轮修：首版漏了 receiver，
// 运行时必 panic num args mismatch）。
func (p protoMethod) call(appRef reflect.Value, args []json.RawMessage) (string, error) {
	if len(args) != len(p.paramTypes) {
		return "", fmt.Errorf("%s: 需要 %d 个参数，收到 %d", p.name, len(p.paramTypes), len(args))
	}
	vals := make([]reflect.Value, 1+len(p.paramTypes))
	vals[0] = appRef
	for i, raw := range args {
		v := reflect.New(p.paramTypes[i])
		if err := json.Unmarshal(raw, v.Interface()); err != nil {
			return "", fmt.Errorf("%s: 参数 %d (%s) 类型不符: %v", p.name, i, p.paramTypes[i], err)
		}
		vals[i+1] = v.Elem()
	}
	out := p.fn.Call(vals)
	switch p.kind {
	case 1: // (string,)
		res, _ := out[0].Interface().(string)
		return res, nil
	case 2: // (error,)：nil → result 空串（Wails 生成代码此处 resolve undefined）
		if errVal := out[0].Interface(); errVal != nil {
			if err, ok := errVal.(error); ok {
				return "", err
			}
			return "", fmt.Errorf("%s: 未知错误值 %v", p.name, errVal)
		}
		return "", nil
	default: // (string, error)
		res, _ := out[0].Interface().(string)
		if errVal := out[1].Interface(); errVal != nil {
			if err, ok := errVal.(error); ok {
				return "", err
			}
			return "", fmt.Errorf("%s: 未知错误值 %v", p.name, errVal)
		}
		return res, nil
	}
}

// protoWriter 单写者互斥 + 逐行 flush（事件必须尽快可见，不能攒在缓冲里）。
type protoWriter struct {
	mu sync.Mutex
	w  *bufio.Writer
}

func newProtoWriter(out io.Writer) *protoWriter {
	return &protoWriter{w: bufio.NewWriterSize(out, 1<<16)}
}

func (p *protoWriter) line(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[sidecar] 下行序列化失败: %v\n", err)
		return
	}
	p.mu.Lock()
	p.w.Write(b)
	p.w.WriteByte('\n')
	p.w.Flush()
	p.mu.Unlock()
}

// protoRequest 上行请求。id 可选（无 id=单向，不回执）。
type protoRequest struct {
	ID     *json.RawMessage  `json:"id"`
	Method string            `json:"method"`
	Args   []json.RawMessage `json:"args"`
}

// runStdioSidecar 是协议服务主体，拆成函数是为了测试可注入管道。
// 返回 0=正常收尾（EOF 或 __shutdown）。
func runStdioSidecar(in io.Reader, rawOut io.Writer) int {
	// stdout 纪律（见文件头）：协议独占原始 fd，顺带打印改道 stderr。
	// 测试注入的是 buffer（≠os.Stdout），此时不改道。
	if f, ok := rawOut.(*os.File); ok && f == os.Stdout {
		os.Stdout = os.Stderr
	}
	w := newProtoWriter(rawOut)
	sidecarEventSink = func(name string, data any) {
		w.line(protoEvent{Event: name, Data: data})
	}

	app := NewApp()
	ctx := context.Background()
	app.ctx = ctx
	rev := ""
	if bi := readBuildVCS(); bi != nil {
		rev, _ = bi["revision"].(string)
	}
	appLogStartup(buildVersion, rev)
	// 与 Wails 入口 startup 对齐，但**不启托盘**：sidecar 没有窗，托盘图标
	// 会指向一个不存在的窗口（Windows 上表现为一个点了没反应的图标）。
	// 托盘的两个动作由 Electron 主进程承接（显示主窗=win.show；退出=__shutdown）。
	if err := native.Init(); err != nil {
		fmt.Fprintf(os.Stderr, "[zhenguojian] engine initialize 失败: %v\n", err)
	}
	app.downloadSvc.OnStartup(ctx)
	sidecarQuit = func() {
		go func() {
			app.shutdown(ctx)
			os.Exit(0)
		}()
	}

	table := buildDispatchTable()
	appRef := reflect.ValueOf(app) // call() 需要把 *App 排进 receiver 位

	reader := bufio.NewReaderSize(in, 1<<20)
	group := sync.WaitGroup{}
	// 并发槽：v1 sidecar=6。这里放宽到 32——**慢网络调用不许饿死快本地调用**。
	// 首版=8 时被 selftest 实锤：无网沙箱下 DiscoverView 的 9 个 Cover（海报抓取）
	// 各挂满 30s 超时，把 8 个槽占满，快调用 Ping 排队等槽、错过自己的 30s 预算
	// （「sidecar call Ping timed out after 30000ms」）。主进程侧的超时**不会**
	// 释放这里的槽（native 抓取仍在跑），所以只能把槽数抬到明显高于启动期
	// 并发突发的水平，让本地调用永远有位。生产同样受益：一次 UI 动作不该排在
	// 一堆海报抓取后面。
	slots := make(chan struct{}, 32)
	respond := func(id *json.RawMessage, result string, err error) {
		if id == nil {
			return
		}
		idv, _ := json.Marshal(*id)
		if err != nil {
			w.line(map[string]any{"id": json.RawMessage(idv), "error": err.Error()})
			return
		}
		w.line(map[string]any{"id": json.RawMessage(idv), "result": result})
	}
	for {
		line, readErr := reader.ReadBytes('\n')
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 {
			var req protoRequest
			if json.Unmarshal(trimmed, &req) != nil {
				// 容错（v1 同款）：非协议行跳过。正常路径不会有——写端只有
				// Electron 主进程，且 os.Stdout 已改道，这里是双保险。
				fmt.Fprintf(os.Stderr, "[sidecar] 跳过非协议行: %.120s\n", trimmed)
				if readErr != nil {
					break
				}
				continue
			}
			if req.Method == "__shutdown" {
				respond(req.ID, "ok", nil)
				if readErr != nil {
					break
				}
				// 收尾：等所有在飞请求落定，再走有界 shutdown（同 Wails 退出链）。
				break
			}
			// __methods：返回本表全部方法名（JSON 数组）。Electron 壳的 preload
			// 用它把 window.go.main.App 建成**真·普通对象**（contextBridge 克隆
			// 不了 Proxy，但克隆普通函数对象没问题）——方法面与反射表同源，
			// 与 Wails「App = 恰好这些绑定方法」的形态一致。
			if req.Method == "__methods" {
				names := make([]string, 0, len(table))
				for n := range table {
					names = append(names, n)
				}
				sort.Strings(names)
				b, _ := json.Marshal(names)
				respond(req.ID, string(b), nil)
				if readErr != nil {
					break
				}
				continue
			}
			// __embed_set_wid / __embed_get_wid（P3）：Electron 壳的视频面是一个
			// 主进程自管的 BaseWindow（X11 XID / Win32 HWND 归主进程），sidecar
			// 只持有这个句柄给 mpv --wid（playerSvc.SetWid）。内嵌方法
			// （PlayerAttach/SetBounds/HitBands/Detach/ForceWindow）由**主进程
			// 拦截**——窗口几何的真相在主进程手里，Go 侧不建窗。这两个方法是
			// 主进程↔sidecar 的私有协议面，**不进** __methods 反射表（那是前端
			// 契约面，preload 只该看到真正的绑定）。
			if req.Method == "__embed_set_wid" {
				if len(req.Args) != 1 {
					respond(req.ID, "", fmt.Errorf("__embed_set_wid: 需要 1 个参数（句柄数字）"))
					if readErr != nil {
						break
					}
					continue
				}
				var wid uint64
				if json.Unmarshal(req.Args[0], &wid) != nil {
					respond(req.ID, "", fmt.Errorf("__embed_set_wid: 句柄必须是数字"))
					if readErr != nil {
						break
					}
					continue
				}
				app.playerSvc.SetWid(uintptr(wid))
				appLogEvent("embed-set-wid 0x%x", wid)
				respond(req.ID, "ok", nil)
				if readErr != nil {
					break
				}
				continue
			}
			if req.Method == "__embed_get_wid" {
				b, _ := json.Marshal(uint64(app.playerSvc.EmbedWid()))
				respond(req.ID, string(b), nil)
				if readErr != nil {
					break
				}
				continue
			}
			pm, ok := table[req.Method]
			if !ok {
				respond(req.ID, "", fmt.Errorf("method '%s' not registered", req.Method))
				if readErr != nil {
					break
				}
				continue
			}
			group.Add(1)
			go func(id *json.RawMessage, pm protoMethod, args []json.RawMessage) {
				defer group.Done()
				slots <- struct{}{}
				defer func() { <-slots }()
				res, err := pm.call(appRef, args)
				respond(id, res, err)
			}(req.ID, pm, req.Args)
		}
		if readErr != nil {
			if readErr != io.EOF {
				fmt.Fprintf(os.Stderr, "[sidecar] stdin: %v\n", readErr)
			}
			break
		}
	}
	group.Wait()
	app.shutdown(ctx)
	return 0
}

func main() {
	// 可选位置参数=数据目录（v1 sidecar 同款，argv[1]）。必须在 NewApp 前
	// 落 env：NewApp 里 playerSvc 的 DataDir 在构造时就读。
	if len(os.Args) > 1 {
		if dir := strings.TrimSpace(os.Args[1]); dir != "" {
			os.Setenv("ZHENGUO_DATA", dir)
		}
	}
	os.Exit(runStdioSidecar(os.Stdin, os.Stdout))
}

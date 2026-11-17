//go:build zgj_sidecar

// sidecar_protocol_test.go —— stdio 协议入口的执行级门。
//
// 第九十八轮 P1：sidecar 是 Electron 壳（P2）与 Go 逻辑层之间唯一的桥。
// 三条断言各自钉一个「下次有人能改坏」的形状：
//
//	① buildDispatchTable 反射收满三种返回形状（81 STRE + 4 STR + 3 ERR）。
//	   修前只收 (string,error)，会漏 7 个真绑定 → 前端一调就是
//	   "method not registered"（本仓第 5 次「判据静默漏项」，这次我自己
//	   在反射代码里犯）。
//	② call 把 *App receiver 排进 Call 实参。修前漏了 receiver → 运行时
//	   必 panic "num args mismatch"（编译期发现不了，必须执行级抓）。
//	③ contract.ts 里每个 App.X 都必须在表里（前端会调、sidecar 得接）。
//	   这条把「前端契约 ⊆ sidecar 表」做成机器对账，漏项当场红。
//
// 端到端那条真跑 runStdioSidecar（NewApp+native.Init+shutdown 全链），
// 临时 ZHENGUO_DATA 隔离；native.Init 用 engine_test 同款 ResetInitForTest
// 保证在临时目录上 initialize（core 对重复 initialize 是 no-op）。
package main

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"zhenguojian/client-v2/internal/native"
)

// ---------- ① 反射表收满三种形状 ----------

func TestSidecarDispatchTableShapes(t *testing.T) {
	table := buildDispatchTable()

	// 修前漏掉的 7 个（4 单 string + 3 单 error），逐一在表且形状正确。
	// 用「在表」断言形状，而不是硬编码 len==88——方法集会增删，
	// 钉「该在的必须在 + 形状对」才是不随增长而失效的判据。
	wantKind1 := map[string]bool{"GetBuildInfo": false, "Ping": false, "TrayStatus": false, "UpdateStartup": false}
	wantKind2 := map[string]bool{"ClipboardWrite": false, "OpenDownloadPath": false, "PlayerHitBands": false}
	for name, pm := range table {
		if !isExportedName(name) {
			t.Fatalf("表里出现非导出方法 %q（反射判据漂了）", name)
		}
		if _, ok := wantKind1[name]; ok {
			if pm.kind != 1 {
				t.Fatalf("%s 应是单 string（kind 1），实得 kind=%d", name, pm.kind)
			}
			wantKind1[name] = true
		}
		if _, ok := wantKind2[name]; ok {
			if pm.kind != 2 {
				t.Fatalf("%s 应是单 error（kind 2），实得 kind=%d", name, pm.kind)
			}
			wantKind2[name] = true
		}
	}
	for n, ok := range wantKind1 {
		if !ok {
			t.Fatalf("单 string 方法 %s 不在表（被 (string,error) 判据漏了？）", n)
		}
	}
	for n, ok := range wantKind2 {
		if !ok {
			t.Fatalf("单 error 方法 %s 不在表", n)
		}
	}
	if len(table) < 80 {
		t.Fatalf("表大小=%d，明显偏小（反射判据漏收了一片），want >= 80", len(table))
	}
	t.Logf("sidecar 表 %d 个方法，三种形状齐（STRE/STR/ERR 各验过）", len(table))
}

// ---------- ③ 前端契约 ⊆ sidecar 表 ----------

// TestSidecarTableCoversContract 把「前端 contract.ts 会调的方法名」解析出来，
// 逐一断言在 sidecar 表里。这就是「前端调了、sidecar 没接 → not registered」
// 的机器对账——手写清单会漏，反射表 + 这个断言不会静默漏。
func TestSidecarTableCoversContract(t *testing.T) {
	table := buildDispatchTable()
	rel := filepath.Join("frontend", "src", "services", "contract.ts")
	data, err := os.ReadFile(rel)
	if err != nil {
		t.Fatalf("读 contract.ts 失败（相对 client-v2/ 的 %s）：%v", rel, err)
	}
	re := regexp.MustCompile(`App\.([A-Z][A-Za-z0-9]*)\(`)
	names := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(string(data), -1) {
		names[m[1]] = true
	}
	if len(names) < 80 {
		t.Fatalf("contract.ts 只解析出 %d 个 App.X（正则或文件漂了），want >= 80", len(names))
	}
	var missing []string
	for n := range names {
		if _, ok := table[n]; !ok {
			missing = append(missing, n)
		}
	}
	sort.Strings(missing)
	if len(missing) != 0 {
		t.Fatalf("contract.ts 调用但 sidecar 表没有的 %d 个方法：%v（前端一调就 not registered）", len(missing), missing)
	}
	t.Logf("contract.ts %d 个 App.X 全部在 sidecar 表（契约 ⊆ 表）", len(names))
}

// ---------- 端到端：真跑 runStdioSidecar ----------

type sidecarResp struct {
	ID     json.RawMessage `json:"id"`
	Result string          `json:"result"`
	Error  string          `json:"error"`
}

// TestSidecarProtocolEndToEnd 走真 stdio 管道：发一批方法调用 + 一个未注册 +
// __shutdown，逐一按 id 对回执。覆盖三种返回形状的真实往返、错误帧、收尾。
func TestSidecarProtocolEndToEnd(t *testing.T) {
	t.Setenv("ZHENGUO_DATA", t.TempDir())
	native.ResetInitForTest()
	t.Cleanup(native.ResetInitForTest)

	inR, inW, err := os.Pipe()
	if err != nil {
		t.Fatalf("输入管道: %v", err)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatalf("输出管道: %v", err)
	}
	defer inR.Close()
	defer inW.Close()
	defer outR.Close()
	defer outW.Close()

	type result struct {
		code int
		err  error
	}
	done := make(chan result, 1)
	go func() {
		code := runStdioSidecar(inR, outW)
		done <- result{code, nil}
	}()

	// 逐行发。__shutdown 收尾（侧车回执 ok 后 break→收尾→返回）。
	req := func(id string, method string, args ...json.RawMessage) {
		line, _ := json.Marshal(map[string]any{"id": id, "method": method, "args": args})
		if _, err := inW.Write(append(line, '\n')); err != nil {
			t.Fatalf("写请求 %s: %v", method, err)
		}
	}
	req("1", "Ping")
	req("2", "DataPath")
	req("3", "ClipboardWrite", json.RawMessage(`"sidecar-test"`))
	req("4", "GetBuildInfo")
	req("5", "NoSuchMethodXYZ")
	req("6", "__shutdown")
	inW.Close()

	// 收回执：按 id 匹配，忽略事件帧（无 id）。收齐 6 个即停。
	wantIDs := map[string]bool{"1": false, "2": false, "3": false, "4": false, "5": false, "6": false}
	got := map[string]sidecarResp{}
	deadline := time.Now().Add(60 * time.Second)
	sc := bufio.NewScanner(outR)
	sc.Buffer(make([]byte, 1<<20), 1<<22)
	for len(got) < len(wantIDs) {
		remain := time.Until(deadline)
		if remain <= 0 {
			t.Fatalf("收不齐回执（%d/%d）：已收 %v", len(got), len(wantIDs), keys(got))
		}
		outR.SetReadDeadline(time.Now().Add(remain))
		if !sc.Scan() {
			if err := outR.SetReadDeadline(time.Time{}); err != nil {
				break
			}
			if err := sc.Err(); err != nil {
				t.Fatalf("读输出失败（已收 %v）：%v", keys(got), err)
			}
			break
		}
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var ev map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("下行不是合法 JSON: %.200s", line)
		}
		idRaw, ok := ev["id"]
		if !ok {
			continue // 事件帧（无 id）
		}
		var resp sidecarResp
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Fatalf("回执解析失败: %.200s", line)
		}
		var id string
		if err := json.Unmarshal(idRaw, &id); err != nil {
			t.Fatalf("回执 id 不是字符串: %.200s", line)
		}
		got[id] = resp
		wantIDs[id] = true
	}
	outR.SetReadDeadline(time.Time{})

	// ① Ping（kind 1，单 string）→ result "pong"
	if r, ok := got["1"]; !ok || r.Error != "" || r.Result != "pong" {
		t.Fatalf("Ping 回执=%+v，want result=\"pong\"（单 string 形状没走通/receiver 没排对）", r)
	}
	// ② DataPath（kind 0，(string,error)）→ result = 临时数据目录
	if r, ok := got["2"]; !ok || r.Error != "" || r.Result == "" {
		t.Fatalf("DataPath 回执=%+v，want 非空目录串", r)
	}
	// ③ ClipboardWrite（kind 2，单 error）→ 成功 result 空串、无 error 字段
	if r, ok := got["3"]; !ok || r.Error != "" {
		t.Fatalf("ClipboardWrite 回执=%+v，want 无 error（Linux no-op 返回 nil）", r)
	}
	// ④ GetBuildInfo（kind 1，单 string）→ result 是 JSON 串
	if r, ok := got["4"]; !ok || r.Error != "" || !strings.Contains(r.Result, "{") {
		t.Fatalf("GetBuildInfo 回执=%+v，want JSON 串", r)
	}
	// ⑤ 未注册 → error 帧，带方法名
	if r, ok := got["5"]; !ok || !strings.Contains(r.Error, "NoSuchMethodXYZ") || !strings.Contains(r.Error, "not registered") {
		t.Fatalf("未注册方法回执=%+v，want 点名 not registered", r)
	}
	// ⑥ __shutdown → result "ok"
	if r, ok := got["6"]; !ok || r.Error != "" || r.Result != "ok" {
		t.Fatalf("__shutdown 回执=%+v，want result=\"ok\"", r)
	}

	// 进程收尾：返回 0，且不再拖住（shutdown 有界）。
	select {
	case res := <-done:
		if res.code != 0 {
			t.Fatalf("runStdioSidecar 返回码=%d, want 0", res.code)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("sidecar 收尾没在有界内返回（shutdown 无界了）")
	}
	t.Logf("端到端 %d 条回执全对（含三形状 + 错误帧 + 收尾）", len(got))
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

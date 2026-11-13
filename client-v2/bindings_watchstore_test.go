package main

// bindings_watchstore_test.go —— App 层（Wails 绑定）的行为测试。
//
// 为什么专门补这一层：`evidence_audit.py` 报出 63 个绑定"没有任何 Go 测试提到"，
// 其中会写数据的 5 个最危险 —— 内层 Store/Service 都有单测，
// 但**绑定层**（JSON 解码、空值、参数换算、返回形状）只有编译级证据。
// 绑定层出错时前端的表现是"点了没反应"或"静默写坏数据"，
// 而 mock 面（VITE_MOCK）根本不走桥，Playwright 全绿也照不出来。
//
// 这里能真跑起来靠两点：`native.DataDir()` 认 `ZHENGUO_DATA`，
// 而 `getWatchStore()` 每次 `watchstore.Open(dir)` 重开一个实例
// （没有全局注册表），所以每个用例给一个 TempDir 就是天然隔离；
// 跨调用的持久化走文件，正好也是我想验的那件事。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newAppIn(t *testing.T) *App {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("ZHENGUO_DATA", dir)
	t.Cleanup(func() { _ = os.Unsetenv("ZHENGUO_DATA") })
	return &App{}
}

func decodeMap(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("解 JSON 失败 %q: %v", s, err)
	}
	return m
}

func num(t *testing.T, m map[string]any, k string) float64 {
	t.Helper()
	v, ok := m[k]
	if !ok {
		t.Fatalf("缺字段 %q：%+v", k, m)
	}
	f, ok := v.(float64)
	if !ok {
		t.Fatalf("%q 不是数：%T %+v", k, v, m)
	}
	return f
}

// ---------------------------------------------------------------- 看过标记

func TestAppToggleSeenRoundTrip(t *testing.T) {
	app := newAppIn(t)

	res := decodeMap(t, ok(app.ToggleSeen("hongguo:1")))
	if res["on"] != true {
		t.Fatalf("第一次翻转应 on=true，得 %+v", res)
	}
	marks := decodeMap(t, ok(app.SeenList()))
	if _, had := marks["hongguo:1"]; !had {
		t.Fatalf("SeenList 应含该 id：%+v", marks)
	}
	// 落盘是"值=epoch 秒"的扁平映射（v2-only 兄弟文件，见 store.go 那段设计注释）
	seenFile := filepath.Join(os.Getenv("ZHENGUO_DATA"), "seen-library.json")
	body, err := os.ReadFile(seenFile)
	if err != nil {
		t.Fatalf("标记没落盘：%v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatalf("seen-library.json 解析失败：%v / %s", err, body)
	}
	if _, had := raw["hongguo:1"]; !had {
		t.Fatalf("文件里没有该 id：%s", body)
	}

	res = decodeMap(t, ok(app.ToggleSeen("hongguo:1")))
	if res["on"] != false {
		t.Fatalf("第二次翻转应 on=false，得 %+v", res)
	}
	// 再开一次 SeenList 应当读回空 —— 但注意：`Open()` 每次重开实例读文件，
	// 这一步同时验到"绑定层没有把状态只留在内存里"。
	marks = decodeMap(t, ok(app.SeenList()))
	if len(marks) != 0 {
		t.Fatalf("取消后应无标记，得 %+v", marks)
	}
}

func TestAppToggleSeenRejectsEmptyID(t *testing.T) {
	// 这条专测错误分支，所以不取 ok（取了不用是编译错误）
	app := newAppIn(t)
	if _, err := app.ToggleSeen(""); err == nil || !strings.Contains(err.Error(), "缺少剧目 id") {
		t.Fatalf("空 id 该报「缺少剧目 id」，得 %v", err)
	}
	// 报错路径不能留下半个文件
	if _, err := os.Stat(filepath.Join(os.Getenv("ZHENGUO_DATA"), "seen-library.json")); err == nil {
		t.Fatal("校验失败却写了文件")
	}
}

// 绑定名是 ImportSeen，Store 侧方法叫 ImportSeenJSON —— 别按内层名字去调绑定。
func TestAppImportSeenNormalizesMillisAndKeepsExisting(t *testing.T) {
	app, ok := newAppIn(t), okOf(t)

	// 毫秒载荷（前端 localStorage 存的是 epoch ms）要归一成秒
	out := decodeMap(t, ok(app.ImportSeen(`{"hongguo:7":1763000000000}`)))
	if num(t, out, "added") != 1 {
		t.Fatalf("首次导入应新增 1 条，得 %+v", out)
	}
	marks := decodeMap(t, ok(app.SeenList()))
	if got := num(t, marks, "hongguo:7"); got != 1763000000 {
		t.Fatalf("毫秒没归一成秒，得 %v", got)
	}

	// 已有 id 不覆盖（v1 迁移语义：Go 侧真值优先）
	out = decodeMap(t, ok(app.ImportSeen(`{"hongguo:7":1,"hongguo:8":2}`)))
	if num(t, out, "added") != 1 {
		t.Fatalf("只该新增 hongguo:8，得 %+v", out)
	}
	marks = decodeMap(t, ok(app.SeenList()))
	if got := num(t, marks, "hongguo:7"); got != 1763000000 {
		t.Fatalf("已有标记被导入覆盖了：%v", got)
	}

	// 空串是合法的空导入（前端"没有可迁移数据"时也调这里），不该报错
	out = decodeMap(t, ok(app.ImportSeen("   ")))
	if num(t, out, "added") != 0 {
		t.Fatalf("空载荷应 added=0，得 %+v", out)
	}

	// 坏 JSON 必须报错上抛：静默当空导入会把用户的标记悄悄丢掉
	if _, err := app.ImportSeen(`{"hongguo:9"`); err == nil {
		t.Fatal("坏 JSON 该报错")
	}
	// 报错路径不能写坏已有文件
	marks = decodeMap(t, ok(app.SeenList()))
	if len(marks) != 2 {
		t.Fatalf("坏 JSON 之后标记数变了：%+v", marks)
	}
}

// ---------------------------------------------------------------- 下一季入口

func TestAppSetNextSeasonRequiresFollowRow(t *testing.T) {
	app, ok := newAppIn(t), okOf(t)
	next := `{"id":"hongguo:2","title":"某剧 第二季","source":"hongguo"}`

	// 没追过这部剧 → marked=false，不创建行（v1 mark_next_season 同形）
	out := decodeMap(t, ok(app.SetNextSeason("hongguo:1", next)))
	if out["marked"] != false {
		t.Fatalf("无追剧行时不该标记成功：%+v", out)
	}
	list := ok(app.Watchlist())
	if strings.Contains(list, "hongguo:1") {
		t.Fatalf("无追剧行却凭空出现了行：%s", list)
	}

	// 追剧之后才记入口；season 传 0 → 由新剧标题的季号推（v1 `int(season or season_number(title))`）
	ok(app.ToggleFollow(`{"id":"hongguo:1","title":"某剧 第一季","source":"hongguo"}`))
	out = decodeMap(t, ok(app.SetNextSeason("hongguo:1", next)))
	if out["marked"] != true {
		t.Fatalf("有追剧行时应标记成功：%+v", out)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(ok(app.Watchlist())), &rows); err != nil {
		t.Fatalf("Watchlist 解析：%v", err)
	}
	var row map[string]any
	for _, r := range rows {
		if d, _ := r["drama"].(map[string]any); d["id"] == "hongguo:1" {
			row = r
		}
	}
	if row == nil {
		t.Fatalf("Watchlist 里找不到追剧行：%+v", rows)
	}
	if num(t, row, "nextSeason") != 2 {
		t.Errorf("nextSeason 应从标题推出 2，得 %v", row["nextSeason"])
	}
	if row["arrived"] != true || row["reserved"] != false || row["newSeason"] != true {
		t.Errorf("入口记上后的旗标不对：%+v", row)
	}
	if nd, _ := row["nextDrama"].(map[string]any); nd == nil || nd["id"] != "hongguo:2" {
		t.Errorf("nextDrama 没落到行上：%+v", row["nextDrama"])
	}
}

// ---------------------------------------------------------------- 观察器

func TestAppObserveDramaReportsWhetherFollowRowExists(t *testing.T) {
	app, ok := newAppIn(t), okOf(t)
	drama := `{"id":"hongguo:5","title":"某剧","source":"hongguo","episodes":10}`

	// 没追这部剧：v1 observe_drama 直接 return（连 emit 都不发），这里如实报 observed=false
	out := decodeMap(t, ok(app.ObserveDrama(drama)))
	if out["observed"] != false {
		t.Fatalf("未追剧应 observed=false，得 %+v", out)
	}

	ok(app.ToggleFollow(`{"id":"hongguo:5","title":"某剧","source":"hongguo","episodes":3}`))
	out = decodeMap(t, ok(app.ObserveDrama(drama)))
	if out["observed"] != true {
		t.Fatalf("已追剧应 observed=true，得 %+v", out)
	}
	if out["changed"] != true {
		t.Fatalf("集数 3→10 该报 changed=true，得 %+v", out)
	}
	row, _ := out["row"].(map[string]any)
	if row == nil {
		t.Fatalf("该把观察后的行回吐出来：%+v", out)
	}
	if num(t, row, "known") != 10 {
		t.Errorf("known 应跟到 10，得 %v", row["known"])
	}
	// 新集数：episodes(10) > known 时 newEpisodes = episodes - read（read 初值 0）
	if num(t, row, "newEpisodes") != 10 {
		t.Errorf("newEpisodes 应为 10（未读过），得 %v", row["newEpisodes"])
	}

	// 同样的数据再观察一次：不该再有变化（后台每 30 分钟一轮，重复观察必须幂等）
	out = decodeMap(t, ok(app.ObserveDrama(drama)))
	if out["changed"] != false {
		t.Fatalf("重复观察不该再报 changed：%+v", out)
	}

	// 坏 JSON 要上抛给前端，不能当成"没变化"
	if _, err := app.ObserveDrama(`{"id":`); err == nil {
		t.Fatal("坏 dramaJSON 该报错")
	}
}

// okOf 返回一个"只吃 (string, error)"的取值器。
// 为什么不能写成 `ok(app.X())`：Go 允许多返回值直接当实参的**唯一情形**是
// 实参列表恰好就是那一个调用（f(g())），混进别的参数（比如 t）就报
// "multiple-value ... in single-value context"。
// 我上一版就是这么写的，vet 在 Windows job 上直接拦下 ——
// 这条语言规则我记一次就够了：以后写 Go 测试统一用闭包工厂。
func okOf(t *testing.T) func(string, error) string {
	t.Helper()
	return func(s string, err error) string {
		t.Helper()
		if err != nil {
			t.Fatalf("绑定返回错误: %v", err)
		}
		return s
	}
}

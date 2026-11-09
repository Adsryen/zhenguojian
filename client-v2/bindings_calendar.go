package main

// 追剧日历壳层绑定（从 bindings_watchstore.go 迁入并修正 gender 传参类型）。
//
// 迁移原因：日历属独立功能域，与 V2-2 临时寄放在 watchstore 的壳层绑定分离；
// 原实现把 gender 以 JSON number 塞进 payload（{"gender": 0}），而核心
// nativeInput.Gender 是 string（guoapp-src/native/core/app_runtime.go `json:"gender"`），
// json.Unmarshal 严格类型下整请求失败返回「请求格式无效」。此处改传字符串，
// 与 v1 core_client.calendar()（zhenguojian/app/zhenguojian/core_client.py:303，
// payload["gender"] 为字符串）对齐。
//
// 复用说明（遵守约束：不在 internal/native/actions.go 新增任何导出）：
//   - native.Call(action, payload)（client-v2/internal/native/engine.go）即核心
//     门面已有导出：composeInput 补 session/sequence → core.NativeRequest →
//     decodeEnvelope 把 {"ok":true,"data":{…}} 平铺返回；calendar 不在
//     noSessionActions，正常带 session，无需新导出。
//   - 日历页的「追剧 / 预约 / 取消预约」按钮复用 bindings_watchstore.go 已有绑定
//     App.ToggleFollow / App.Reserve / App.CancelReservation（前端 api.ts 直调，
//     本文件不重复包装）。
//   - Wails 通过 main.go `Bind: []interface{}{app}` 整结构体绑定 *App 的导出方法，
//     本文件新增方法无需 app.go / main.go 注册 diff。
//
// 返回契约：核心 case "calendar" → engine.hongguoCalendar（provider_hongguo_calendar.go）
// 的 hongguoCalendarPage 原样平铺 JSON：
//
//	{"items":[{"seriesId","title","cover","description?","category?","tags?",
//	"episodeCnt?","publishAt?","publishText?","hint?","note?","heat?",
//	"expectedText?","isOnline","subscribed","badge?"}, …],
//	 "dates":["YYYYMMDD",…],"defaultDate":"YYYYMMDD","hasMore":bool,
//	 "nextOffset":int,"sessionId":"…","fetchedAt":"RFC3339"}
//
// items 属于「所查询日期」（无逐条 date 字段）；date 为空 = 服务端默认日期（≈今天）。

import (
	"strconv"

	"zhenguojian/client-v2/internal/native"
)

// Calendar 红果上新日历：date=YYYYMMDD（空=今天/服务端默认日），offset 翻页游标，
// gender 2=全部 1=男频 0=女频（2 时省略，核心缺省即 "2"）。
// 透传核心 data（items 平铺）；错误中文原文走 JS reject。
func (a *App) Calendar(date string, offset float64, gender float64) (string, error) {
	payload := map[string]any{"date": date, "offset": int(offset)}
	if g := int(gender); g != 2 {
		payload["gender"] = strconv.Itoa(g) // 核心 nativeInput.Gender 为 string
	}
	data, err := native.Call("calendar", payload)
	if err != nil {
		return "", err
	}
	return marshalJSON(data)
}

package main

// 青空账号（登录态/滑块验证码/登录/登出）绑定 —— 审计缺口「青空登录 GUI」Go 侧。
// 通道=核心 action "soraniAccount"（app_runtime.go:455 →
// downloader.soraniAccount(ctx, command, input.Account)）。
//
// 硬性协议（provider_sorani_account.go:84-116 json tag 实形；v1
// core_client.sorani_account 头注释同款告诫）：
//   - 登录入参必须嵌套 account 键（nativeInput 只读 input.Account，
//     顶层平铺会被静默丢掉、永远报「请输入用户名或邮箱」）；
//   - track=soraniCaptchaTrack{bgImageWidth,bgImageHeight,templateImageWidth,
//     templateImageHeight,startTime,stopTime,trackList:[{x,y,type,t}]}，
//     前端 SliderCaptcha 采集后原样透传（真人轨迹语义见 v1 sorani_login.py）；
//   - 应答 ""→{loggedIn,name}；"captcha"→{id,backgroundImage,templateImage,
//     backgroundWidth,backgroundHeight,templateWidth,templateHeight}
//     （图为 base64 data URL）；"login"→{loggedIn,name}；"logout"→空态。

import (
	"errors"
	"strings"

	"zhenguojian/client-v2/internal/native"
)

// SoraniAccount command ∈ ""|captcha|login|logout；accountJSON 仅 login 需要
// （形状 {identifier,password,captchaId,track:{…}}）。
func (a *App) SoraniAccount(command, accountJSON string) (string, error) {
	switch command {
	case "", "captcha", "login", "logout":
	default:
		return "", errors.New("账号操作无效")
	}
	payload := map[string]any{"command": command}
	if command == "login" {
		if strings.TrimSpace(accountJSON) == "" {
			return "", errors.New("请输入用户名或邮箱")
		}
		account, err := unmarshalObject(accountJSON)
		if err != nil {
			return "", err
		}
		payload["account"] = account
	}
	data, err := native.Call("soraniAccount", payload)
	if err != nil {
		return "", err
	}
	return marshalJSON(data)
}

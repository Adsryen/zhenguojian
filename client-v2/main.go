package main

import (
	"embed"

	"zhenguojian/client-v2/internal/updater"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

// buildVersion 由发布构建经 -ldflags "-X main.buildVersion=vX.Y.Z" 注入；
// 本地开发构建为空串。它是版本判定与「应用版本/打包提交」展示的第一权威——
// 编译进二进制，不会被 exe 同级残留的旧 build-info.json 污染。
var buildVersion string

func main() {
	updater.EmbeddedVersion = buildVersion
	app := NewApp()

	err := wails.Run(&options.App{
		Title:     "真果鉴",
		Width:     1280,
		Height:    800,
		MinWidth:  1024,
		MinHeight: 640,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		// 深色背景对齐 tokens.css --bg #0f1117，避免 WebView2 首帧白闪
		BackgroundColour: &options.RGBA{R: 15, G: 17, B: 23, A: 255},
		OnStartup:        app.startup,
		OnShutdown:       app.shutdown,
		OnBeforeClose:    app.beforeClose,
		Bind: []interface{}{
			app,
		},
	})
	if err != nil {
		println("Error:", err.Error())
	}
}

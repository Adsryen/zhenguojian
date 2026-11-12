package main

import (
	"embed"
	"fmt"
	"os"

	"zhenguojian/client-v2/internal/selftest"
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

	// v1「真果鉴.exe --selftest」的对等物：在**已打包的 exe 内部**跑真实管线，
	// 逐行打印 OK/FAIL/SKIP、写 selftest-report.json，然后按 FAIL 与否决定退出码。
	// 这里刻意不起 GUI、不开 mpv 窗口（v1 原话：屏显播放不进自检，人工验）。
	// 未接通的步骤会显式打 PENDING —— 六步绿不等于与 v1 完全等同，
	// 报告里的 pending 字段就是这句话的机器可读版本。
	if hasArg(os.Args[1:], "--selftest") {
		// 沙箱必须在 Run 之前建：resources 步会 native.Init()，
		// 而 DataDir 只在第一次 initialize 时生效——晚设就把下载/合并写进用户真库了。
		cleanup, err := selftest.Prepare()
		if err != nil {
			fmt.Fprintf(os.Stderr, "[selftest] 沙箱建立失败: %v\n", err)
			os.Exit(2)
		}
		results, code := selftest.Run(os.Stdout, selftest.Steps(), buildVersion)
		// 全绿就清干净；有 FAIL 则**保留沙箱并打印路径**——
		// 自检失败时最没用的就是一个已被销毁的现场（v1 是直接写真实库，谈不上现场）。
		if keep := selftest.ShouldKeepSandbox(results); keep {
			fmt.Fprintf(os.Stdout, "[selftest] 沙箱保留供排查: %s\n", selftest.SandboxDir())
			os.Exit(code)
		}
		cleanup()
		os.Exit(code)
	}

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

// hasArg 只认精确匹配的参数（v1 用 sys.argv 判定，行为一致）。
func hasArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

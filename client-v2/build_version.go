package main

// 跨入口共享的版本注入位。原在 main.go（Wails 入口独占）；第九十八轮
// Electron 迁移把入口拆成两个（main.go = Wails、sidecar_main.go = stdio
// 协议），-ldflags "-X main.buildVersion=…" 注入的变量必须两个入口都看得到，
// 所以挪到无 tag 的共享文件。语义不变：发布构建注入 vX.Y.Z，本地为空串。
var buildVersion string

// hasArg 只认精确匹配的参数（v1 用 sys.argv 判定，行为一致）。
func hasArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

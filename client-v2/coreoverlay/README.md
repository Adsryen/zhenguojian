# coreoverlay —— v2 注入核心包的 overlay 源文件（**存成 `.go.txt`，不是 `.go`**）

为什么不是 `.go`：这两个文件是 `package core` 且引用 `nativeDrama` 等
只有注入 core 包后才存在的符号。放在模块里当 `.go` 看，
`go vet ./...` 会真的去编译这个目录并报
`coreoverlay\discovery.go:18:11: undefined: nativeDrama`（CI 实测踩过）。
v1 那边没暴露，只是因为它的构建只 `go build .`（根包），没下钻子目录。
现在由 `make-overlay.py` 在构建时把 `.go.txt` 落地成临时 `.go`
（写到 overlay.json 同级的 `overlay-src/`），仓库里这个目录**不是一个 Go 包**。

`client-v2/scripts/make-overlay.py` 把本目录的两个文件以 `go build -overlay`
注入 `guoapp-src/native/core` 包（键名 `sidecar_overlay_<name>.go`），
CI 的 linux 与 windows 两个 job 都带这个 overlay 构建。

## 来历与分叉

这两个文件原本住在 **v1 的目录树**里（`zhenguojian/sidecar/core_overlay/`）。
那意味着 v2 的构建输入依赖 v1 还在 —— "把 v1 删掉"会直接打断 CI。
D2 第一步先把它们**复制**过来（复制当时两份 sha256 一致：
`discovery.go 7d81412c9fda…`、`poster_metadata.go f101b208774b…`），
CI 验证通过后才删除 v1 树。

**从那时起两份允许不同。** 需要同步 v1 的修复时，必须显式 diff 并说明理由；
取 v1 原文用不可漂移的 tag：

    git show v1-final:zhenguojian/sidecar/core_overlay/discovery.go

## 为什么用 overlay 而不是直接放进 core

`guoapp-src/native` 是与 v1 共用的核心源码（`go.mod` 里 `replace` 指过去）。
把 v2 专属的站源覆盖逻辑**写进 core 目录**会污染 v1 也用的那份源码；
overlay 让"同一份 core、两套站源实现"在构建期成立，两边都不改源码。

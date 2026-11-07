# 真果鉴 · AI 源码构建说明

这个包是**纯源码**，不含 exe、mpv、ffmpeg 等二进制。按下面的步骤可以在一台干净的
Windows x64 机器上从零编译出 `dist\真果鉴.exe` 和成品 zip。

## 1. 目录结构

```
真果鉴源码/
├─ AI_BUILD.md                  ← 本文件
├─ requirements.txt             Python 依赖
├─ guoapp-src/native/           Go 核心（红果接口、六头签名、取流、下载、解密、合并）
│  ├─ core/                     主要逻辑
│  ├─ core/sign6/               六头签名算法（纯计算）
│  └─ bridge/                   c-shared 入口（sidecar 不用，保留）
└─ zhenguojian/
   ├─ app/                      PyQt5 界面
   │  ├─ run.py                 入口
   │  └─ zhenguojian/*.py       界面/播放/下载/合并/自检
   ├─ sidecar/                  Go sidecar（stdin/stdout JSON 行协议）
   │  ├─ core_overlay/          真果鉴本地核心扩展：批量海报信息、演职员作品及找剧筛选
   │  └─ vendor/                Go 依赖源码及许可证（源码包内附带）
   ├─ build/build.py            编译脚本
   ├─ build/package.py          出成品 zip
   └─ README.md                 功能与使用说明
```

## 2. 环境要求

- Windows 10/11 x64
- **Go 1.24.1+**（`go version` 能查到即可；否则用 `ZHENGUO_GO` 指定 go.exe）
- **Python 3.9 x64**（本项目只在 3.9 上验证过；3.10+ 理论可用但未测）
- 网络：源码包自带 `sidecar/vendor/`（约 31 MB），**Go 依赖可离线编译**；
  如果删掉 vendor，首次编译会走 `GOPROXY=https://goproxy.cn,direct` 拉模块
- 运行期还需要：
  - `mpv.exe`（建议 0.38+），放在 `runtime\mpv\mpv.exe`
  - `ffmpeg.exe` / `ffprobe.exe`，放在 `runtime\ffmpeg\`
  - `vulkan-1.dll`（可选，放在 mpv.exe 旁边；没装 Vulkan 运行库的机器需要）

## 3. 安装 Python 依赖

```bat
python -m pip install -r requirements.txt
```

## 4. 准备运行库（二选一）

方式 A：直接把已有文件放进目录

```
zhenguojian\runtime\mpv\mpv.exe
zhenguojian\runtime\mpv\vulkan-1.dll
zhenguojian\runtime\ffmpeg\ffmpeg.exe
zhenguojian\runtime\ffmpeg\ffprobe.exe
```

方式 B：用环境变量告诉编译脚本去哪拷（脚本会复制到 `runtime\`）：

```bat
set ZHENGUO_MPV=C:\tools\mpv\mpv.exe
set ZHENGUO_FFMPEG=C:\tools\ffmpeg\ffmpeg.exe
set ZHENGUO_FFPROBE=C:\tools\ffmpeg\ffprobe.exe
```

如果 Go 不在 PATH：

```bat
set ZHENGUO_GO=C:\Go\bin\go.exe
```

## 5. 编译

```bat
cd zhenguojian
python build\build.py
```

脚本做三件事：

1. `go build` 编译 `sidecar/main.go`（依赖 `..\..\guoapp-src\native`，go.mod 里用
   `replace duanjuapp/native => ../../guoapp-src/native` 指向本地源码）。源码包自带
   `sidecar/vendor/`，脚本会优先用 `-mod=vendor` 离线编译；删掉 vendor 后自动改用
   `-mod=mod` 走模块代理。产物 →
   `runtime\sidecar.exe`。编译脚本会自动生成 Go overlay：将 `sidecar/core_overlay/`
   的本地扩展与两个核心解析补丁一并编入，不修改 `guoapp-src/native` 或 vendor
   内的原始源码。**不能省略 `core_overlay/`，也不要只在 sidecar 目录直接运行
   普通 `go build` 代替构建脚本。**
2. 把 mpv / ffmpeg / ffprobe 归位到 `runtime\`
3. PyInstaller onefile 打包 → `dist\真果鉴.exe`，并把 `runtime\` 镜像到 `dist\runtime\`

## 6. 验证

```bat
dist\真果鉴.exe --selftest
```

会依次跑 10 步：resources / library / newseries / search / detail / play / preload /
download / decrypt / merge。退出码 0 且最后一行 `SELFTEST_RESULT ... "passed": true` 即通过。
其中 play/download 需要能访问红果接口，离线环境会失败。

只验证 Go 部分：

```bat
cd guoapp-src\native
go test .\core .\core\sign6
```

完整 `go test .\core` 里有会访问线上接口的用例（离线会失败）。只跑纯单元测试：

```bat
go test .\core -run "TestSignHongguo|TestPendingHongguo|TestHongguoVideoIDOf"
go test .\core\sign6
```

## 7. 出成品包

```bat
cd zhenguojian
python build\package.py
```

产物：`真果鉴.zip`（exe + runtime + README + 空下载目录）。打包前建议先跑一遍
`--selftest`。

## 8. 常见问题

**找不到 Go / native 源码**：`build.py` 会直接报错并打印当前的 `GO` 与
`GO_SOURCES`。装 Go 进 PATH，或设置 `ZHENGUO_GO` / `ZHENGUO_GO_SRC`。

**PyInstaller 报文件被占用**：关掉正在运行的 `真果鉴.exe` / `mpv.exe` 再编译；
`build.py` 会先把旧的 `dist\真果鉴.exe` 改名再删。

**运行后提示缺少播放器**：检查 `runtime\mpv\mpv.exe` 和 `runtime\ffmpeg\*.exe`
是否存在；`dist\` 下也要有同样的 `runtime\` 目录（`build.py` 会自动镜像）。

**没有声音/黑屏**：确认 mpv 旁边的 `vulkan-1.dll` 存在；mpv 实际用 D3D11 解码，
这个 DLL 只是让 Windows 能加载它。

**下载只有 360P**：红果取流要求已注册设备 + 正确签名。源码里已经内置
`device_register`、六头签名、Chrome TLS 指纹与批量预取；如果仍然降级，先确认
`api5-normal-sinfonline*.fqnovel.com` 可达，并检查是否被本地代理改写。

**应用数据目录**：`%LOCALAPPDATA%\ZhenguoJian`（日志在 `logs\app.log`，可用
`ZHENGUO_DATA` 改写）。下载目录默认在 exe 同级的 `下载\`。

## 9. 关键实现位置

| 功能 | 文件 |
| --- | --- |
| 六头签名 / Gorgon | `guoapp-src/native/core/sign6/sign6.go` |
| App 请求与缓存 | `guoapp-src/native/core/provider_hongguo_app.go` |
| 取流（App/网页/公开源） | `guoapp-src/native/core/provider_hongguo_native_media.go`、`provider_hongguo.go` |
| 批量预取与画质保护 | `guoapp-src/native/core/provider_hongguo_media_cache.go` |
| Chrome TLS 指纹传输层 | `guoapp-src/native/core/network_hongguo_app.go` |
| 下载队列 | `guoapp-src/native/core/app_downloads.go` |
| 播放会话/本地流代理 | `guoapp-src/native/core/app_stream.go`、`app_playback_routes.go` |
| 解密/合并 | `zhenguojian/app/zhenguojian/library.py`、`media.py` |
| 界面 | `zhenguojian/app/zhenguojian/main.py` 等 |
| 演职员头像与个人作品页 | `zhenguojian/app/zhenguojian/creators.py` |
| 整页详情布局和选集卡片 | `zhenguojian/app/zhenguojian/detail_layout.py`、`detail_episodes.py` |
| 组合筛选、可收起找剧页 | `zhenguojian/app/zhenguojian/browse_panel.py` |
| 本地核心扩展 | `zhenguojian/sidecar/core_overlay/discovery.go`、`poster_metadata.go` |
| 自检 | `zhenguojian/app/zhenguojian/selftest.py` |

## 10. 注意

- 不要提交/分发 `runtime\` 里的 mpv、ffmpeg 二进制到公开仓库（体积与许可证）。
- 取流依赖线上接口，接口行为可能变化；改完签名/请求后先跑
  `dist\真果鉴.exe --selftest`，再验证一集 1080P 下载。

## 源码包校验

`SOURCE_MANIFEST.json` 记录源码包内各文件的 SHA-256；应用源码和本地核心扩展
来自当前工作目录，vendor 由当前 `go.mod` / `go.sum` 重新生成。压缩包不包含
运行库二进制、已下载视频、个人配置、缓存、构建产物或旧版源码备份。

#!/usr/bin/env pwsh
<#
gen_latest_json_electron.ps1 —— Electron 形态的 latest.json 生成器。

为什么不改 Wails 时代的 gen_latest_json.ps1：那个脚本的资产模型是
「exe + app.zip + runtime.zip 三件全 mandatory + PackageDir/BinDir 双目录 +
notes 走 release_notes.py（annotation 优先级硬门）」，Electron 形态的契约是
「exe=NSIS 全量包 + appZip=热更三件套，runtime.zip 不产，notes 简化」——
字段取舍与校验规则都分叉了，塞参数开关会让两个时代互相踩。旧脚本继续服务
旧 workflow，本脚本是新发布面的唯一实现。

对齐的 Go 侧硬规则（改任何一条前先看这些行）：
  - schema=1                    internal/updater/latest.go:29   LatestJSONSchema
  - tag 形态 [vV]?X.Y.Z[-pre.N] internal/updater/semver.go:17   semverPattern
  - version 去 v 前缀与 tag 对账 internal/updater/latest.go:121  sameVersionText
  - prerelease 以 tag 文本判    internal/updater/latest.go:48   IsPrereleaseTag
    （⚠ 旧脚本用 '-(alpha|beta|rc)' 窄口径——v2.0.0-dev.1 会被 Go 判 pre、
     被窄口径判正式版，对账直接报错；本脚本改成与 semverPattern 同源）
  - assets.exe 的 url/size/sha256 三者必须齐 latest.go:156-164（硬校验）
  - url 必须 github.com 形状（镜像代理只吃这一侧，latest.go:3-7 的实测依据；
    形状唯一实现 = ReleaseDownloadURL:36 = base + /download/<tag>/<name>）
  - 资产 name 必须是契约名 zhenGuoJian-v2.exe / zhenGuoJian-v2-app.zip
    （releases.go:56-57）。**隐身错预警**：name 写成别的（比如带 sha 后缀的
    真实产物名）照样过 exe 的 url/size/sha 校验，但 IsV2Release 与
    SelectableAssets 按名过滤后列表为空——Go 不报错、UI 里空转。本脚本
    把 name 钉成契约常量，且 url 只用契约名拼（source 文件叫什么不重要，
    size/sha 从 source 取；release 上传时必须按契约名命名，否则 url 404）。

关于「assets 顺序 appZip 在前」：产出 JSON 里 appZip 键写在 exe 前（人眼阅读
与文本 diff 友好），但要诚实说明——**这影响不到 UI 默认选中**：
ParseLatestJSON 反序列化进 Go struct 后按 exe→appZip 的固定顺序 append 进
Release.Assets（latest.go:165-179），SelectableAssets 保序透传
（releases.go:43-52），所以前端 list[0] 恒为 exe，文件里的键顺序根本不参与。
日常更新真正走 appZip 的机制是 service.go:313-316 的 Download 默认值分支
（IsElectronInstall → DefaultAppZipAsset）；要让对话框默认选中也变 appZip，
得 Go 侧给 Release 加「默认资产」字段或调 append 序——那是主线 internal/updater
的活，不在本脚本射程。-SelfTest 里断言的是「文本键序」这条可兑现的事实。

用法：
  pwsh -NoProfile -File gen_latest_json_electron.ps1 -SelfTest
  pwsh -NoProfile -File gen_latest_json_electron.ps1 -Tag v2.0.0-alpha.42 `
       -NsisPath dist/zhenguojian-v2-win-x64-2.0.0-alpha.42.exe `
       -AppZipPath dist/zhenGuoJian-v2-app.zip -OutDir dist
#>
param(
  [string]$Tag = "",
  # NSIS 安装包（磁盘文件，名字随意，size/sha 从它取；manifest 里按契约名发布）
  [string]$NsisPath = "",
  # app.zip（同上；磁盘名可带 sha 后缀）
  [string]$AppZipPath = "",
  # 产出目录；latest.json 写在这
  [string]$OutDir = ".",
  [string]$OutFile = "",
  # notes：直接给文本（推荐 CI 里传 tag 注释/CHANGELOG 首行）。缺省生成一句带 tag 的占位
  [string]$Notes = "",
  [switch]$SelfTest
)

$ErrorActionPreference = "Stop"
# 控制台/管道编码显式 UTF-8：runner 默认 cp1252，中文 notes 不设会静默写坏
# （alpha.32 release_notes 崩溃 + alpha.37 桩崩，两次都是这条，旧脚本头部实录）。
try {
  [Console]::OutputEncoding = [System.Text.Encoding]::UTF8
  $OutputEncoding = [System.Text.Encoding]::UTF8
} catch { }

# ---- 契约常量（与 Go 侧同名常量一一对应，勿单点改动） ----
$OwnerRepo   = "Adsryen/zhenguojian"        # latest.go:20 RepoReleaseBase 的 owner/repo
$ExeName     = "zhenGuoJian-v2.exe"         # releases.go:56 DefaultExeAsset
$AppZipName  = "zhenGuoJian-v2-app.zip"     # releases.go:57 DefaultAppZipAsset
$BaseRelease = "https://github.com/$OwnerRepo/releases"
# semverPattern（semver.go:17）逐字符照搬：允许大小写 v、可选 -pre(.N)、可选 +build
$SemverPattern = '^[vV]?(\d+)\.(\d+)\.(\d+)(?:-([A-Za-z]+)(?:\.(\d+))?)?(?:\+[0-9A-Za-z.]+)?$'

function Get-Asset {
  param([string]$Path, [string]$Name, [string]$Base)
  if (-not (Test-Path $Path)) { throw "latest.json 缺资产源文件: $Path" }
  $item = Get-Item $Path
  # sha256 必须小写 64 位（Go isSHA256Hex 大小写都收，但旧脚本约定小写，跟齐）
  $sha = (Get-FileHash -Algorithm SHA256 -Path $Path).Hash.ToLower()
  if ($sha -notmatch '^[0-9a-f]{64}$') { throw "sha256 形态不对: $sha" }
  # url 形状唯一实现 = Go ReleaseDownloadURL（latest.go:36-38）：base/download/<tag>/<契约名>。
  # 注意用 $Name（契约名）不是 $Path 的文件名——上传 release 时文件名必须等于契约名。
  return [ordered]@{ name = $Name; size = [int64]$item.Length; sha256 = $sha; url = "$Base/$Name" }
}

function New-Manifest {
  param([string]$Tag, [string]$NsisPath, [string]$AppZipPath, [string]$Notes)
  $m = [regex]::Match($Tag, $SemverPattern)
  if (-not $m.Success) { throw "tag $Tag 不是合法版本号（期望 vX.Y.Z[-pre.N]，同 Go semverPattern）" }
  # tag 里的 v 原样保留（GitHub release tag 就叫 v2.0.0-alpha.N；url 直接拼它）
  $base = "$BaseRelease/download/$Tag"
  # prerelease = semverPattern 的 pre 组是否命中（IsPrereleaseTag 同源口径，见头部注释）
  $pre = [bool]($m.Groups[4].Value -ne '')
  $ver = $Tag -replace '^[vV]', ''
  if (-not $Notes) { $Notes = "真果鉴 V2 $Tag —— 更新详情见该 release 页面。" }
  return [ordered]@{
    schema      = 1
    generatedAt = ([DateTime]::UtcNow).ToString("yyyy-MM-ddTHH:mm:ssZ")
    tag         = $Tag
    version     = $ver
    prerelease  = $pre
    notes       = $Notes
    assets      = [ordered]@{
      # appZip 键在前：人眼/diff 友好。对 Go 解析/UI 默认无作用（头部注释实录）。
      appZip = Get-Asset $AppZipPath $AppZipName $base
      exe    = Get-Asset $NsisPath    $ExeName    $base
    }
    # runtimeZip / buildInfo 键刻意省略：Electron 形态不产 runtime.zip（lead 拍
    # 2026-11），缺键在 Go 侧 = 零值资产直接跳过（latest.go:166-169），合法。
  }
}

# 生成后硬对账：把 manifest 按 ParseLatestJSON 的四条硬规则过一遍再落盘，
# CI 在这里红 = 免得产出一份 Go 端「契约不合格」的 latest.json 让用户侧报错。
function Invoke-ManifestGate {
  param([object]$Back)
  if ($Back.schema -ne 1) { throw "gate: schema 应为 1" }
  # 规则一：url 必须 github.com 形状（且就是 ReleaseDownloadURL 拼出来的那个）
  # ⚠ 一切以 $Back 为输入：早前版本引用了外层 $Tag，SelfTest 传假 tag 时
  #   gate 拿空串比对，绿灯是假的（夹具必须自证，旧脚本 case 9 同款教训）。
  foreach ($k in @('exe','appZip')) {
    $u = $Back.assets.$k.url
    $want = "$BaseRelease/download/$($Back.tag)/$($Back.assets.$k.name)"
    if ($u -notmatch '^https://github\.com/') { throw "gate: assets.$k.url 不是 github.com 形状: $u" }
    if ($u -ne $want) { throw "gate: assets.$k.url 形状错: $u（期望 $want，github.com 形状/ReleaseDownloadURL 同源）" }
    if ($Back.assets.$k.sha256 -notmatch '^[0-9a-fA-F]{64}$') { throw "gate: assets.$k.sha256 非 64hex" }
    if ($Back.assets.$k.size -le 0) { throw "gate: assets.$k.size 非正数" }
  }
  # 规则二：version 去 v 前缀必须与 tag 对账（sameVersionText：TrimPrefix v + EqualFold）
  if (($Back.version -replace '^[vV]?', '') -ne ($Back.tag -replace '^[vV]?', '')) {
    throw "gate: version $($Back.version) 与 tag $($Back.tag) 去 v 前缀后不一致"
  }
  # 规则三：prerelease 布尔与 tag 文本判定一致
  $wantPre = [bool]([regex]::Match($Back.tag, $SemverPattern).Groups[4].Value -ne '')
  if ($Back.prerelease -ne $wantPre) { throw "gate: prerelease=$($Back.prerelease) 与 tag 文本判定=$wantPre 冲突" }
  # 规则四：name 必须契约名（IsV2Release/SelectableAssets 按名过滤，错名=隐身空列表）
  if ($Back.assets.exe.name -ne $ExeName) { throw "gate: exe.name 非契约名 $($Back.assets.exe.name)" }
  if ($Back.assets.appZip.name -ne $AppZipName) { throw "gate: appZip.name 非契约名" }
}

function Invoke-SelfTest {
  $script:pass = 0; $script:failn = 0
  function ok([string]$name) { $script:pass++; Write-Host "PASS $name" }
  function bad([string]$name, [string]$detail) { $script:failn++; Write-Host "FAIL $name | $detail" }
  function expect-throw([string]$name, [scriptblock]$body, [string]$needle) {
    try { & $body | Out-Null } catch {
      $msg = "$_"
      if ($msg -like "*$needle*") { ok "$name（按预期拒绝：$($needle)）" } else { bad $name "报错内容不对：$msg" }
      return
    }
    bad $name "根本没报错"
  }

  $tmp = Join-Path ([IO.Path]::GetTempPath()) ("gle-" + [Guid]::NewGuid().ToString("N").Substring(0, 8))
  New-Item -ItemType Directory -Force $tmp | Out-Null
  # 假资产：名字故意带版本/sha 后缀（模拟 CI 真实产物名），验证 manifest 用契约名
  $nsis = Join-Path $tmp "zhenguojian-v2-win-x64-2.0.0-alpha.99.exe"
  $appz = Join-Path $tmp "zhenGuoJian-v2-app-deadbeef.zip"
  Set-Content -Encoding utf8NoBOM -Path $nsis -Value "NSIS-FAKE"
  Set-Content -Encoding utf8NoBOM -Path $appz -Value "APP-FAKE"

  # 1) happy path：字段齐全 + version 去 v 前缀一致 + tag 原样
  $m = New-Manifest -Tag "v2.0.0-alpha.99" -NsisPath $nsis -AppZipPath $appz -Notes ""
  $json = $m | ConvertTo-Json -Depth 5
  $back = $json | ConvertFrom-Json
  if ($back.schema -eq 1 -and $back.tag -eq "v2.0.0-alpha.99" -and $back.version -eq "2.0.0-alpha.99" -and $back.notes) {
    ok "1 schema/tag/version/notes 齐全，version 去 v 前缀"
  } else { bad "1 字段" ($m | ConvertTo-Json -Depth 5) }

  # 2) url github.com 形状（ParseLatestJSON 硬规则①）：与 ReleaseDownloadURL 逐字一致
  if ($back.assets.exe.url -eq "https://github.com/$OwnerRepo/releases/download/v2.0.0-alpha.99/zhenGuoJian-v2.exe" -and
      $back.assets.appZip.url -eq "https://github.com/$OwnerRepo/releases/download/v2.0.0-alpha.99/zhenGuoJian-v2-app.zip") {
    ok "2 url 为 github.com 形状且用契约名（源文件带 sha 后缀不渗漏）"
  } else { bad "2 url" ($back.assets | ConvertTo-Json -Depth 4) }

  # 3) sha256：64 位小写 hex，且与源文件实算一致（不只是形态对）
  $wantSha = (Get-FileHash -Algorithm SHA256 -Path $appz).Hash.ToLower()
  if ($back.assets.appZip.sha256 -eq $wantSha -and $back.assets.exe.sha256 -match '^[0-9a-f]{64}$') {
    ok "3 sha256 实算一致 + 小写 64hex"
  } else { bad "3 sha" "appZip=$($back.assets.appZip.sha256) want=$wantSha" }

  # 4) size 从真实源文件取
  if ($back.assets.exe.size -eq (Get-Item $nsis).Length) { ok "4 size=源文件字节数" } else { bad "4 size" $back.assets.exe.size }

  # 5) prerelease 口径 = Go IsPrereleaseTag（含旧窄口径会错的 -dev 用例）
  $cases = @(
    @{ t = "v2.0.0";            want = $false },
    @{ t = "v2.0.0-alpha.7";    want = $true },
    @{ t = "v2.0.0-rc.1";       want = $true },
    @{ t = "v2.1.0-dev.3";      want = $true }   # 旧脚本 '-(alpha|beta|rc)' 在此判假→对账爆
  )
  foreach ($c in $cases) {
    $mm = New-Manifest -Tag $c.t -NsisPath $nsis -AppZipPath $appz -Notes ""
    if ($mm.prerelease -eq $c.want) { ok "5 prerelease $($c.t) => $($c.want)" } else { bad "5 prerelease" "$($c.t) 得到 $($mm.prerelease)" }
  }

  # 6) 非法 tag 拒绝（形态错，Go ParseVersion 必报错，别产半成品）
  foreach ($badTag in @("2.0.0alpha.9", "v2.0", "va.b.c", "v2.0.0_alpha.1")) {
    expect-throw "6 非法 tag [$badTag] 被拒绝" {
      New-Manifest -Tag $badTag -NsisPath $nsis -AppZipPath $appz -Notes ""
    } "不是合法版本号"
  }

  # 7) 缺源文件拒绝（旧脚本同款：宁 CI 红，不产 Go 端必报错的文件）
  expect-throw "7 缺 app.zip 被拒绝" {
    New-Manifest -Tag "v2.0.0-alpha.99" -NsisPath $nsis -AppZipPath (Join-Path $tmp "nope.zip") -Notes ""
  } "缺资产源文件"

  # 8) ConvertTo-Json 回读仍是对象（-Depth 不够会把 assets 压成字符串——旧脚本 7 号同款钉）
  if ($back.assets.appZip.name.Length -gt 0 -and $back.assets.exe.size -gt 0) { ok "8 JSON 回读 assets 仍是对象" } else { bad "8 Depth" "assets 被压成字符串" }

  # 9) 文本键序 appZip 在 exe 前（lead 拍板的阅读序；对 Go 解析无效是已知事实，见头部注释）
  if ($json.IndexOf('"appZip"') -lt $json.IndexOf('"exe"')) { ok "9 产出 JSON 里 appZip 键在 exe 前" } else { bad "9 键序" $json }

  # 10) ManifestGate 总闸对 happy path 放行、对 url 被镜像改写的坏样本拦截
  Invoke-ManifestGate -Back $back
  ok "10 gate 放行合法 manifest"
  $badBack = $json.Replace("github.com", "mirror.gh-proxy.com") | ConvertFrom-Json
  expect-throw "10b gate 拒绝非 github.com 形状 url" {
    Invoke-ManifestGate -Back $badBack
  } "github.com 形状"

  Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
  Write-Host ""
  Write-Host "gen_latest_json_electron selftest: $script:pass 通过 / $script:failn 失败"
  return ($script:failn -eq 0)
}

if ($SelfTest) {
  if (Invoke-SelfTest) { exit 0 } else { exit 1 }
}

if (-not $Tag -or -not $NsisPath -or -not $AppZipPath) {
  Write-Error "需要 -Tag / -NsisPath / -AppZipPath（或 -SelfTest）"
  exit 2
}
if (-not $OutFile) { $OutFile = Join-Path $OutDir "latest.json" }
$m = New-Manifest -Tag $Tag -NsisPath $NsisPath -AppZipPath $AppZipPath -Notes $Notes
$json = $m | ConvertTo-Json -Depth 5
$back = $json | ConvertFrom-Json
Invoke-ManifestGate -Back $back
New-Item -ItemType Directory -Force (Split-Path $OutFile) | Out-Null
# UTF8 无 BOM：Go json.Unmarshal 不剥 BOM，带了就「解析失败」级整链红（旧脚本同款落盘姿势）
[IO.File]::WriteAllText($OutFile, $json, (New-Object System.Text.UTF8Encoding($false)))
Write-Host "latest.json 就绪：$OutFile tag=$Tag prerelease=$($m.prerelease) exe=$($m.assets.exe.size)B appZip=$($m.assets.appZip.size)B"

#!/usr/bin/env pwsh
<#
gen_latest_json.ps1 —— latest.json 的唯一实现（CI 只调用这个文件）

为什么要把它从 workflow 里抽出来：
  1. alpha.36 的 windows job 死在这段内联 PowerShell 的一个**语法错**
     （`(& python … 换行 --explain … )` → ParserError: Missing closing ')'）。
     而这类错在本仓库里已经烧过两次：alpha.32 是 release_notes 的 cp1252 崩溃，
     这次是括号换行。**内联在 workflow 里的逻辑没有任何东西能测它**，
     我每次只能靠"下一次 tag 构建"来发现，而每一次都是一轮人工测试的代价。
  2. 本机现在有 pwsh（~/.local/pwsh，7.4.6），这段逻辑**可以**本地跑。
     「本机没有 pwsh 所以没验」这个借口从此不成立。

用法：
  pwsh -NoProfile -File gen_latest_json.ps1 -SelfTest
  pwsh -NoProfile -File gen_latest_json.ps1 -Tag v2.0.0-alpha.37 -BinDir client-v2/build/bin
#>
param(
  [string]$Tag = "",
  [string]$BinDir = "client-v2/build/bin",
  [string]$NotesFile = "",
  [string]$OutFile = "",
  [string]$RepoRoot = ".",
  [string]$NotesScript = "",
  [string]$TempDir = "",
  [switch]$SelfTest
)

$ErrorActionPreference = "Stop"
# 控制台编码必须显式 UTF-8：runner 默认 cp1252，PowerShell 是按 [Console]::OutputEncoding
# 解 python 的 stdout 的，notes 现在是中文 tag 注释，不设就会**静默**写坏进 latest.json。
try {
  [Console]::OutputEncoding = [System.Text.Encoding]::UTF8
  $OutputEncoding = [System.Text.Encoding]::UTF8
} catch { }

if (-not $NotesScript) {
  $NotesScript = Join-Path $RepoRoot "client-v2/scripts/release_notes.py"
}
if (-not $NotesFile) {
  $NotesFile = Join-Path $BinDir "RELEASE-NOTES.md"
}
if (-not $OutFile) {
  $OutFile = Join-Path $BinDir "latest.json"
}
$PythonExe = if ($IsWindows) { "python" } else { "python3" }
$OwnerRepo = "Adsryen/zhenguojian"

function Get-Asset {
  param([string]$Path, [string]$Name, [string]$Base)
  if (-not (Test-Path $Path)) { throw "latest.json 缺资产源文件: $Path" }
  $item = Get-Item $Path
  # sha256 必须小写 64 位：Go 侧按 64 hex 校验，Get-FileHash 默认给大写
  $sha = (Get-FileHash -Algorithm SHA256 -Path $Path).Hash.ToLower()
  if ($sha -notmatch '^[0-9a-f]{64}$') { throw "sha256 形态不对: $sha" }
  return [ordered]@{ name = $Name; size = [int64]$item.Length; sha256 = $sha; url = "$Base/$Name" }
}

function Get-Notes {
  param([string]$Tag, [string]$NotesFile, [string]$NotesScript, [string]$RepoRoot, [string]$TempDir)
  # 判据的唯一实现在 release_notes.py（带 --selftest）。这里只负责调用与取回来源。
  # 来源走**文件**而不是解析 stderr：文件读法几乎不会错，可测的部分全留在 Python。
  $srcFile = Join-Path $TempDir "notes-source.txt"
  if (Test-Path $srcFile) { Remove-Item $srcFile -Force }
  $lines = @(& $PythonExe $NotesScript --tag $Tag --file $NotesFile --cwd $RepoRoot --explain $srcFile)
  if ($LASTEXITCODE -ne 0) { throw "release_notes 取摘要失败 (exit $LASTEXITCODE)" }
  $first = if ($lines.Count -ge 1) { [string]$lines[0] } else { "" }
  $src = ""
  if (Test-Path $srcFile) { $src = (Get-Content -Raw -Encoding UTF8 $srcFile).Trim() }
  return [ordered]@{ notes = $first; source = $src }
}

function New-Manifest {
  param([string]$Tag, [string]$BinDir, [string]$NotesFile, [string]$NotesScript,
         [string]$RepoRoot, [string]$TempDir)
  $ver = $Tag -replace '^v', ''
  # 预发布与否由 tag 文本判（与 Go 侧 IsPrereleaseTag 同一口径）；
  # manifest 里的 prerelease 布尔必须与它一致，否则 Go 侧对账报错。
  $pre = [bool]($Tag -match '-(alpha|beta|rc)')
  $base = "https://github.com/$OwnerRepo/releases/download/$Tag"
  $n = Get-Notes -Tag $Tag -NotesFile $NotesFile -NotesScript $NotesScript -RepoRoot $RepoRoot -TempDir $TempDir
  $m = [ordered]@{
    schema      = 1
    generatedAt = ([DateTime]::UtcNow).ToString("yyyy-MM-ddTHH:mm:ssZ")
    tag         = $Tag
    version     = $ver
    prerelease  = $pre
    notes       = $n.notes
    assets      = [ordered]@{
      exe        = Get-Asset (Join-Path $BinDir "zhenGuoJian-v2.exe") "zhenGuoJian-v2.exe" $base
      appZip     = Get-Asset (Join-Path $BinDir "zhenGuoJian-v2-app.zip") "zhenGuoJian-v2-app.zip" $base
      runtimeZip = Get-Asset (Join-Path $BinDir "zhenGuoJian-v2-runtime.zip") "zhenGuoJian-v2-runtime.zip" $base
      buildInfo  = Get-Asset (Join-Path $BinDir "build-info.json") "build-info.json" $base
    }
  }
  # 硬门：tag 对象存在（annotated）却没走 annotation，说明优先级坏了。
  # 这正是 alpha.33 隐身的那条错：checkout 默认不取 tag 对象，notes 静默退化成
  # 文件回退那一行，而从内容上看不出任何异常。lightweight tag 退回文件是设计内，
  # 所以这里只钉「有注释却没用它」。
  $tagType = ""
  try {
    $o = & git -C $RepoRoot cat-file -t "refs/tags/$Tag" 2>$null
    if ($LASTEXITCODE -eq 0) { $tagType = ($o | Select-Object -First 1) }
  } catch { }
  if (($tagType -eq 'tag') -and ($n.source -notmatch 'annotation')) {
    throw "tag $Tag 是 annotated，但来源=$($n.source) —— annotation 优先级没生效，不带着它发布"
  }
  return $m
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

  $tmp = Join-Path ([IO.Path]::GetTempPath()) ("glj-" + [Guid]::NewGuid().ToString("N").Substring(0, 8))
  $bin = Join-Path $tmp "bin"; New-Item -ItemType Directory -Force $bin | Out-Null
  $repo = Join-Path $tmp "repo"; New-Item -ItemType Directory -Force $repo | Out-Null
  $notesScript = (Resolve-Path (Join-Path $PSScriptRoot "release_notes.py")).Path

  # 假资产
  Set-Content -Encoding utf8NoBOM -Path (Join-Path $bin "zhenGuoJian-v2.exe") -Value "EXE-FAKE"
  Set-Content -Encoding utf8NoBOM -Path (Join-Path $bin "zhenGuoJian-v2-app.zip") -Value "APP-FAKE"
  Set-Content -Encoding utf8NoBOM -Path (Join-Path $bin "zhenGuoJian-v2-runtime.zip") -Value "RT-FAKE"
  Set-Content -Encoding utf8NoBOM -Path (Join-Path $bin "build-info.json") -Value '{"revision":"deadbeef"}'
  $nf = Join-Path $bin "RELEASE-NOTES.md"
  Set-Content -Encoding utf8NoBOM -Path $nf -Value @("# 标题", "", "这是发布说明的第一段正文。")

  # 1) happy path（无 git tag → 文件回退）
  $m = New-Manifest -Tag "v2.0.0-alpha.99" -BinDir $bin -NotesFile $nf -NotesScript $notesScript `
                    -RepoRoot $repo -TempDir $tmp
  if ($m.schema -eq 1 -and $m.tag -eq "v2.0.0-alpha.99" -and $m.version -eq "2.0.0-alpha.99") {
    ok "1 字段：schema/tag/version 一致"
  } else { bad "1 字段" ($m | ConvertTo-Json -Depth 5) }
  if ($m.prerelease -eq $true) { ok "2 prerelease：alpha 判真" } else { bad "2 prerelease" "alpha 判成了假" }
  $m2 = New-Manifest -Tag "v2.0.0" -BinDir $bin -NotesFile $nf -NotesScript $notesScript `
                     -RepoRoot $repo -TempDir $tmp
  if ($m2.prerelease -eq $false) { ok "3 prerelease：正式版判假" } else { bad "3 prerelease" "v2.0.0 判成了真" }
  if ($m.notes -eq "这是发布说明的第一段正文。") { ok "4 notes 取自说明文件正文（不是标题）" }
  else { bad "4 notes" "实际=[$($m.notes)]" }
  $sha = $m.assets.exe.sha256
  if ($sha -match '^[0-9a-f]{64}$') { ok "5 exe.sha256 是 64 位小写十六进制" } else { bad "5 sha" $sha }
  if ($m.assets.exe.size -gt 0 -and $m.assets.exe.url -eq "https://github.com/$OwnerRepo/releases/download/v2.0.0-alpha.99/zhenGuoJian-v2.exe") {
    ok "6 size>0 且 url 由 tag 拼出"
  } else { bad "6 url/size" ($m.assets.exe | ConvertTo-Json) }

  # 7) 生成的 JSON 必须能被回读（-Depth 不够会把 assets 变成字符串）
  $json = $m | ConvertTo-Json -Depth 5
  $back = $json | ConvertFrom-Json
  if ($back.assets.exe.sha256 -and $back.assets.runtimeZip.size -gt 0) { ok "7 ConvertTo-Json 回读仍是对象（Depth 够）" }
  else { bad "7 Depth" "assets 被压成字符串了" }

  # 8) 缺资产必须拒绝，不能生成一个 Go 侧必然报错的文件
  $miss = Join-Path $tmp "bin2"; Copy-Item -Recurse $bin $miss
  Remove-Item (Join-Path $miss "zhenGuoJian-v2-runtime.zip") -Force
  expect-throw "8 缺 runtime.zip 被拒绝" {
    New-Manifest -Tag "v2.0.0-alpha.99" -BinDir $miss -NotesFile $nf -NotesScript $notesScript `
                 -RepoRoot $repo -TempDir $tmp
  } "缺资产源文件"

  # 9) annotated tag 存在时，notes 必须用注释；用不了就该拒绝发布
  $g = Join-Path $tmp "gitrepo"; New-Item -ItemType Directory -Force $g | Out-Null
  & git -C $g init -q 2>&1 | Out-Null
  Set-Content -Encoding utf8NoBOM -Path (Join-Path $g "a.txt") -Value "x"
  & git -C $g add a.txt 2>&1 | Out-Null
  & git -C $g -c user.email=t@t -c user.name=t commit -q -m "c" 2>&1 | Out-Null
  & git -C $g tag -a v9.9.9-alpha.1 -m "v9.9.9-alpha.1 —— 注释首行是摘要" 2>&1 | Out-Null
  $m9 = New-Manifest -Tag "v9.9.9-alpha.1" -BinDir $bin -NotesFile $nf -NotesScript $notesScript `
                     -RepoRoot $g -TempDir $tmp
  if ($m9.notes -eq "v9.9.9-alpha.1 —— 注释首行是摘要") { ok "9 annotated tag 注释优先于文件" }
  else { bad "9 优先级" "实际=[$($m9.notes)]" }

  # 10) lightweight tag 退回文件是设计内（不许误报）
  & git -C $g tag v9.9.9-alpha.2 2>&1 | Out-Null
  $m10 = New-Manifest -Tag "v9.9.9-alpha.2" -BinDir $bin -NotesFile $nf -NotesScript $notesScript `
                      -RepoRoot $g -TempDir $tmp
  if ($m10.notes -eq "这是发布说明的第一段正文。") { ok "10 lightweight tag 退回文件，不被硬门误伤" }
  else { bad "10 lightweight" "实际=[$($m10.notes)]" }

  # 11) annotation 硬门必须真的会响：这就是 alpha.33 那条隐身错——
  #     tag 对象在（annotated），可 notes 却来自文件回退。
  #     本地 release_notes.py 自己不会这么错，所以这里拿一个**桩**冒充 CI 的坏环境。
  $stub = Join-Path $tmp "stub_notes.py"
  Set-Content -Encoding utf8NoBOM -Path $stub -Value @'
import sys
a = sys.argv
notes = "这是桩给出的文件回退行。"
i = a.index("--explain") + 1
open(a[i], "w", encoding="utf-8").write("notes-source=file-fallback\n")
sys.stderr.write("notes-source=file-fallback\n")
print(notes)
'@
  expect-throw "11 annotated tag + 文件回退 = 拒绝发布" {
    New-Manifest -Tag "v9.9.9-alpha.1" -BinDir $bin -NotesFile $nf -NotesScript $stub `
                 -RepoRoot $g -TempDir $tmp
  } "annotation 优先级没生效"

  Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
  Write-Host ""
  Write-Host "gen_latest_json selftest: $script:pass 通过 / $script:failn 失败"
  return ($script:failn -eq 0)
}

if ($SelfTest) {
  if (Invoke-SelfTest) { exit 0 } else { exit 1 }
}

if (-not $Tag) { Write-Error "要么给 -Tag，要么给 -SelfTest"; exit 2 }
# RUNNER_TEMP 只在 CI 里有；本地跑生产路径（我这就在跑）时它是空串，
# Join-Path 空串会直接抛 "Cannot bind argument to parameter 'Path' because it is null"，
# 所以这里兜到系统临时目录。
if (-not $TempDir) { $TempDir = [IO.Path]::GetTempPath() }
$m = New-Manifest -Tag $Tag -BinDir $BinDir -NotesFile $NotesFile -NotesScript $NotesScript `
                  -RepoRoot $RepoRoot -TempDir $TempDir
$json = $m | ConvertTo-Json -Depth 5
$back = $json | ConvertFrom-Json
if ($back.schema -ne 1 -or -not $back.assets.exe.sha256 -or $back.assets.exe.size -le 0 -or $back.tag -ne $Tag) {
  throw "latest.json 自检失败（schema/exe.sha256/size/tag）"
}
New-Item -ItemType Directory -Force (Split-Path $OutFile) | Out-Null
[IO.File]::WriteAllText($OutFile, $json, (New-Object System.Text.UTF8Encoding($false)))
Write-Host "latest.json 就绪：tag=$Tag prerelease=$($m.prerelease) notes来源=$($m.notes.Substring(0, [Math]::Min(28, $m.notes.Length)))"

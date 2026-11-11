// Package updater —— v2 程序内检查/下载/应用更新（GitHub Releases API + 国内加速候选链）。
// 语义参照 reference/guoapp/native/core/app_update.go（服务端 /v1/latest 模式），
// 差异：本包直连 GitHub Releases 列表并本地选版；通道 = stable 默认 / 含 prerelease 可选；
// 版本比较 = semver-lite vX.Y.Z(-pre.N)。平台差异仅收在 apply_* 与文件名判断。
package updater

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// semverPattern 只接受决策约定的形态：vX.Y.Z 可选 -pre(.N)，pre 为字母段
// （alpha/beta/rc 或其它，按字典序），N 可选数字。允许小写 v。
var semverPattern = regexp.MustCompile(`^[vV]?(\d+)\.(\d+)\.(\d+)(?:-([A-Za-z]+)(?:\.(\d+))?)?(?:\+[0-9A-Za-z.]+)?$`)

// Version 是 semver-lite 解析结果；Pre=="" 表示正式发布版。
type Version struct {
	Major  int
	Minor  int
	Patch  int
	Pre    string // 预发布段名（小写归一）；空 = 正式版
	PreNum int    // 预发布数字段
	HasNum bool   // 是否带数字段（alpha < alpha.1，semver 11.4.1）
	Raw    string // 原始输入
}

// ParseVersion 解析 vX.Y.Z(-pre.N)；非法形态返回 error（调用方按「跳过/开发版」处理）。
func ParseVersion(s string) (Version, error) {
	raw := strings.TrimSpace(s)
	m := semverPattern.FindStringSubmatch(raw)
	if m == nil {
		return Version{}, fmt.Errorf("无效版本号：%q（期望 vX.Y.Z[-pre.N]）", raw)
	}
	v := Version{Raw: raw}
	v.Major, _ = strconv.Atoi(m[1])
	v.Minor, _ = strconv.Atoi(m[2])
	v.Patch, _ = strconv.Atoi(m[3])
	if m[4] != "" {
		v.Pre = strings.ToLower(m[4])
		if m[5] != "" {
			v.PreNum, _ = strconv.Atoi(m[5])
			v.HasNum = true
		}
	}
	return v, nil
}

// Compare 按 semver 优先级规则比较；返回 -1/0/1。
// 规则：数值主版本 → 正式版 > 同号预发布版 → pre 名字典序（alpha<beta<rc）→ 数字段。
func (v Version) Compare(o Version) int {
	if c := cmpInt(v.Major, o.Major); c != 0 {
		return c
	}
	if c := cmpInt(v.Minor, o.Minor); c != 0 {
		return c
	}
	if c := cmpInt(v.Patch, o.Patch); c != 0 {
		return c
	}
	switch {
	case v.Pre == "" && o.Pre == "":
		return 0
	case v.Pre == "":
		return 1 // 1.0.0 > 1.0.0-rc.1
	case o.Pre == "":
		return -1
	}
	if v.Pre != o.Pre {
		return strings.Compare(v.Pre, o.Pre)
	}
	switch {
	case v.HasNum == o.HasNum:
		return cmpInt(v.PreNum, o.PreNum)
	case v.HasNum:
		return 1 // alpha.1 > alpha
	default:
		return -1
	}
}

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// CompareVersions 解析并比较两个版本串；任一非法时报错。
func CompareVersions(a, b string) (int, error) {
	va, err := ParseVersion(a)
	if err != nil {
		return 0, err
	}
	vb, err := ParseVersion(b)
	if err != nil {
		return 0, err
	}
	return va.Compare(vb), nil
}

// IsNewer 判断 candidate 是否严格新于 current。
func IsNewer(candidate, current string) (bool, error) {
	c, err := CompareVersions(candidate, current)
	if err != nil {
		return false, err
	}
	return c > 0, nil
}

// IsValidVersion 校验版本串形态（release tag 过滤用）。
func IsValidVersion(s string) bool {
	_, err := ParseVersion(s)
	return err == nil
}

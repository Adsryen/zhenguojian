package updater

import (
	"testing"
)

func TestVersionCompareTable(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"v2.0.0", "v2.0.1", -1},
		{"v2.1.0", "v2.0.9", 1},
		{"v2.0.0-alpha.1", "v2.0.0-alpha.2", -1},
		{"v2.0.0-alpha.1", "v2.0.0", -1},
		{"v2.0.0", "v2.0.0-rc.1", 1},
		{"v2.0.0-alpha", "v2.0.0-alpha.1", -1},   // 无数字段 < 有数字段
		{"v2.0.0-alpha.1", "v2.0.0-alpha", 1},    // 反向
		{"v2.0.0-beta.3", "v2.0.0-rc.1", -1},     // alpha<beta<rc 字典序
		{"v2.0.0-alpha.10", "v2.0.0-alpha.9", 1}, // 数字段按数值
		{"V2.0.0", "v2.0.0", 0},                  // 大写 V 归一
		{"v1.9.9", "v2.0.0-alpha.1", -1},         // 跨主版本，预发布仍大于旧稳定
		{"v2.0.0+build.9", "v2.0.0", 0},          // build 元数据忽略
		{"v2.0.0-alpha.3", "v2.0.0-alpha.3", 0},  // 相同
		{"0.1.0", "0.2.0", -1},                   // 无 v 前缀也可
		{"v0.0.0-dev", "v0.0.0", -1},             // CI dev 版 < 同号正式版
	}
	for _, c := range cases {
		got, err := CompareVersions(c.a, c.b)
		if err != nil {
			t.Fatalf("CompareVersions(%q,%q) err=%v", c.a, c.b, err)
		}
		if got != c.want {
			t.Errorf("CompareVersions(%q,%q)=%d want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestParseVersionErrors(t *testing.T) {
	bad := []string{"", "2.0", "abc", "v1", "v1.2.3.4", "v1.2.x", "v1.2.3-1", "release-1"}
	for _, s := range bad {
		if _, err := ParseVersion(s); err == nil {
			t.Errorf("ParseVersion(%q) should fail", s)
		}
	}
	v, err := ParseVersion("v2.0.0-alpha.3")
	if err != nil {
		t.Fatal(err)
	}
	if v.Major != 2 || v.Pre != "alpha" || v.PreNum != 3 || !v.HasNum {
		t.Errorf("ParseVersion fields wrong: %+v", v)
	}
}

func TestIsNewer(t *testing.T) {
	ok, err := IsNewer("v2.0.0", "v2.0.0-alpha.3")
	if err != nil || !ok {
		t.Errorf("v2.0.0 should be newer than v2.0.0-alpha.3 (ok=%v err=%v)", ok, err)
	}
	ok, err = IsNewer("v2.0.0-alpha.2", "v2.0.0-alpha.3")
	if err != nil || ok {
		t.Errorf("alpha.2 must not be newer than alpha.3 (ok=%v err=%v)", ok, err)
	}
	if _, err := IsNewer("v1.x", "v2.0.0"); err == nil {
		t.Error("invalid candidate should error")
	}
}

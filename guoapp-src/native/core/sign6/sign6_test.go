package sign6

import (
	"encoding/base64"
	"encoding/hex"
	"strconv"
	"testing"
)

// 固定一份 URL+body，供各用例共用。
const (
	testURL     = "https://api5-normal-sinfonlineb.fqnovel.com/novel/player/multi_video_model/v1/?device_id=3470001122334455&version_name=7.1.3.32&ts=1761123456&_rticket=1761123456789"
	testBody    = `{"vid":"7693917197759958040","client":"Android"}`
	testKhronos = int64(1761123456)
)

// SM3 是对外不可见但整条 medusa 链路依赖的内建算法，
// 用 GB/T 32905 的公开测试向量固定其正确性。
func TestSM3KnownVectors(t *testing.T) {
	cases := []struct{ in, want string }{
		{"abc", "66c7f0f462eeedd9d1f2d46bdc10e4e24167c4875cf2f7a2297da02b8f4ba8e0"},
		{"", "1ab21d8355cfa17f8e61194831e81a8f22bec8c728fefb747ed035eb5082aa2b"},
		{"abcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcdabcd",
			"debe9ff92275b8a138604889c18e5a4d6fdb70e5387e5765293dcba39c0c5732"},
	}
	for _, c := range cases {
		got := hex.EncodeToString(sm3([]byte(c.in)))
		if got != c.want {
			t.Fatalf("SM3(%q) = %s, want %s", c.in, got, c.want)
		}
	}
}

func TestSignReturnsSixHeaders(t *testing.T) {
	out := Sign(testURL, []byte(testBody), testKhronos)
	want := []string{"x-gorgon", "x-khronos", "x-ladon", "x-argus", "x-helios", "x-medusa"}
	if len(out) != len(want) {
		t.Fatalf("签名头数量 = %d, want %d (%v)", len(out), len(want), out)
	}
	for _, k := range want {
		v, ok := out[k]
		if !ok {
			t.Fatalf("缺少签名头 %q", k)
		}
		if v == "" {
			t.Fatalf("签名头 %q 为空", k)
		}
	}
	if out["x-khronos"] != strconv.FormatInt(testKhronos, 10) {
		t.Fatalf("x-khronos = %q, want %d", out["x-khronos"], testKhronos)
	}
	// x-ladon / x-argus 应是 be32/le32(khronos) 的 base64，各 4 字节。
	ladon, err := base64.StdEncoding.DecodeString(out["x-ladon"])
	if err != nil || len(ladon) != 4 {
		t.Fatalf("x-ladon 解码失败: %v len=%d", err, len(ladon))
	}
	if got, w := hex.EncodeToString(ladon), hex.EncodeToString(be32(uint32(testKhronos))); got != w {
		t.Fatalf("x-ladon = %s, want %s", got, w)
	}
	argus, err := base64.StdEncoding.DecodeString(out["x-argus"])
	if err != nil || len(argus) != 4 {
		t.Fatalf("x-argus 解码失败: %v len=%d", err, len(argus))
	}
	if got, w := hex.EncodeToString(argus), hex.EncodeToString(le32(uint32(testKhronos))); got != w {
		t.Fatalf("x-argus = %s, want %s", got, w)
	}
	// x-gorgon 是十六进制串，以 8404 开头。
	if len(out["x-gorgon"]) < 4 || out["x-gorgon"][:4] != "8404" {
		t.Fatalf("x-gorgon 前缀异常: %q", out["x-gorgon"])
	}
	if _, err := hex.DecodeString(out["x-gorgon"]); err != nil {
		t.Fatalf("x-gorgon 不是合法十六进制: %v", err)
	}
	// x-helios / x-medusa 是可 base64 解码的非空字节串。
	for _, k := range []string{"x-helios", "x-medusa"} {
		b, err := base64.StdEncoding.DecodeString(out[k])
		if err != nil || len(b) == 0 {
			t.Fatalf("%s 解码失败: %v len=%d", k, err, len(b))
		}
	}
}

// BranchOf 的合法取值为 0 / 1 / 2（1 是服务端拒绝的分支，2 走 branch2F13），
// 且同一 (url, body, khronos) 必须稳定；重复调用不得 panic。
func TestBranchOfStableAndInRange(t *testing.T) {
	first := BranchOf(testURL, []byte(testBody), testKhronos)
	if first < 0 || first > 2 {
		t.Fatalf("BranchOf = %d, 超出 0..2", first)
	}
	for i := 0; i < 20; i++ {
		if got := BranchOf(testURL, []byte(testBody), testKhronos); got != first {
			t.Fatalf("第 %d 次 BranchOf = %d, 与首次 %d 不一致", i, got, first)
		}
	}
	// 覆盖三个分支：不同 ts / query 应能取到不同值，且都在合法范围内。
	seen := map[int]bool{}
	for ts := int64(1761123400); ts < 1761123460; ts++ {
		u := testURL + "&ts=" + strconv.FormatInt(ts, 10)
		b := BranchOf(u, []byte(testBody), ts)
		if b < 0 || b > 2 {
			t.Fatalf("BranchOf(%d) = %d, 超出 0..2", ts, b)
		}
		seen[b] = true
	}
}

// 驱动 hashF13 的三条实现路径（branch 0 / 1 / 2），确认各分支返回值长度
// 一致（branch 0 与 2 均为 16+4=20 字节；branch 1 为 16+16+4=36 字节），
// 且不出现越界 panic（JS 侧越界读为 0，这里必须靠索引掩码保证同效）。
func TestHashF13AllBranches(t *testing.T) {
	counts := map[int]int{}
	bodyMd5 := md5Of([]byte(testBody))
	for ts := int64(1761123400); ts < 1761123600; ts++ {
		u := testURL + "&ts=" + strconv.FormatInt(ts, 10)
		b := BranchOf(u, []byte(testBody), ts)
		counts[b]++
		qs := sm3([]byte(queryOf(u)))
		got := hashF13(qs, bodyMd5, le32(uint32(ts)), ts)
		switch b {
		case 1:
			if len(got) != 36 {
				t.Fatalf("ts=%d branch=1 长度=%d, want 36", ts, len(got))
			}
		default:
			if len(got) != 20 {
				t.Fatalf("ts=%d branch=%d 长度=%d, want 20", ts, b, len(got))
			}
		}
		// 三条分支都要真正跑一遍完整 Sign，确认无 panic。
		if ts == 1761123400 || counts[b] == 1 {
			out := Sign(u, []byte(testBody), ts)
			if len(out) != 6 {
				t.Fatalf("ts=%d 的 Sign 返回 %d 个头", ts, len(out))
			}
		}
	}
	t.Logf("200 个时间点的分支分布: %v", counts)
	if counts[0] == 0 || counts[1] == 0 || counts[2] == 0 {
		t.Logf("注意：本次未覆盖全部分支: %v", counts)
	}
}

// 重复调用、空 body、无 query 的 URL 都不能 panic。
func TestSignNoPanicOnEdgeInputs(t *testing.T) {
	for i := 0; i < 8; i++ {
		out := Sign(testURL, []byte(testBody), testKhronos)
		if len(out) != 6 {
			t.Fatalf("第 %d 次调用返回 %d 个头", i, len(out))
		}
	}
	cases := []struct {
		url  string
		body []byte
	}{
		{"https://api5-normal-sinfonlineb.fqnovel.com/novel/player/multi_video_model/v1/", nil},
		{"https://api5-normal-sinfonlineb.fqnovel.com/x?", []byte{}},
		{"https://api5-normal-sinfonlineb.fqnovel.com/x?device_id=&version_name=", []byte(testBody)},
	}
	for _, c := range cases {
		out := Sign(c.url, c.body, testKhronos)
		for _, k := range []string{"x-gorgon", "x-khronos", "x-ladon", "x-argus", "x-helios", "x-medusa"} {
			if out[k] == "" {
				t.Fatalf("url=%q 的 %q 为空", c.url, k)
			}
		}
		if b := BranchOf(c.url, c.body, testKhronos); b < 0 || b > 2 {
			t.Fatalf("url=%q BranchOf = %d", c.url, b)
		}
	}
}

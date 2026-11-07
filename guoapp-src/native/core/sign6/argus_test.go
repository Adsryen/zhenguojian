package sign6

import (
	"encoding/hex"
	"testing"
)

// 向量来自 4.5.7 运行时 Frida 抓取：SM3 输入 seed‖3ccc567b‖seed，
// 输出 a7b4ce11...；前 4 个轮密钥应为该摘要的 4 个小端 64 位字。
func TestArgusKeySchedule(t *testing.T) {
	keys := argusKeySchedule()
	want := []uint64{
		0x021b679611ceb4a7,
		0x1ed55598d2ea93b7,
		0xffd4bb9a1f1747b0,
		0x5725ea6902a1695e,
		0x43cd84d6e5d0aa88,
		0xed5124e78f6e97db,
	}
	for i, w := range want {
		if keys[i] != w {
			t.Fatalf("key[%d] = %#x, want %#x", i, keys[i], w)
		}
	}
}

func TestArgusSM3(t *testing.T) {
	input := append(append(append([]byte{}, argusSeed...), 0x3c, 0xcc, 0x56, 0x7b), argusSeed...)
	got := hex.EncodeToString(sm3Sum(input))
	want := "a7b4ce1196671b02b793ead29855d51eb047171f9abbd4ff5e69a10269ea2557"
	if got != want {
		t.Fatalf("sm3 = %s, want %s", got, want)
	}
}

// 72 轮链式轮函数对 Frida 抓取的实时状态验证：
// 初始 (x,prev)=(0x42202f5c695d918,0x210048280a4d208)，k[0] 后 x=0x10836d010e3412cf。
func TestArgusRoundChain(t *testing.T) {
	keys := argusKeySchedule()
	x := uint64(0x042202f5c695d918)
	prev := uint64(0x0210048280a4d208)
	k := keys[0]
	next := rol64p(x, 2) ^ prev ^ (rol64p(x, 8) & rol64p(x, 1)) ^ k
	if next != 0x10836d010e3412cf {
		t.Fatalf("round = %#x, want 0x10836d010e3412cf", next)
	}
}

func TestPlayerArgusStructure(t *testing.T) {
	out := PlayerArgus("1234567890123456", 1793619776, "{}", "a=1", make([]byte, 20))
	if len(out) != 304 {
		t.Fatalf("argus len = %d, want 304", len(out))
	}
}

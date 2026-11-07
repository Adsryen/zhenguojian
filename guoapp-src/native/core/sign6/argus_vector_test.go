package sign6

import (
	"encoding/hex"
	"testing"
)

// 回归向量：明文与最终 x-argus 均取自 duanju_core.dll 真实运行内存/请求头。
// 任何改动导致输出不再逐字节一致，都会在这里失败。
func TestArgusDLLVector(t *testing.T) {
	plainHex := "08d2a480820410021883ae96e3042204383636322a1033313139373839323234323739313030320a313631313932313736343a08372e332e352e333242147630342e30372e30312d6d6c2d616e64726f6964488084b84052080000000000000000580060809fc4ae0d6a06d5a0a2df84ba72066d33761898d77a0c08011001180138ccd9cbbc0c820100a201046e6f6e65a801e205ba01210a0a32353035335254343743120231361a097570646174655f3634208080808008c801020404040400b808839a648c9b2d010b04d6790907"
	want := "PMw9YFFxQvnBdpAk38eY06bGWxP+W+M7paJZSEMEa4naQ7DK7cUMk5+VtPB2mZbwDj6kiTs2tMpXPAv5Bt3qRzS5choybuOko6Z4IzCK2awJFGHntB7g5qzAgv8CINH1m0o66/H0N4WORq0HVucHUBbbcFjXZA9mAHxv7U1nxJPXU5CANSGo2hSvz+6Ty6E6TrJNAFnDLVTsWyPJZu0OIxDL/KtfKjA3sH21AIj571RUV7NS/P7Oz0VUQkiSRjwwTmNlm2Aj6/xxJYpyPYJ+tUOdXbK6jnzWxzwoKYpkLJyMSA=="
	plain, err := hex.DecodeString(plainHex)
	if err != nil {
		t.Fatal(err)
	}
	if got := argusWrap(plain); got != want {
		t.Fatalf("argus mismatch\n got %s\nwant %s", got, want)
	}
}

package sign6

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"testing"
)

// TestArgusByteExact 用 DLL 同进程抓到的 208 字节明文，验证我们产出的
// x-argus 与 DLL 逐字节完全一致。
func TestArgusByteExact(t *testing.T) {
	srcHex, err := os.ReadFile(`C:\temp\pair_src.txt`)
	if err != nil {
		t.Skip("no src")
	}
	wantB64, err := os.ReadFile(`C:\temp\pair_argus.txt`)
	if err != nil {
		t.Skip("no argus")
	}
	src, _ := hex.DecodeString(string(bytes.TrimSpace(srcHex)))
	if len(src) < argusPlainLen {
		t.Fatalf("src too short: %d", len(src))
	}
	got := argusWrap(src[:argusPlainLen])
	want := string(bytes.TrimSpace(wantB64))
	if got == want {
		fmt.Println("BYTE-EXACT MATCH")
		return
	}
	fmt.Printf("got  %s\nwant %s\n", got, want)
	t.Fatal("mismatch")
}

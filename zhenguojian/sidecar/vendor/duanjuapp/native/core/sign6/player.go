package sign6

import (
	"bytes"
	"crypto/md5"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
)

// PlayerLadon 生成播放接口的长版 x-ladon（36 字节：4 字节 nonce + 32 字节密文）。
//
// 逆向自短剧之家 4.5.7 duanju_core.dll 的 hongguoPlayerLadon：
//   - MD5(nonce || aid) 的 16 字节摘要转 32 位小写 hex；
//   - 取 hex 的 4 个 8 字节段作为 Speck128/256 的 4 个 64 位密钥字；
//   - 34 轮 Speck，ECB，块内 (y,x) 顺序、小端；
//   - 明文 = 十进制 khronos + "-1611921764-" + aid，PKCS7 填充。
func PlayerLadon(aid string, khronos uint32, nonce []byte) string {
	raw := make([]byte, 0, len(nonce)+len(aid))
	raw = append(raw, nonce...)
	raw = append(raw, aid...)
	sum := md5.Sum(raw)
	hexstr := hex.EncodeToString(sum[:])

	var words [4]uint64
	for i := 0; i < 4; i++ {
		words[i] = binary.LittleEndian.Uint64([]byte(hexstr[i*8 : i*8+8]))
	}
	roundKeys := speckKeySchedule(words[0], []uint64{words[1], words[2], words[3]}, 34)

	plain := []byte(fmt.Sprintf("%d-1611921764-%s", khronos, aid))
	padding := 16 - len(plain)%16
	plain = append(plain, bytes.Repeat([]byte{byte(padding)}, padding)...)

	out := make([]byte, 0, len(nonce)+len(plain))
	out = append(out, nonce...)
	for i := 0; i < len(plain); i += 16 {
		x := binary.LittleEndian.Uint64(plain[i+8 : i+16])
		y := binary.LittleEndian.Uint64(plain[i : i+8])
		ex, ey := speckEncrypt(x, y, roundKeys)
		var block [16]byte
		binary.LittleEndian.PutUint64(block[0:8], ey)
		binary.LittleEndian.PutUint64(block[8:16], ex)
		out = append(out, block[:]...)
	}
	return base64.StdEncoding.EncodeToString(out)
}

func speckKeySchedule(k0 uint64, l []uint64, rounds int) []uint64 {
	keys := make([]uint64, 0, rounds)
	keys = append(keys, k0)
	for i := 0; i < rounds-1; i++ {
		next := (ror64(l[i], 8) + keys[i]) ^ uint64(i)
		l = append(l, next)
		keys = append(keys, rol64(keys[i], 3)^next)
	}
	return keys
}

func speckEncrypt(x, y uint64, keys []uint64) (uint64, uint64) {
	for _, key := range keys {
		x = (ror64(x, 8) + y) ^ key
		y = rol64(y, 3) ^ x
	}
	return x, y
}

func ror64(x uint64, n uint) uint64 { return x>>n | x<<(64-n) }
func rol64(x uint64, n uint) uint64 { return x<<n | x>>(64-n) }

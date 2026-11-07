package sign6

import (
	"bytes"
	"crypto/aes"
	"crypto/md5"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
)

// 播放专用 x-argus 的静态常量，逆向自短剧之家 4.5.7 duanju_core.dll。
var (
	argusSeed = mustHexPlayer("ac1adaae95a7af94a5114ab3b3a97dd80050aa0a39314c40528caec95256c28c")
	argusAESKey = mustHexPlayer("d31e3718288a1027baab59f146a09a9c")
	argusAESIV  = mustHexPlayer("ea180a0336ed352fcd24e4d50018ae54")
	// 内层 cipher 的常量（key expansion 用）。
	argusConst uint64 = 0x3dc94c3a046d678b
	// 外层明文的 9 字节头与 2 字节尾。
	argusHeader = mustHexPlayer("a6e783ee7001100918")
	argusTrailer = mustHexPlayer("567b")
	// protobuf 固定模板（设备无关部分）。
	argusTemplate = mustHexPlayer("08011001180138ccd9cbbc0c820100a201046e6f6e65a801e205ba01210a0a32353035335254343743120231361a097570646174655f3634208080808008c80102")
	// 内层整段异或掩码（8 字节循环）。
	argusXorMask = mustHexPlayer("d04ffdffd04ffdff")
	// 内层明文固定 208 字节（13 个整块）。
	argusPlainLen = 208
)

func mustHexPlayer(s string) []byte {
	b, _ := hex.DecodeString(s)
	return b
}

// sm3Sum 计算标准 SM3 摘要（与 core.hongguoSM3 相同的算法，独立实现避免循环依赖）。
func sm3Sum(input []byte) []byte {
	length := uint64(len(input)) * 8
	data := append(append([]byte(nil), input...), 0x80)
	for len(data)%64 != 56 {
		data = append(data, 0)
	}
	data = binary.BigEndian.AppendUint64(data, length)
	state := [8]uint32{0x7380166f, 0x4914b2b9, 0x172442d7, 0xda8a0600, 0xa96f30bc, 0x163138aa, 0xe38dee4d, 0xb0fb0e4e}
	for offset := 0; offset < len(data); offset += 64 {
		var words [68]uint32
		for i := 0; i < 16; i++ {
			words[i] = binary.BigEndian.Uint32(data[offset+i*4:])
		}
		for i := 16; i < len(words); i++ {
			v := words[i-16] ^ words[i-9] ^ rotl32(words[i-3], 15)
			words[i] = v ^ rotl32(v, 15) ^ rotl32(v, 23) ^ rotl32(words[i-13], 7) ^ words[i-6]
		}
		a, b, c, d, e, f, g, h := state[0], state[1], state[2], state[3], state[4], state[5], state[6], state[7]
		for i := 0; i < 64; i++ {
			t, ff, gg := uint32(0x79cc4519), a^b^c, e^f^g
			if i >= 16 {
				t, ff, gg = 0x7a879d8a, (a&b)|(a&c)|(b&c), (e&f)|(^e&g)
			}
			ss1 := rotl32(rotl32(a, 12)+e+rotl32(t, uint(i%32)), 7)
			tt1 := ff + d + (ss1 ^ rotl32(a, 12)) + (words[i] ^ words[i+4])
			tt2 := gg + h + ss1 + words[i]
			a, b, c, d = tt1, a, rotl32(b, 9), c
			e, f, g, h = tt2 ^ rotl32(tt2, 9) ^ rotl32(tt2, 17), e, rotl32(f, 19), g
		}
		for i, value := range [8]uint32{a, b, c, d, e, f, g, h} {
			state[i] ^= value
		}
	}
	out := make([]byte, 32)
	for i, v := range state {
		binary.BigEndian.PutUint32(out[i*4:], v)
	}
	return out
}

func rotl32(x uint32, n uint) uint32 { return x<<n | x>>(32-n) }

func rol64p(x uint64, n uint) uint64 { return x<<n | x>>(64-n) }
func ror64p(x uint64, n uint) uint64 { return x>>n | x<<(64-n) }

// argusKeySchedule 生成 72 个 64 位轮密钥。
// w[0..3] = SM3(seed ‖ 3ccc567b ‖ seed) 的 4 个小端 64 位字；
// w[i+4] = bit(i) ^ ~w[i] ^ ROR(t,1) ^ t ^ 3，其中 t = ROR(w[i+3],3) ^ w[i+1]，
// bit(i) = (0x3dc94c3a046d678b >> ((i-4) mod 62)) & 1。
func argusKeySchedule() []uint64 {
	digest := sm3Sum(append(append(append([]byte{}, argusSeed...), 0x3c, 0xcc, 0x56, 0x7b), argusSeed...))
	// w[0..3] 占位（对应栈上跳过的 4 个槽），w[4..7] 为摘要的 4 个字，之后扩展到 76。
	w := make([]uint64, 4, 76)
	for i := 0; i < 4; i++ {
		w = append(w, binary.LittleEndian.Uint64(digest[i*8:i*8+8]))
	}
	for i := 8; i < 76; i++ {
		t := ror64p(w[i-1], 3) ^ w[i-3]
		bit := (argusConst >> uint((i-8)%62)) & 1
		w = append(w, bit^ (^w[i-4]) ^ ror64p(t, 1) ^ t ^ 3)
	}
	return w[4:76]
}

// argusEncryptBlock 对 16 字节块跑 72 轮链式轮函数。
// 初始 (x, prev) = (blk[8:16], blk[0:8]) 小端；
// 每轮 new = ROL(x,2) ^ prev ^ (ROL(x,8) & ROL(x,1)) ^ k；prev = x；x = new。
// 输出 = prev(8B) ‖ x(8B)，均小端（实测自 DLL 存储指令）。
func argusEncryptBlock(blk []byte, keys []uint64) []byte {
	x := binary.LittleEndian.Uint64(blk[8:16])
	prev := binary.LittleEndian.Uint64(blk[:8])
	for _, k := range keys {
		next := rol64p(x, 2) ^ prev ^ (rol64p(x, 8) & rol64p(x, 1)) ^ k
		prev = x
		x = next
	}
	out := make([]byte, 16)
	binary.LittleEndian.PutUint64(out[:8], prev)
	binary.LittleEndian.PutUint64(out[8:], x)
	return out
}

func appendVarintPlayer(out []byte, v uint64) []byte {
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			out = append(out, b|0x80)
		} else {
			return append(out, b)
		}
	}
}

// buildArgusPlaintext 构造 208 字节内层明文。
// 布局（全部实测自 DLL 运行内存）：
//
//	field1 varint 0x40401252, field2=2, field3=随机 32 位,
//	field4 "8662", field5 deviceID, field6 "1611921764", field7 "7.3.5.32",
//	field8 "v04.07.01-ml-android", field9 varint 0x80e0200, field10 8 字节零,
//	field11=0, field12=khronos*2,
//	field13=SM3(md5(body))[:6], field14=SM3(query)[:6], field15 固定模板,
//	尾部 04040404 + 随机填充到 208 字节。
func buildArgusPlaintext(deviceID string, khronos uint32, body, query string, rnd []byte) []byte {
	if len(rnd) < 16 {
		extended := make([]byte, 16)
		copy(extended, rnd)
		_, _ = rand.Read(extended[len(rnd):])
		rnd = extended
	}
	p := []byte{}
	p = append(p, 0x08)
	p = appendVarintPlayer(p, 0x40401252)
	p = append(p, 0x10)
	p = appendVarintPlayer(p, 2)
	p = append(p, 0x18)
	p = appendVarintPlayer(p, uint64(binary.LittleEndian.Uint32(rnd[12:16])))
	p = append(p, 0x22, 0x04)
	p = append(p, "8662"...)
	p = append(p, 0x2a, byte(len(deviceID)))
	p = append(p, deviceID...)
	p = append(p, 0x32, 0x0a)
	p = append(p, "1611921764"...)
	p = append(p, 0x3a, 0x08)
	p = append(p, "7.3.5.32"...)
	p = append(p, 0x42, 0x14)
	p = append(p, "v04.07.01-ml-android"...)
	p = append(p, 0x48)
	p = appendVarintPlayer(p, 0x80e0200)
	p = append(p, 0x52, 0x08)
	p = append(p, make([]byte, 8)...)
	p = append(p, 0x58)
	p = appendVarintPlayer(p, 0)
	p = append(p, 0x60)
	p = appendVarintPlayer(p, uint64(khronos)*2)
	bodySum := md5.Sum([]byte(body))
	p = append(p, 0x6a, 0x06)
	p = append(p, sm3Sum(bodySum[:])[:6]...)
	p = append(p, 0x72, 0x06)
	p = append(p, sm3Sum([]byte(query))[:6]...)
	p = append(p, 0x7a, 0x0c)
	p = append(p, argusTemplate...)
	p = append(p, 0x04, 0x04, 0x04, 0x04)
	// 尾部随机填充到 208 字节。
	for len(p) < argusPlainLen {
		p = append(p, rnd[len(p)%len(rnd)])
	}
	return p[:argusPlainLen]
}

// PlayerArgus 生成播放接口的长版 x-argus。
// 完整链路（逐字节比对过 DLL 输出）：
//  1. 208 字节明文按 16 字节块做 72 轮 ARX，块内 (x,prev)=(hi,lo)，输出 prev‖x；
//  2. 整段与 8 字节掩码 d0 4f fd ff d0 4f fd ff 循环异或；
//  3. 前置 8 字节掩码，得到 216 字节后整段反转；
//  4. 取反转后 [16:216] 的 200 字节作为内层；
//  5. 3ccc + AES-CBC(9 字节头 ‖ 内层 ‖ 567b ‖ PKCS7)，base64。
func PlayerArgus(deviceID string, khronos uint32, body, query string, rnd []byte) string {
	return argusWrap(buildArgusPlaintext(deviceID, khronos, body, query, rnd))
}

// argusWrap 对 208 字节内层明文执行完整的 ARX/XOR/反转/AES 封装。
func argusWrap(plain []byte) string {
	keys := argusKeySchedule()

	layer := make([]byte, len(plain))
	for i := 0; i < len(plain); i += 16 {
		copy(layer[i:], argusEncryptBlock(plain[i:i+16], keys))
	}
	for i := range layer {
		layer[i] ^= argusXorMask[i%len(argusXorMask)]
	}

	buf := make([]byte, 0, len(argusXorMask)+len(layer))
	buf = append(buf, argusXorMask...)
	buf = append(buf, layer...)
	for i, j := 0, len(buf)-1; i < j; i, j = i+1, j-1 {
		buf[i], buf[j] = buf[j], buf[i]
	}
	inner := buf[16:]

	// 外层：3ccc + AES-CBC(header ‖ inner ‖ 567b ‖ PKCS7)。
	wrapped := append(append([]byte{}, argusHeader...), inner...)
	wrapped = append(wrapped, argusTrailer...)
	pad := aes.BlockSize - len(wrapped)%aes.BlockSize
	wrapped = append(wrapped, bytes.Repeat([]byte{byte(pad)}, pad)...)
	blockCipher, _ := aes.NewCipher(argusAESKey)
	out := make([]byte, len(wrapped))
	cipher.NewCBCEncrypter(blockCipher, argusAESIV).CryptBlocks(out, wrapped)
	result := append([]byte{0x3c, 0xcc}, out...)
	return base64.StdEncoding.EncodeToString(result)
}


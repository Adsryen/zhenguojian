// Package mpvembed 是 v2 播放器方案的 spike：Windows 下把 mpv 以子窗口形式
// 嵌入 Wails webview 宿主（mpv --wid=<hwnd>），并用命名管道 JSON IPC 控制它。
//
// 平台切分：
//   - ipc.go        跨平台协议层（对 io.ReadWriteCloser 读写，Linux 可用 net.Pipe 单测）
//   - window_windows.go / player_windows.go  仅 windows（//go:build windows），
//     真机验证在 v2 实施期做；spike 达标线 = GOOS=windows go build ./... 通过。
package mpvembed

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// Conn 是 IPC 传输抽象：Windows 上是 \\.\pipe\... 文件，测试里是 net.Pipe。
type Conn interface {
	io.ReadWriteCloser
}

// Response 是 mpv 对带 requestID 命令的应答行。
type Response struct {
	Error     string          `json:"error"`
	RequestID int             `json:"requestID"`
	Data      json.RawMessage `json:"data,omitempty"`
}

// IPC 是 mpv JSON IPC 读写器：单命令单应答模型，跳过中间的异步事件行
// （异步事件无 requestID 字段，mpv 文档约定）。并发发送用互斥串行化。
type IPC struct {
	mu      sync.Mutex
	conn    Conn
	reader  *bufio.Reader
	nextID  int
	pending map[int]chan Response // 预留：v2 做异步回调时用
}

func NewIPC(conn Conn) *IPC {
	return &IPC{conn: conn, reader: bufio.NewReader(conn), pending: map[int]chan Response{}}
}

// SendCommand 写一行 {"command":[...],"requestID":N} 并等待对应应答。
// 例：SendCommand("loadfile", url) / SendCommand("get_property", "pause")。
func (i *IPC) SendCommand(args ...any) (Response, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.nextID++
	id := i.nextID
	req := map[string]any{"command": args, "requestID": id}
	body, err := json.Marshal(req)
	if err != nil {
		return Response{}, err
	}
	if _, err := i.conn.Write(append(body, '\n')); err != nil {
		return Response{}, fmt.Errorf("mpv ipc write: %w", err)
	}
	for {
		line, err := i.reader.ReadBytes('\n')
		if err != nil {
			return Response{}, fmt.Errorf("mpv ipc read: %w", err)
		}
		var resp Response
		if err := json.Unmarshal(line, &resp); err != nil {
			continue // 非 JSON 行（mpv.com 启动噪声）忽略
		}
		if resp.RequestID != id {
			continue // 异步事件或其它请求应答
		}
		if resp.Error != "" && resp.Error != "success" {
			return resp, fmt.Errorf("mpv error: %s", resp.Error)
		}
		return resp, nil
	}
}

// Close 关闭底层连接（管道断开即 mpv 退出监控信号，v2 再细化）。
func (i *IPC) Close() error { return i.conn.Close() }

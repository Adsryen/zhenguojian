package mpv

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// Conn 是一条 mpv IPC 连接。抽象成接口的原因（V2-3 定案）：
//   - Windows 真实现是命名管道（mpv --input-ipc-server 建管道服务器），
//     由 backend_windows.go 用 CreateFileW 打开并包成 os.File；
//   - Linux/CI 单测用 net.Pipe 假 mpv 应答器直接喂读写的两端。
type Conn interface {
	io.ReadWriteCloser
}

// Response 是 mpv JSON IPC 的同步回复行。
// requestID 键名兼容两代协议：旧版（<0.38）回复 "requestID"，
// 新版回复 "request_id"（两者都解析，取非零者配对；真机曾因此错配
// 导致所有命令「超时」——回复行被当作别的 requestID 跳过）。
type Response struct {
	Error      string          `json:"error"`
	RequestID  int             `json:"requestID"`
	RequestID2 int             `json:"request_id"`
	Async      bool            `json:"async"`
	Data       json.RawMessage `json:"data"`
}

// id 返回回复携带的请求 id（兼容两种键名）。
func (r Response) id() int {
	if r.RequestID != 0 {
		return r.RequestID
	}
	return r.RequestID2
}

// IPC 是一条连接上的序列化命令通道（毕业自 spike/mpvembed/ipc.go）。
// mpv JSON IPC 按连接排队处理命令，读侧可能出现异步事件/其它 requestID
// 的行，因此发送侧加锁、接收侧跳过不匹配行直到取到本 requestID 的
// 最终回复（async:true 的 pending 行同样跳过）。
type IPC struct {
	conn   Conn
	reader *bufio.Reader
	mu     sync.Mutex // 序列化命令，保证 requestID 配对唯一
	nextID int
}

func NewIPC(conn Conn) *IPC {
	return &IPC{conn: conn, reader: bufio.NewReader(conn), nextID: 0}
}

// SendCommand 发送 {"command": [...], "requestID": id} 并等待最终回复。
// 返回的 error 是非 nil 表示 mpv 报错（error != "success"）。
func (i *IPC) SendCommand(command []any) (Response, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	i.nextID++
	id := i.nextID
	// 双键名同发：老 mpv 认 requestID、新 mpv 认 request_id，多余键忽略。
	req := map[string]any{"command": command, "requestID": id, "request_id": id}
	b, err := json.Marshal(req)
	if err != nil {
		return Response{}, err
	}
	if _, err := i.conn.Write(append(b, '\n')); err != nil {
		return Response{}, fmt.Errorf("mpv write: %w", err)
	}

	for {
		resp, err := i.readLine()
		if err != nil {
			return Response{}, err
		}
		// 跳过异步事件、非 JSON 噪声、其它 requestID、pending 行
		if resp.id() != id || resp.Async {
			continue
		}
		if resp.Error != "" && resp.Error != "success" {
			return resp, fmt.Errorf("mpv: %s", resp.Error)
		}
		return resp, nil
	}
}

func (i *IPC) readLine() (Response, error) {
	line, err := i.reader.ReadBytes('\n')
	if err != nil {
		return Response{}, err
	}
	var resp Response
	if len(line) == 0 {
		return resp, nil
	}
	if err := json.Unmarshal(line, &resp); err != nil {
		// 非 JSON 行：当作 requestID=0 的噪声跳过（命令 id 从 1 起）
		return Response{}, nil
	}
	return resp, nil
}

// Close 关闭底层连接，唤醒阻塞中的读写。
func (i *IPC) Close() error { return i.conn.Close() }

// rawEvent 是 mpv 事件行的最小解析结构。end-file 的 reason 在事件行顶层
// （不在 data 里），property-change 的值在 data —— 两类字段都要收。
type rawEvent struct {
	Event  string          `json:"event"`
	ID     int             `json:"id"`
	Name   string          `json:"name"`
	Reason string          `json:"reason"`
	Data   json.RawMessage `json:"data"`
}

// parseEventLine 解析事件行；无 "event" 字段（回复/噪声）返回 ok=false。
func parseEventLine(line []byte) (ev rawEvent, ok bool) {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(line, &probe); err != nil {
		return rawEvent{}, false
	}
	if _, has := probe["event"]; !has {
		return rawEvent{}, false
	}
	if err := json.Unmarshal(line, &ev); err != nil {
		return rawEvent{}, false
	}
	return ev, true
}

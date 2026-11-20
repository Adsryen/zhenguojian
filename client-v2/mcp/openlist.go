package main

// openlist 客户端（zgj_publish 的 115 推送通道）。
//
// 形状全部对 224 活实例 + openlist v4.2.9 源码实锤（2026-11-20）：
//   - 登录  POST /api/auth/login {username,password} → data.token（JWT，48h）
//   - 鉴权  Authorization 头 = **裸 token**（无 Bearer 前缀——v4 中间件直接
//     拿整头当缓存 key 比对，带前缀必 401，见 b82 真根因①）
//   - 列表  GET  /api/fs/list?path=<url>
//   - 建目录 POST /api/fs/mkdir {path}（不递归，逐级建）
//   - 上传  PUT  /api/fs/put，头 File-Path（URL-escaped 完整目标路径）+
//     Content-Type + X-File-Size + X-File-Sha1（可选），body=原始字节流。
//     Overwrite 头缺省 true（重发布=覆盖，幂等）。
//   - 删除  POST /api/fs/remove {dir, names[]}（dir=父目录，names=相对名）
//
// 为什么不走 WebDAV：/dav/115/ 在 WebDAV 驱动下写操作全 500/405（驱动不适配
// 115 Open），原生 API 是唯一可靠通道。

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type olConfig struct {
	base string // 如 https://alist.prlrr.com
	user string
	pass string
}

func (c *olConfig) enabled() bool { return c != nil && c.base != "" && c.user != "" && c.pass != "" }

type olFile struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	Sha1 string `json:"sha1"`
	IsDir bool  `json:"is_dir"`
}

type olClient struct {
	cfg  olConfig
	hc   *http.Client
	mu   sync.Mutex
	tok  string
	expr time.Time
}

func newOLClient(cfg olConfig) *olClient {
	return &olClient{cfg: cfg, hc: &http.Client{Timeout: 60 * time.Second}}
}

// ensure 登录（token 过期前 5 分钟预续）。
func (c *olClient) ensure() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.tok != "" && time.Now().Before(c.expr.Add(-5*time.Minute)) {
		return nil
	}
	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Token string `json:"token"`
			ExpireIn float64 `json:"expire_in"`
		} `json:"data"`
	}
	body, _ := json.Marshal(map[string]string{"username": c.cfg.user, "password": c.cfg.pass})
	req, err := http.NewRequest("POST", c.cfg.base+"/api/auth/login", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	r, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("openlist 登录请求失败（%s 不可达）：%w", c.cfg.base, err)
	}
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(&resp); err != nil {
		return fmt.Errorf("openlist 登录响应解析失败（HTTP %d）：%w", r.StatusCode, err)
	}
	if resp.Code != 200 || resp.Data.Token == "" {
		return fmt.Errorf("openlist 登录失败：code=%d %s", resp.Code, resp.Message)
	}
	c.tok = resp.Data.Token
	exp := resp.Data.ExpireIn
	if exp <= 0 {
		exp = 48 * 3600
	}
	c.expr = time.Now().Add(time.Duration(exp) * time.Second)
	return nil
}

func (c *olClient) do(method, path string, body any, out any) error {
	if err := c.ensure(); err != nil {
		return err
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.cfg.base+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", c.tok) // 裸 token（无 Bearer）
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	r, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer r.Body.Close()
	data, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return err
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("openlist %s 响应非 JSON（HTTP %d）：%s", path, r.StatusCode, olTrunc(string(data), 200))
		}
	} else if r.StatusCode >= 400 {
		return fmt.Errorf("openlist %s HTTP %d：%s", path, r.StatusCode, olTrunc(string(data), 200))
	}
	return nil
}

func olTrunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func (c *olClient) list(path string) ([]olFile, error) {
	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Content []olFile `json:"content"`
		} `json:"data"`
	}
	if err := c.do("GET", "/api/fs/list?path="+url.QueryEscape(path), nil, &resp); err != nil {
		return nil, err
	}
	if resp.Code != 200 {
		return nil, fmt.Errorf("openlist 列目录 %s 失败：code=%d %s", path, resp.Code, resp.Message)
	}
	return resp.Data.Content, nil
}

func (c *olClient) mkdir(path string) error {
	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := c.do("POST", "/api/fs/mkdir", map[string]string{"path": path}, &resp); err != nil {
		return err
	}
	// 已存在不视为错误（幂等建目录）。
	if resp.Code != 200 && !strings.Contains(strings.ToLower(resp.Message), "exist") {
		return fmt.Errorf("openlist mkdir %s 失败：code=%d %s", path, resp.Code, resp.Message)
	}
	return nil
}

// mkdirAll 逐级建目录（openlist mkdir 不递归）。
func (c *olClient) mkdirAll(path string) error {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	cur := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		cur += "/" + p
		if err := c.mkdir(cur); err != nil {
			// 已存在的错误在 mkdir 里已吞；到这里是真失败。
			return err
		}
	}
	return nil
}

// put 流式上传（body 直接透传，不落盘——10GB 级合并成品不占本地空间）。
// rc 用完关闭；size 必填（Content-Length）；sha1 可空。
func (c *olClient) put(targetPath string, size int64, fileSha1 string, rc io.ReadCloser) error {
	defer rc.Close()
	if err := c.ensure(); err != nil {
		return err
	}
	req, err := http.NewRequest("PUT", c.cfg.base+"/api/fs/put", rc)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", c.tok)
	req.Header.Set("File-Path", url.PathEscape(targetPath))
	req.ContentLength = size
	if size > 0 {
		req.Header.Set("X-File-Size", fmt.Sprintf("%d", size))
	}
	if fileSha1 != "" {
		req.Header.Set("X-File-Sha1", fileSha1)
	}
	// 上传走长超时客户端（10GB 级文件）。
	slow := &http.Client{Timeout: 4 * time.Hour}
	r, err := slow.Do(req)
	if err != nil {
		return fmt.Errorf("openlist 上传 %s 失败：%w", targetPath, err)
	}
	defer r.Body.Close()
	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&resp); err != nil {
		return fmt.Errorf("openlist 上传 %s 响应非 JSON（HTTP %d）", targetPath, r.StatusCode)
	}
	if resp.Code != 200 {
		return fmt.Errorf("openlist 上传 %s 失败：code=%d %s", targetPath, resp.Code, resp.Message)
	}
	return nil
}

func (c *olClient) remove(dir string, names []string) error {
	if len(names) == 0 {
		return nil
	}
	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if err := c.do("POST", "/api/fs/remove", map[string]any{"dir": dir, "names": names}, &resp); err != nil {
		return err
	}
	if resp.Code != 200 {
		return fmt.Errorf("openlist 删除 %s/%v 失败：code=%d %s", dir, names, resp.Code, resp.Message)
	}
	return nil
}

// ---------------------------------------------------------------- publish 辅助

// olListOne 查单个文件的条目（回读校验用）。
func (c *olClient) statFile(path string) (*olFile, error) {
	dir, name := splitOL(path)
	files, err := c.list(dir)
	if err != nil {
		return nil, err
	}
	for i := range files {
		if files[i].Name == name && !files[i].IsDir {
			return &files[i], nil
		}
	}
	return nil, fmt.Errorf("openlist 上传后未找到 %s（回读校验失败）", name)
}

func splitOL(path string) (dir, name string) {
	path = strings.TrimRight(path, "/")
	i := strings.LastIndex(path, "/")
	if i <= 0 {
		return "/", path
	}
	return path[:i], path[i+1:]
}

// sha1Of 计算本地流 sha1（只用于小文件回传；大文件用 X-File-Sha1 空值让 openlist 自算/跳校验）。
// 注意：publish 主流程不算全文件 sha1（10GB 级两次全读太贵），只信源端 bytes+openlist 回读 size。
func sha1Of(r io.Reader) (string, error) {
	h := sha1.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

var _ = errors.New

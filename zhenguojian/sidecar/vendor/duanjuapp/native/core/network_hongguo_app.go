package core

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tlsclient "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
)

// 红果 App 接口（api5-normal-sinfonline*.fqnovel.com）除了校验六个签名头，
// 还会看 TLS 指纹：同样的 URL/参数/签名，用 Go 默认的 net/http 发出去时取流
// 接口会高频回「200 + 空 body」（约 1/7 成功率，每失败一次还要重试 5 秒），
// 换成 Chrome 指纹后同样的请求稳定 200 且 0.2 秒内返回。
// 这里复用仓库里已有的 bogdanfinn/tls-client（黄果网页用的同一套）。
const hongguoAppBrowserProfile = "Chrome 150"

type hongguoAppBrowserTransport struct {
	base      http.RoundTripper
	router    *proxyRouter
	insecure  bool
	record    func(diagnosticEvent)
	mu        sync.Mutex
	clients   map[string]browserHTTPClient
	newClient func(string) (browserHTTPClient, error)
}

func newHongguoAppBrowserTransport(base http.RoundTripper, downloader *Downloader) *hongguoAppBrowserTransport {
	transport := &hongguoAppBrowserTransport{
		base: base, router: downloader.proxyRouter, insecure: downloader.cfg.InsecureTLS,
		record: downloader.recordDiagnostic, clients: make(map[string]browserHTTPClient),
	}
	transport.newClient = transport.createClient
	return transport
}

func hongguoAppAPIHost(host string) bool {
	host = strings.ToLower(host)
	switch host {
	case "api5-normal-sinfonlinea.fqnovel.com",
		"api5-normal-sinfonlineb.fqnovel.com",
		"api5-normal-sinfonlinec.fqnovel.com",
		"api5-normal-lf.fqnovel.com",
		"api5-normal-lq.fqnovel.com",
		"api5-normal-hl.fqnovel.com":
		return true
	}
	return false
}

func (transport *hongguoAppBrowserTransport) matches(request *http.Request) bool {
	if request.URL == nil || !strings.EqualFold(request.URL.Scheme, "https") {
		return false
	}
	return hongguoAppAPIHost(request.URL.Hostname())
}

func (transport *hongguoAppBrowserTransport) createClient(proxy string) (browserHTTPClient, error) {
	idleTimeout := 90 * time.Second
	options := []tlsclient.HttpClientOption{
		tlsclient.WithClientProfile(profiles.Chrome_150),
		tlsclient.WithRandomTLSExtensionOrder(),
		tlsclient.WithDisableHttp3(),
		tlsclient.WithNotFollowRedirects(),
		tlsclient.WithTimeoutSeconds(30),
		tlsclient.WithProxyUrl(proxy),
		tlsclient.WithCookieJar(tlsclient.NewCookieJar()),
		tlsclient.WithTransportOptions(&tlsclient.TransportOptions{
			IdleConnTimeout: &idleTimeout, MaxIdleConns: 8, MaxIdleConnsPerHost: 4,
			MaxResponseHeaderBytes: 1 << 20,
		}),
	}
	if transport.insecure {
		options = append(options, tlsclient.WithInsecureSkipVerify())
	}
	return tlsclient.NewHttpClient(tlsclient.NewNoopLogger(), options...)
}

func (transport *hongguoAppBrowserTransport) client(request *http.Request) (browserHTTPClient, error) {
	transport.mu.Lock()
	defer transport.mu.Unlock()
	proxyURL := ""
	if transport.router != nil {
		proxy, err := transport.router.proxy(request)
		if err != nil {
			return nil, err
		}
		if proxy != nil {
			proxyURL = proxy.String()
		}
	}
	if client := transport.clients[proxyURL]; client != nil {
		return client, nil
	}
	client, err := transport.newClient(proxyURL)
	if err != nil {
		return nil, err
	}
	if len(transport.clients) >= 4 {
		for key, old := range transport.clients {
			old.CloseIdleConnections()
			delete(transport.clients, key)
			break
		}
	}
	transport.clients[proxyURL] = client
	return client, nil
}

// hongguoAppBrowserHeaders 把 net/http 的头原样搬给 fhttp，并固定发送顺序。
// 顺序按参考客户端的抓包排列，避免不同请求之间的头部顺序抖动。
func hongguoAppBrowserHeaders(request *http.Request) fhttp.Header {
	headers := make(fhttp.Header, len(request.Header)+1)
	for key, values := range request.Header {
		lower := strings.ToLower(key)
		if lower == "host" || lower == "connection" || lower == "content-length" {
			continue
		}
		headers[lower] = append([]string(nil), values...)
	}
	order := []string{
		"user-agent", "accept", "x-xs-from-web", "sdk-version", "x-tt-request-tag",
		"passport-sdk-version", "x-vc-bdturing-sdk-version", "x-tt-dt",
		"content-type", "x-gorgon", "x-khronos", "x-ladon", "x-argus",
		"x-helios", "x-medusa", "x-ss-req-ticket", "x-ss-stub",
	}
	ordered := make([]string, 0, len(order)+len(headers))
	for _, key := range order {
		if _, found := headers[key]; found {
			ordered = append(ordered, key)
		}
	}
	for key := range headers {
		known := false
		for _, item := range order {
			if key == item {
				known = true
				break
			}
		}
		if !known {
			ordered = append(ordered, key)
		}
	}
	headers[fhttp.HeaderOrderKey] = ordered
	return headers
}

func (transport *hongguoAppBrowserTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if !transport.matches(request) {
		return transport.base.RoundTrip(request)
	}
	client, err := transport.client(request)
	if err != nil {
		return nil, fmt.Errorf("红果 App 浏览器指纹客户端初始化失败：%w", publicError(err))
	}
	var body io.Reader
	if request.Body != nil {
		body = request.Body
	}
	upstream, err := fhttp.NewRequestWithContext(request.Context(), request.Method, request.URL.String(), body)
	if err != nil {
		return nil, err
	}
	upstream.Header = hongguoAppBrowserHeaders(request)
	upstream.Host = request.Host
	upstream.ContentLength = request.ContentLength
	upstream.GetBody = request.GetBody
	response, err := client.Do(upstream)
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return nil, publicError(err)
	}
	result := &http.Response{
		Status: response.Status, StatusCode: response.StatusCode,
		Proto: response.Proto, ProtoMajor: response.ProtoMajor, ProtoMinor: response.ProtoMinor,
		Header: standardBrowserHeaders(response.Header), Body: response.Body,
		ContentLength: response.ContentLength, TransferEncoding: response.TransferEncoding,
		Close: response.Close, Uncompressed: response.Uncompressed,
		Trailer: standardBrowserHeaders(response.Trailer), Request: request,
	}
	if transport.record != nil {
		level := "info"
		if result.StatusCode >= 400 {
			level = "warning"
		}
		transport.record(diagnosticEvent{Event: "network.hongguo_app_request", Level: level, Source: sourceHongguo,
			Host: request.URL.Hostname(), HTTPStatus: result.StatusCode, Client: hongguoAppBrowserProfile,
			Protocol: result.Proto, ResponseType: truncate(result.Header.Get("Content-Type"), 128),
			Message: "红果 App 浏览器指纹请求完成"})
	}
	return result, nil
}

func (transport *hongguoAppBrowserTransport) CloseIdleConnections() {
	transport.mu.Lock()
	for key, client := range transport.clients {
		client.CloseIdleConnections()
		delete(transport.clients, key)
	}
	transport.mu.Unlock()
	if closer, ok := transport.base.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

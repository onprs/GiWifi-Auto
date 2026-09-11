package control

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	ProtocolVersion       = 1
	DefaultMaxMessage     = 1 << 20
	DefaultRequestTimeout = 10 * time.Second
)

// Request 是本地控制请求。
type Request struct {
	Version int             `json:"version"`
	ID      string          `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

// Response 是本地控制响应。
type Response struct {
	Version int             `json:"version"`
	ID      string          `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}

// RPCError 是机器可识别的控制错误。
type RPCError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// RemoteError 表示服务端返回的错误。
type RemoteError struct {
	Code    string
	Message string
}

func (e *RemoteError) Error() string {
	if e == nil {
		return ""
	}
	return e.Code + ": " + e.Message
}

// Handler 处理一个已通过协议校验的请求。返回值必须是可脱敏的响应数据。
type Handler interface {
	Handle(context.Context, Request) (interface{}, *RPCError)
}

// HandlerFunc 将函数适配为 Handler。
type HandlerFunc func(context.Context, Request) (interface{}, *RPCError)

func (handler HandlerFunc) Handle(ctx context.Context, request Request) (interface{}, *RPCError) {
	return handler(ctx, request)
}

// Server 提供本地控制服务。
type Server struct {
	listener        net.Listener
	handler         Handler
	maxMessageBytes int
	requestTimeout  time.Duration
	closeOnce       sync.Once
}

// NewServer 使用已有监听器创建控制服务。
func NewServer(listener net.Listener, handler Handler) (*Server, error) {
	if listener == nil {
		return nil, errors.New("控制服务监听器不能为空")
	}
	if handler == nil {
		return nil, errors.New("控制服务处理器不能为空")
	}
	return &Server{
		listener:        listener,
		handler:         handler,
		maxMessageBytes: DefaultMaxMessage,
		requestTimeout:  DefaultRequestTimeout,
	}, nil
}

// Listen 根据地址创建监听器。默认路径是 Unix Socket，tcp 仅允许回环地址。
func Listen(address string) (net.Listener, error) {
	if strings.HasPrefix(address, "tcp://") {
		parsed, err := parseLoopbackTCPAddress(address)
		if err != nil {
			return nil, err
		}
		return net.Listen("tcp", parsed.Host)
	}
	if address == "" || !strings.HasPrefix(address, "/") || strings.IndexByte(address, 0) >= 0 || strings.ContainsAny(address, "\r\n") {
		return nil, errors.New("控制地址必须是绝对 Unix Socket 路径或回环 TCP 地址")
	}
	if info, err := os.Stat(address); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, errors.New("控制 Socket 路径已被非 Socket 文件占用")
		}
		if err := os.Remove(address); err != nil {
			return nil, fmt.Errorf("删除旧控制 Socket 失败: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("检查控制 Socket 失败: %w", err)
	}
	listener, err := net.Listen("unix", address)
	if err != nil {
		return nil, fmt.Errorf("创建 Unix Socket 失败: %w", err)
	}
	if err := os.Chmod(address, 0o600); err != nil {
		_ = listener.Close()
		_ = os.Remove(address)
		return nil, fmt.Errorf("设置控制 Socket 权限失败: %w", err)
	}
	return &unixListener{Listener: listener, path: address}, nil
}

// Serve 接受请求直到 context 取消或监听器关闭。
func (server *Server) Serve(ctx context.Context) error {
	if ctx == nil {
		return errors.New("控制服务上下文不能为空")
	}
	closed := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = server.listener.Close()
		case <-closed:
		}
	}()
	defer close(closed)

	for {
		connection, err := server.listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			if temporary(err) {
				continue
			}
			return fmt.Errorf("接受控制连接失败: %w", err)
		}
		go server.serveConnection(ctx, connection)
	}
}

// Close 主动关闭监听器。
func (server *Server) Close() error {
	var err error
	server.closeOnce.Do(func() {
		err = server.listener.Close()
	})
	return err
}

// Call 向本地控制服务发送一次请求并解码结果。
func Call(ctx context.Context, address, method string, params interface{}, result interface{}) error {
	if ctx == nil {
		return errors.New("控制请求上下文不能为空")
	}
	if method == "" {
		return errors.New("控制方法不能为空")
	}
	connection, err := dial(ctx, address)
	if err != nil {
		return err
	}
	defer connection.Close()

	stopOnCancel := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = connection.Close()
		case <-stopOnCancel:
		}
	}()
	defer close(stopOnCancel)
	if deadline, exists := ctx.Deadline(); exists {
		_ = connection.SetDeadline(deadline)
	}

	encodedParams, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("编码控制参数失败: %w", err)
	}
	request := Request{
		Version: ProtocolVersion,
		ID:      strconv.FormatInt(time.Now().UnixNano(), 10),
		Method:  method,
		Params:  encodedParams,
	}
	if err := writeJSONLineLimited(connection, request, DefaultMaxMessage); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("发送控制请求失败: %w", err)
	}

	line, err := readJSONLine(bufio.NewReader(connection), DefaultMaxMessage)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("读取控制响应失败: %w", err)
	}
	var response Response
	if err := decodeResponse(line, &response); err != nil {
		return err
	}
	if response.ID != request.ID {
		return errors.New("控制响应 ID 不匹配")
	}
	if response.Error != nil {
		return &RemoteError{Code: response.Error.Code, Message: response.Error.Message}
	}
	if result == nil || len(response.Result) == 0 || bytes.Equal(response.Result, []byte("null")) {
		return nil
	}
	if err := json.Unmarshal(response.Result, result); err != nil {
		return fmt.Errorf("解码控制结果失败: %w", err)
	}
	return nil
}

func (server *Server) serveConnection(parent context.Context, connection net.Conn) {
	defer connection.Close()
	connectionDone := make(chan struct{})
	defer close(connectionDone)
	go func() {
		select {
		case <-parent.Done():
			_ = connection.Close()
		case <-connectionDone:
		}
	}()
	reader := bufio.NewReader(connection)
	for {
		if server.requestTimeout > 0 {
			_ = connection.SetReadDeadline(time.Now().Add(server.requestTimeout))
		}
		line, err := readJSONLine(reader, server.maxMessageBytes)
		_ = connection.SetReadDeadline(time.Time{})
		if err != nil {
			if parent.Err() != nil || errors.Is(err, io.EOF) {
				return
			}
			_ = writeJSONLineLimited(connection, Response{
				Version: ProtocolVersion,
				Error:   &RPCError{Code: "invalid_request", Message: "请求格式无效"},
			}, server.maxMessageBytes)
			return
		}

		request, err := decodeRequest(line)
		if err != nil {
			_ = writeJSONLineLimited(connection, Response{
				Version: ProtocolVersion,
				Error:   &RPCError{Code: "invalid_request", Message: "请求格式无效"},
			}, server.maxMessageBytes)
			return
		}
		requestContext := parent
		cancel := func() {}
		if server.requestTimeout > 0 {
			requestContext, cancel = context.WithTimeout(parent, server.requestTimeout)
		}
		result, rpcError := server.handler.Handle(requestContext, request)
		cancel()
		response := Response{Version: ProtocolVersion, ID: request.ID, Error: rpcError}
		if rpcError == nil {
			encoded, err := json.Marshal(result)
			if err != nil {
				response.Error = &RPCError{Code: "internal_error", Message: "控制结果无法编码"}
			} else {
				response.Result = encoded
			}
		}
		if err := writeJSONLineLimited(connection, response, server.maxMessageBytes); err != nil {
			return
		}
	}
}

func decodeRequest(line []byte) (Request, error) {
	var request Request
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return Request{}, err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Request{}, errors.New("请求包含多段 JSON 数据")
	}
	if request.Version != ProtocolVersion || request.ID == "" || request.Method == "" {
		return Request{}, errors.New("请求版本、ID 或方法无效")
	}
	if len(request.ID) > 128 || len(request.Method) > 128 {
		return Request{}, errors.New("请求标识过长")
	}
	if len(request.Params) == 0 {
		request.Params = json.RawMessage("{}")
	}
	return request, nil
}

func decodeResponse(line []byte, response *Response) error {
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(response); err != nil {
		return fmt.Errorf("控制响应格式无效: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("控制响应包含多段 JSON 数据")
	}
	if response.Version != ProtocolVersion {
		return errors.New("控制响应版本不兼容")
	}
	return nil
}

func writeJSONLineLimited(writer io.Writer, value interface{}, maxBytes int) error {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return err
	}
	if maxBytes > 0 && buffer.Len() > maxBytes {
		return errors.New("控制消息超过大小限制")
	}
	written, err := writer.Write(buffer.Bytes())
	if err != nil {
		return err
	}
	if written != buffer.Len() {
		return io.ErrShortWrite
	}
	return nil
}

func readJSONLine(reader *bufio.Reader, maxBytes int) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, errors.New("消息大小限制无效")
	}
	var line []byte
	for {
		part, err := reader.ReadSlice('\n')
		line = append(line, part...)
		if len(line) > maxBytes {
			return nil, errors.New("控制消息超过大小限制")
		}
		if err == nil {
			return bytes.TrimSpace(line), nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) && len(line) > 0 {
			return bytes.TrimSpace(line), nil
		}
		return nil, err
	}
}

func dial(ctx context.Context, address string) (net.Conn, error) {
	if strings.HasPrefix(address, "tcp://") {
		parsed, err := parseLoopbackTCPAddress(address)
		if err != nil {
			return nil, err
		}
		return (&net.Dialer{}).DialContext(ctx, "tcp", parsed.Host)
	}
	if address == "" || !strings.HasPrefix(address, "/") || strings.IndexByte(address, 0) >= 0 || strings.ContainsAny(address, "\r\n") {
		return nil, errors.New("控制地址必须是绝对 Unix Socket 路径或回环 TCP 地址")
	}
	return (&net.Dialer{}).DialContext(ctx, "unix", address)
}

func parseLoopbackTCPAddress(address string) (*url.URL, error) {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme != "tcp" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("回环 TCP 控制地址无效")
	}
	host := parsed.Hostname()
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return nil, errors.New("TCP 控制地址只允许回环主机")
	}
	port, err := strconv.Atoi(parsed.Port())
	if err != nil || port < 1 || port > 65535 {
		return nil, errors.New("TCP 控制地址端口无效")
	}
	return parsed, nil
}

func (listener *unixListener) Close() error {
	listener.once.Do(func() {
		listener.closeErr = listener.Listener.Close()
		if err := os.Remove(listener.path); err != nil && !errors.Is(err, os.ErrNotExist) && listener.closeErr == nil {
			listener.closeErr = err
		}
	})
	return listener.closeErr
}

func (listener *unixListener) Addr() net.Addr {
	return listener.Listener.Addr()
}

type unixListener struct {
	net.Listener
	path     string
	once     sync.Once
	closeErr error
}

func temporary(err error) bool {
	temporaryError, ok := err.(net.Error)
	return ok && temporaryError.Temporary()
}

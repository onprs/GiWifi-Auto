package credential

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"
)

const maxCredentialBytes = 4096

// Category 是凭据读取错误的分类。
type Category string

const (
	CategoryConfiguration Category = "credential_configuration"
	CategoryNotFound      Category = "credential_not_found"
	CategoryAccess        Category = "credential_access"
	CategoryUnavailable   Category = "credential_unavailable"
)

// Resolver 从外部安全存储读取密码，不把密码放入配置对象。
type Resolver interface {
	Resolve(context.Context, string) (string, error)
}

// Error 是凭据读取错误。错误文本不包含凭据引用值或密码。
type Error struct {
	Category Category
	message  string
	cause    error
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.message == "" {
		return string(e.Category)
	}
	return fmt.Sprintf("%s: %s", e.Category, e.message)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func (e *Error) ErrorCategory() string {
	if e == nil {
		return ""
	}
	return string(e.Category)
}

// DefaultResolver 根据凭据引用前缀选择环境变量、文件或 UCI 读取方式。
type DefaultResolver struct{}

func (DefaultResolver) Resolve(ctx context.Context, reference string) (string, error) {
	if ctx == nil {
		return "", newError(CategoryConfiguration, "请求上下文不能为空", nil)
	}
	if strings.TrimSpace(reference) == "" || strings.IndexFunc(reference, unicode.IsSpace) >= 0 {
		return "", newError(CategoryConfiguration, "凭据引用无效", nil)
	}

	switch {
	case strings.HasPrefix(reference, "env:"):
		return resolveEnvironment(reference[len("env:"):])
	case strings.HasPrefix(reference, "file:"):
		return resolveFile(reference[len("file:"):])
	case strings.HasPrefix(reference, "uci:"):
		return resolveUCI(ctx, reference[len("uci:"):])
	default:
		return "", newError(CategoryConfiguration, "不支持的凭据引用类型", nil)
	}
}

func resolveEnvironment(name string) (string, error) {
	if !validEnvironmentName(name) {
		return "", newError(CategoryConfiguration, "环境变量引用无效", nil)
	}
	value, exists := os.LookupEnv(name)
	if !exists || value == "" {
		return "", newError(CategoryNotFound, "环境变量凭据不存在", nil)
	}
	if len(value) > maxCredentialBytes || strings.ContainsAny(value, "\r\n") {
		return "", newError(CategoryConfiguration, "环境变量凭据必须是单行且不超过大小限制", nil)
	}
	return value, nil
}

func resolveFile(path string) (string, error) {
	if path == "" || !filepath.IsAbs(path) || strings.IndexByte(path, 0) >= 0 {
		return "", newError(CategoryConfiguration, "凭据文件引用无效", nil)
	}

	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", newError(CategoryNotFound, "凭据文件不存在", err)
		}
		return "", newError(CategoryAccess, "打开凭据文件失败", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return "", newError(CategoryAccess, "读取凭据文件属性失败", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return "", newError(CategoryConfiguration, "凭据文件权限必须限制为所有者可读", nil)
	}

	value, err := io.ReadAll(io.LimitReader(file, maxCredentialBytes+1))
	if err != nil {
		return "", newError(CategoryAccess, "读取凭据文件失败", err)
	}
	if len(value) > maxCredentialBytes {
		return "", newError(CategoryConfiguration, "凭据文件超过大小限制", nil)
	}
	value = bytes.TrimSuffix(value, []byte("\n"))
	value = bytes.TrimSuffix(value, []byte("\r"))
	if bytes.ContainsAny(value, "\r\n") || len(value) == 0 {
		return "", newError(CategoryConfiguration, "凭据文件必须包含单行非空凭据", nil)
	}
	return string(value), nil
}

func resolveUCI(ctx context.Context, target string) (string, error) {
	if target == "" || strings.IndexFunc(target, unicode.IsSpace) >= 0 || strings.IndexByte(target, 0) >= 0 {
		return "", newError(CategoryConfiguration, "UCI 凭据引用无效", nil)
	}
	for _, character := range target {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '.' || character == '_' || character == '-' {
			continue
		}
		return "", newError(CategoryConfiguration, "UCI 凭据引用包含非法字符", nil)
	}

	command := exec.CommandContext(ctx, "uci", "-q", "get", target)
	command.Stderr = io.Discard
	var output limitedBuffer
	command.Stdout = &output
	if err := command.Run(); err != nil {
		if output.tooLarge {
			return "", newError(CategoryConfiguration, "UCI 凭据超过大小限制", nil)
		}
		if ctx.Err() != nil {
			return "", newError(CategoryUnavailable, "UCI 读取已取消", ctx.Err())
		}
		return "", newError(CategoryUnavailable, "UCI 凭据读取失败", err)
	}
	if output.tooLarge {
		return "", newError(CategoryConfiguration, "UCI 凭据超过大小限制", nil)
	}
	value := bytes.TrimSuffix(output.Bytes(), []byte("\n"))
	value = bytes.TrimSuffix(value, []byte("\r"))
	if len(value) == 0 || bytes.ContainsAny(value, "\r\n") {
		return "", newError(CategoryNotFound, "UCI 凭据为空", nil)
	}
	return string(value), nil
}

func validEnvironmentName(name string) bool {
	if name == "" {
		return false
	}
	for index, character := range name {
		if (character >= 'A' && character <= 'Z') ||
			(character >= 'a' && character <= 'z') ||
			(index > 0 && character >= '0' && character <= '9') ||
			character == '_' {
			continue
		}
		return false
	}
	return true
}

type limitedBuffer struct {
	bytes.Buffer
	tooLarge bool
}

func (buffer *limitedBuffer) Write(data []byte) (int, error) {
	remaining := maxCredentialBytes - buffer.Len()
	if remaining <= 0 {
		buffer.tooLarge = true
		return 0, io.ErrShortWrite
	}
	if len(data) > remaining {
		buffer.tooLarge = true
		_, _ = buffer.Buffer.Write(data[:remaining])
		return remaining, io.ErrShortWrite
	}
	return buffer.Buffer.Write(data)
}

func newError(category Category, message string, cause error) *Error {
	return &Error{Category: category, message: message, cause: cause}
}

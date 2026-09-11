package config

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

// Load 根据路径选择开发期 JSON 或 OpenWrt UCI 配置。
func Load(ctx context.Context, path string) (Config, error) {
	if strings.HasSuffix(strings.ToLower(path), ".json") {
		return LoadFile(path)
	}
	return LoadUCIFile(ctx, path)
}

// ParseUCI 将 uci export 输出解析为当前配置模型。
func ParseUCI(data []byte) (Config, error) {
	document, err := parseUCIDocument(data)
	if err != nil {
		return Config{}, err
	}
	return document.config()
}

// LoadUCIFile 从 OpenWrt 的 UCI 数据库读取配置。
func LoadUCIFile(ctx context.Context, path string) (Config, error) {
	if ctx == nil {
		return Config{}, errors.New("UCI 配置读取上下文不能为空")
	}
	packageName, err := uciPackageName(path)
	if err != nil {
		return Config{}, err
	}
	output, err := runUCI(ctx, "export", packageName)
	if err != nil {
		return Config{}, fmt.Errorf("读取 UCI 配置失败: %w", err)
	}
	cfg, err := ParseUCI(output)
	if err != nil {
		return Config{}, fmt.Errorf("解析 UCI 配置失败: %w", err)
	}
	return cfg, nil
}

// UpdateUCIAccountEnabled 通过 UCI 事务更新账号启用状态。
func UpdateUCIAccountEnabled(ctx context.Context, path, id string, enabled bool) error {
	if ctx == nil {
		return errors.New("UCI 配置更新上下文不能为空")
	}
	packageName, err := uciPackageName(path)
	if err != nil {
		return err
	}
	if !validUCIIdentifier(id) {
		return errors.New("账号 ID 不能作为 UCI section 标识")
	}
	value := "0"
	if enabled {
		value = "1"
	}
	assignment := packageName + "." + id + ".enabled=" + value
	if _, err := runUCI(ctx, "set", assignment); err != nil {
		return fmt.Errorf("更新 UCI 账号状态失败: %w", err)
	}
	if _, err := runUCI(ctx, "commit", packageName); err != nil {
		_, _ = runUCI(ctx, "revert", packageName)
		return fmt.Errorf("提交 UCI 账号状态失败: %w", err)
	}
	return nil
}

type uciDocument struct {
	packageName string
	sections    []uciSection
}

type uciSection struct {
	kind    string
	name    string
	options map[string]string
}

func parseUCIDocument(data []byte) (uciDocument, error) {
	if len(data) > maxConfigFileSize {
		return uciDocument{}, errors.New("UCI 配置导出超过 1 MiB 限制")
	}
	document := uciDocument{}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 1024), maxConfigFileSize+1)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		tokens, err := tokenizeUCILine(line)
		if err != nil {
			return uciDocument{}, fmt.Errorf("第 %d 行格式错误: %w", lineNumber, err)
		}
		if len(tokens) == 0 {
			continue
		}
		switch tokens[0] {
		case "package":
			if len(tokens) != 2 || document.packageName != "" {
				return uciDocument{}, fmt.Errorf("第 %d 行 package 声明无效", lineNumber)
			}
			if !validUCIIdentifier(tokens[1]) {
				return uciDocument{}, fmt.Errorf("第 %d 行 package 名称无效", lineNumber)
			}
			document.packageName = tokens[1]
		case "config":
			if len(tokens) != 3 {
				return uciDocument{}, fmt.Errorf("第 %d 行 config 声明无效", lineNumber)
			}
			if !validUCIIdentifier(tokens[1]) || !validUCIIdentifier(tokens[2]) {
				return uciDocument{}, fmt.Errorf("第 %d 行 section 标识无效", lineNumber)
			}
			for _, section := range document.sections {
				if section.name == tokens[2] {
					return uciDocument{}, fmt.Errorf("第 %d 行 section 名称重复", lineNumber)
				}
			}
			document.sections = append(document.sections, uciSection{kind: tokens[1], name: tokens[2], options: make(map[string]string)})
		case "option":
			if len(tokens) != 3 || len(document.sections) == 0 {
				return uciDocument{}, fmt.Errorf("第 %d 行 option 声明无效", lineNumber)
			}
			section := &document.sections[len(document.sections)-1]
			if _, exists := section.options[tokens[1]]; exists {
				return uciDocument{}, fmt.Errorf("第 %d 行 option 重复", lineNumber)
			}
			section.options[tokens[1]] = tokens[2]
		default:
			return uciDocument{}, fmt.Errorf("第 %d 行包含不支持的 UCI 命令", lineNumber)
		}
	}
	if err := scanner.Err(); err != nil {
		return uciDocument{}, fmt.Errorf("读取 UCI 文本失败: %w", err)
	}
	return document, nil
}

func (document uciDocument) config() (Config, error) {
	cfg := Config{Accounts: make([]AccountConfig, 0)}
	runtimeSection := -1
	for index, section := range document.sections {
		switch section.kind {
		case "runtime":
			if runtimeSection >= 0 {
				return Config{}, errors.New("UCI 配置包含多个 runtime section")
			}
			runtimeSection = index
		case "account":
			account, err := parseUCIAccount(section)
			if err != nil {
				return Config{}, err
			}
			cfg.Accounts = append(cfg.Accounts, account)
		default:
			return Config{}, fmt.Errorf("UCI 配置包含不支持的 section 类型 %q", section.kind)
		}
	}
	if runtimeSection < 0 {
		return Config{}, errors.New("UCI 配置缺少 runtime section")
	}
	if err := parseUCIRuntime(document.sections[runtimeSection], &cfg); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func parseUCIRuntime(section uciSection, cfg *Config) error {
	for option, value := range section.options {
		switch option {
		case "version":
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("runtime.version 无效")
			}
			cfg.Version = parsed
		case "connectivity_url":
			cfg.Runtime.ConnectivityURL = value
		case "portal_login_url":
			cfg.Runtime.PortalLoginURL = value
		case "check_interval_seconds":
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("runtime.check_interval_seconds 无效")
			}
			cfg.Runtime.CheckIntervalSeconds = parsed
		case "request_timeout_seconds":
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("runtime.request_timeout_seconds 无效")
			}
			cfg.Runtime.RequestTimeoutSeconds = parsed
		case "retry_initial_seconds":
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("runtime.retry_initial_seconds 无效")
			}
			cfg.Runtime.RetryInitialSeconds = parsed
		case "retry_max_seconds":
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("runtime.retry_max_seconds 无效")
			}
			cfg.Runtime.RetryMaxSeconds = parsed
		case "max_concurrent_requests":
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("runtime.max_concurrent_requests 无效")
			}
			cfg.Runtime.MaxConcurrentRequests = parsed
		case "log_level":
			cfg.Runtime.LogLevel = value
		case "event_buffer_size":
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("runtime.event_buffer_size 无效")
			}
			cfg.Runtime.EventBufferSize = parsed
		case "control_socket":
			cfg.Runtime.ControlSocket = value
		default:
			return fmt.Errorf("UCI runtime 包含未知 option %q", option)
		}
	}
	return nil
}

func parseUCIAccount(section uciSection) (AccountConfig, error) {
	account := AccountConfig{}
	for option, value := range section.options {
		switch option {
		case "id":
			account.ID = value
		case "display_name":
			account.DisplayName = value
		case "username":
			account.Username = value
		case "credential_ref":
			account.CredentialRef = value
		case "enabled":
			parsed, err := parseUCIBool(value)
			if err != nil {
				return AccountConfig{}, fmt.Errorf("账号 %q 的 enabled 无效", section.name)
			}
			account.Enabled = parsed
		case "priority":
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return AccountConfig{}, fmt.Errorf("账号 %q 的 priority 无效", section.name)
			}
			account.Priority = parsed
		case "network_interface":
			account.NetworkInterface = value
		default:
			return AccountConfig{}, fmt.Errorf("UCI account 包含未知 option %q", option)
		}
	}
	if account.ID == "" {
		account.ID = section.name
	}
	if account.ID != section.name {
		return AccountConfig{}, fmt.Errorf("账号 section 名称必须与 id 一致")
	}
	return account, nil
}

func parseUCIBool(value string) (bool, error) {
	switch value {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, errors.New("布尔值无效")
	}
}

func tokenizeUCILine(line string) ([]string, error) {
	var tokens []string
	var token strings.Builder
	var quote rune
	escaped := false
	tokenStarted := false
	flush := func() {
		if tokenStarted {
			tokens = append(tokens, token.String())
			token.Reset()
			tokenStarted = false
		}
	}
	for _, character := range line {
		if escaped {
			token.WriteRune(character)
			escaped = false
			tokenStarted = true
			continue
		}
		if character == '\\' {
			escaped = true
			tokenStarted = true
			continue
		}
		if quote != 0 {
			if character == quote {
				quote = 0
			} else {
				token.WriteRune(character)
			}
			tokenStarted = true
			continue
		}
		switch {
		case character == '\'' || character == '"':
			quote = character
			tokenStarted = true
		case character == '#':
			flush()
			return tokens, nil
		case unicode.IsSpace(character):
			flush()
		default:
			token.WriteRune(character)
			tokenStarted = true
		}
	}
	if escaped || quote != 0 {
		return nil, errors.New("引号或转义不完整")
	}
	flush()
	return tokens, nil
}

func uciPackageName(path string) (string, error) {
	if path == "" || strings.IndexByte(path, 0) >= 0 {
		return "", errors.New("UCI 配置路径无效")
	}
	name := filepath.Base(filepath.Clean(path))
	if !validUCIIdentifier(name) {
		return "", errors.New("UCI package 名称无效")
	}
	return name, nil
}

func validUCIIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			character == '_' || character == '-' || character == '.' {
			continue
		}
		return false
	}
	return true
}

func runUCI(ctx context.Context, arguments ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "uci", append([]string{"-q"}, arguments...)...)
	command.Stderr = io.Discard
	var output limitedCommandBuffer
	command.Stdout = &output
	if err := command.Run(); err != nil {
		if output.tooLarge {
			return nil, errors.New("UCI 输出超过大小限制")
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	if output.tooLarge {
		return nil, errors.New("UCI 输出超过大小限制")
	}
	return output.Bytes(), nil
}

type limitedCommandBuffer struct {
	bytes.Buffer
	tooLarge bool
}

func (buffer *limitedCommandBuffer) Write(data []byte) (int, error) {
	remaining := maxConfigFileSize - buffer.Len()
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

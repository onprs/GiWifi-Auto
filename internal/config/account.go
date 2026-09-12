package config

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// CredentialReferenceForAccount 返回 TUI 账号配置使用的凭据引用。
func CredentialReferenceForAccount(path, id string) (string, error) {
	if path == "" {
		return "", errors.New("配置路径不能为空")
	}
	if !validAccountID(id) {
		return "", errors.New("账号 ID 无效")
	}
	if strings.HasSuffix(strings.ToLower(path), ".json") {
		absolutePath, err := filepath.Abs(path)
		if err != nil {
			return "", fmt.Errorf("解析配置路径失败: %w", err)
		}
		return "file:" + filepath.Join(filepath.Dir(absolutePath), ".giwifi-credentials", id), nil
	}
	if !validUCIIdentifier(id) {
		return "", errors.New("UCI 账号 ID 只能包含字母、数字和下划线")
	}
	return "uci:giwifi-credentials." + id + ".password", nil
}

// SaveAccountConfiguration 持久化账号配置和可选的新密码。
func SaveAccountConfiguration(ctx context.Context, path string, cfg Config, id, password string, passwordProvided bool) error {
	if ctx == nil {
		return errors.New("账号配置更新上下文不能为空")
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("账号配置保存前校验失败: %w", err)
	}
	if !passwordProvided && password != "" {
		return errors.New("未标记的新密码不能包含内容")
	}
	if passwordProvided {
		if err := validateStoredPassword(password); err != nil {
			return err
		}
	}

	var accountFound bool
	for _, account := range cfg.Accounts {
		if account.ID == id {
			accountFound = true
			break
		}
	}
	if !accountFound {
		return fmt.Errorf("账号 %q 不存在", id)
	}

	if strings.HasSuffix(strings.ToLower(path), ".json") {
		if passwordProvided {
			if err := saveJSONCredential(path, id, password); err != nil {
				return err
			}
		}
		return SaveFileAtomic(path, cfg)
	}
	return saveUCIAccountConfiguration(ctx, path, cfg, id, password, passwordProvided)
}

// DeleteAccountConfiguration 持久化删除账号后的配置，并清理程序管理的凭据。
func DeleteAccountConfiguration(ctx context.Context, path string, cfg Config, removed AccountConfig) error {
	if ctx == nil {
		return errors.New("账号删除上下文不能为空")
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("账号删除保存前校验失败: %w", err)
	}
	if !validAccountID(removed.ID) {
		return errors.New("被删除的账号 ID 无效")
	}

	if strings.HasSuffix(strings.ToLower(path), ".json") {
		if err := SaveFileAtomic(path, cfg); err != nil {
			return err
		}
		return removeJSONCredential(path, removed)
	}
	return deleteUCIAccountConfiguration(ctx, path, removed)
}

func removeJSONCredential(path string, removed AccountConfig) error {
	expected, err := CredentialReferenceForAccount(path, removed.ID)
	if err != nil || removed.CredentialRef != expected {
		return nil
	}
	credentialPath := strings.TrimPrefix(expected, "file:")
	if err := os.Remove(credentialPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("删除账号凭据文件失败: %w", err)
	}
	return nil
}

func deleteUCIAccountConfiguration(ctx context.Context, path string, removed AccountConfig) error {
	packageName, err := uciPackageName(path)
	if err != nil {
		return err
	}
	if _, err := runUCI(ctx, "delete", packageName+"."+removed.ID); err != nil {
		return fmt.Errorf("删除 UCI 账号失败: %w", err)
	}
	if _, err := runUCI(ctx, "commit", packageName); err != nil {
		_, _ = runUCI(ctx, "revert", packageName)
		return fmt.Errorf("提交 UCI 账号删除失败: %w", err)
	}
	if removed.CredentialRef == "uci:giwifi-credentials."+removed.ID+".password" {
		if _, err := runUCI(ctx, "delete", "giwifi-credentials."+removed.ID); err != nil {
			return fmt.Errorf("删除 UCI 凭据失败: %w", err)
		}
		if _, err := runUCI(ctx, "commit", "giwifi-credentials"); err != nil {
			_, _ = runUCI(ctx, "revert", "giwifi-credentials")
			return fmt.Errorf("提交 UCI 凭据删除失败: %w", err)
		}
	}
	return nil
}

func validateStoredPassword(password string) error {
	if password == "" {
		return errors.New("密码不能为空")
	}
	if len(password) > 4096 || strings.ContainsAny(password, "\r\n") {
		return errors.New("密码必须是单行且不超过 4096 字节")
	}
	return nil
}

func saveJSONCredential(path, id, password string) error {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("解析凭据目录失败: %w", err)
	}
	directory := filepath.Join(filepath.Dir(absolutePath), ".giwifi-credentials")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("创建凭据目录失败: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("设置凭据目录权限失败: %w", err)
	}

	file, err := os.CreateTemp(directory, ".credential-*.tmp")
	if err != nil {
		return fmt.Errorf("创建凭据临时文件失败: %w", err)
	}
	temporaryPath := file.Name()
	removeTemporary := true
	defer func() {
		_ = file.Close()
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("设置凭据临时文件权限失败: %w", err)
	}
	if _, err := io.WriteString(file, password+"\n"); err != nil {
		return fmt.Errorf("写入凭据失败: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("同步凭据文件失败: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("关闭凭据文件失败: %w", err)
	}
	if err := os.Rename(temporaryPath, filepath.Join(directory, id)); err != nil {
		return fmt.Errorf("替换凭据文件失败: %w", err)
	}
	removeTemporary = false
	return nil
}

func saveUCIAccountConfiguration(ctx context.Context, path string, cfg Config, id, password string, passwordProvided bool) error {
	packageName, err := uciPackageName(path)
	if err != nil {
		return err
	}
	runtimeSection, err := findUCIRuntimeSection(ctx, packageName)
	if err != nil {
		return err
	}
	var accountConfig AccountConfig
	for _, account := range cfg.Accounts {
		if account.ID == id {
			accountConfig = account
			break
		}
	}

	if passwordProvided {
		if _, err := runUCI(ctx, "set", "giwifi-credentials."+id+"=credential"); err != nil {
			return fmt.Errorf("设置 UCI 凭据 section 失败: %w", err)
		}
		if _, err := runUCI(ctx, "set", "giwifi-credentials."+id+".password="+password); err != nil {
			_, _ = runUCI(ctx, "revert", "giwifi-credentials")
			return fmt.Errorf("设置 UCI 凭据失败: %w", err)
		}
		if _, err := runUCI(ctx, "commit", "giwifi-credentials"); err != nil {
			_, _ = runUCI(ctx, "revert", "giwifi-credentials")
			return fmt.Errorf("提交 UCI 凭据失败: %w", err)
		}
	}

	assignments := []string{
		packageName + "." + id + "=account",
		packageName + "." + id + ".id=" + accountConfig.ID,
		packageName + "." + id + ".display_name=" + accountConfig.DisplayName,
		packageName + "." + id + ".username=" + accountConfig.Username,
		packageName + "." + id + ".credential_ref=" + accountConfig.CredentialRef,
		packageName + "." + id + ".enabled=" + boolValue(accountConfig.Enabled),
		packageName + "." + id + ".priority=" + fmt.Sprintf("%d", accountConfig.Priority),
		packageName + "." + id + ".network_interface=" + accountConfig.NetworkInterface,
		packageName + "." + runtimeSection + ".portal_login_url=" + cfg.Runtime.PortalLoginURL,
	}
	for _, assignment := range assignments {
		if _, err := runUCI(ctx, "set", assignment); err != nil {
			_, _ = runUCI(ctx, "revert", packageName)
			return fmt.Errorf("更新 UCI 配置失败: %w", err)
		}
	}
	if _, err := runUCI(ctx, "commit", packageName); err != nil {
		_, _ = runUCI(ctx, "revert", packageName)
		return fmt.Errorf("提交 UCI 配置失败: %w", err)
	}
	if passwordProvided {
		credentialPath := filepath.Join(filepath.Dir(filepath.Clean(path)), "giwifi-credentials")
		if err := os.Chmod(credentialPath, 0o600); err != nil {
			return fmt.Errorf("设置 UCI 凭据文件权限失败: %w", err)
		}
	}
	return nil
}

func findUCIRuntimeSection(ctx context.Context, packageName string) (string, error) {
	output, err := runUCI(ctx, "show", packageName)
	if err != nil {
		return "", fmt.Errorf("查找 UCI runtime section 失败: %w", err)
	}
	prefix := packageName + "."
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		assignment, _, found := strings.Cut(strings.TrimPrefix(line, prefix), "=")
		if !found || strings.Contains(assignment, ".") || assignment == "" {
			continue
		}
		value := strings.Trim(strings.TrimSpace(strings.SplitN(line, "=", 2)[1]), "'\"")
		if value == "runtime" {
			return assignment, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("读取 UCI runtime section 失败: %w", err)
	}
	return "", errors.New("UCI 配置缺少 runtime section")
}

func boolValue(value bool) string {
	if value {
		return "1"
	}
	return "0"
}

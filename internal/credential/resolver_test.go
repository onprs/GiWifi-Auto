package credential

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDefaultResolverReadsEnvironmentCredential(t *testing.T) {
	const name = "GIWIFI_TEST_CREDENTIAL"
	t.Setenv(name, "synthetic-secret")

	value, err := (DefaultResolver{}).Resolve(context.Background(), "env:"+name)
	if err != nil {
		t.Fatalf("Resolve() 失败: %v", err)
	}
	if value != "synthetic-secret" {
		t.Fatalf("凭据 = %q", value)
	}
}

func TestDefaultResolverRejectsMultilineOrOversizedEnvironmentCredential(t *testing.T) {
	const name = "GIWIFI_INVALID_CREDENTIAL"
	for _, value := range []string{"line1\nline2", strings.Repeat("x", maxCredentialBytes+1)} {
		t.Setenv(name, value)
		_, err := (DefaultResolver{}).Resolve(context.Background(), "env:"+name)
		var credentialErr *Error
		if err == nil || !errors.As(err, &credentialErr) || credentialErr.Category != CategoryConfiguration {
			t.Fatalf("凭据值 %q 的错误 = %T %v", value[:min(len(value), 8)], err, err)
		}
	}
}

func TestDefaultResolverRejectsMissingEnvironmentCredential(t *testing.T) {
	_, err := (DefaultResolver{}).Resolve(context.Background(), "env:GIWIFI_MISSING_CREDENTIAL")
	var credentialErr *Error
	if err == nil || !errors.As(err, &credentialErr) || credentialErr.Category != CategoryNotFound {
		t.Fatalf("错误 = %T %v", err, err)
	}
}

func TestDefaultResolverReadsRestrictedFileCredential(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(path, []byte("synthetic-file-secret\n"), 0o600); err != nil {
		t.Fatalf("写入凭据文件失败: %v", err)
	}

	value, err := (DefaultResolver{}).Resolve(context.Background(), "file:"+path)
	if err != nil {
		t.Fatalf("Resolve() 失败: %v", err)
	}
	if value != "synthetic-file-secret" {
		t.Fatalf("凭据 = %q", value)
	}
}

func TestDefaultResolverRejectsInsecureFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 权限由 ACL 管理，Mode().Perm() 不提供可比的 0600 语义")
	}
	path := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(path, []byte("synthetic-file-secret"), 0o644); err != nil {
		t.Fatalf("写入凭据文件失败: %v", err)
	}

	_, err := (DefaultResolver{}).Resolve(context.Background(), "file:"+path)
	var credentialErr *Error
	if err == nil || !errors.As(err, &credentialErr) || credentialErr.Category != CategoryConfiguration {
		t.Fatalf("错误 = %T %v", err, err)
	}
}

func TestDefaultResolverRejectsUnsupportedOrInvalidReference(t *testing.T) {
	for _, reference := range []string{
		"",
		"literal-secret",
		"env:bad-name",
		"file:relative/path",
		"uci:package section option",
	} {
		_, err := (DefaultResolver{}).Resolve(context.Background(), reference)
		if err == nil {
			t.Errorf("引用 %q 未被拒绝", reference)
		}
	}
}

func TestDefaultResolverDoesNotExposeCredential(t *testing.T) {
	const secret = "synthetic-secret-that-must-not-leak"
	t.Setenv("GIWIFI_TEST_CREDENTIAL", secret)

	_, err := (DefaultResolver{}).Resolve(context.Background(), "env:GIWIFI_TEST_CREDENTIAL_MISSING")
	if err == nil {
		t.Fatal("缺失凭据未返回错误")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("错误泄露凭据: %v", err)
	}
}

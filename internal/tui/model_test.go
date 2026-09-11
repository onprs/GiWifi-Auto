package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/onprs/GiWifi-Auto/internal/account"
	"github.com/onprs/GiWifi-Auto/internal/connectivity"
	"github.com/onprs/GiWifi-Auto/internal/daemon"
	"github.com/onprs/GiWifi-Auto/internal/eventlog"
)

func TestViewFitsNarrowTerminal(t *testing.T) {
	current := model{
		accounts: []account.Snapshot{{
			ID:          "account-primary",
			DisplayName: "中文显示名称很长",
			State:       account.StateAuthenticated,
		}},
		events: []eventlog.Event{{
			AccountID: "account-primary",
			State:     string(account.StateAuthenticated),
			Message:   "网络已确认可用，包含较长消息",
		}},
		width:  28,
		height: 10,
	}

	view := current.View()
	for _, line := range strings.Split(view, "\n") {
		if width := runewidth.StringWidth(line); width > current.width {
			t.Errorf("行宽 %d 超过终端宽度 %d: %q", width, current.width, line)
		}
	}
}

func TestHelpViewFitsNarrowTerminal(t *testing.T) {
	current := model{width: 8, height: 12, showHelp: true}
	for _, line := range strings.Split(current.View(), "\n") {
		if width := runewidth.StringWidth(line); width > current.width {
			t.Errorf("帮助行宽 %d 超过终端宽度 %d: %q", width, current.width, line)
		}
	}
}

func TestUpdateKeepsSelectionWithinAccountRange(t *testing.T) {
	current := model{accounts: []account.Snapshot{
		{ID: "one"},
		{ID: "two"},
	}}
	updated, _ := current.Update(tea.KeyMsg{Type: tea.KeyDown})
	current = updated.(model)
	if current.selected != 1 {
		t.Fatalf("向下选择 = %d, want 1", current.selected)
	}
	updated, _ = current.Update(tea.KeyMsg{Type: tea.KeyDown})
	current = updated.(model)
	if current.selected != 1 {
		t.Fatalf("越界向下选择 = %d, want 1", current.selected)
	}
	updated, _ = current.Update(tea.KeyMsg{Type: tea.KeyUp})
	current = updated.(model)
	if current.selected != 0 {
		t.Fatalf("向上选择 = %d, want 0", current.selected)
	}
}

func TestUpdateLoadsStatusAndClampsSelection(t *testing.T) {
	current := model{selected: 3}
	updated, _ := current.Update(statusMessage{result: daemon.StatusResponse{Accounts: []account.Snapshot{{ID: "only"}}}})
	current = updated.(model)
	if current.selected != 0 || len(current.accounts) != 1 || current.loading {
		t.Fatalf("状态更新 = %+v", current)
	}
}

func TestClipHandlesWideText(t *testing.T) {
	value := clip("中文显示名称", 7)
	if runewidth.StringWidth(value) > 7 {
		t.Fatalf("截断后宽度 = %d", runewidth.StringWidth(value))
	}
	if value == "" {
		t.Fatal("截断结果为空")
	}
}

func TestNewModelUsesProvidedContext(t *testing.T) {
	ctx := context.Background()
	current, ok := NewModel(ctx, "tcp://127.0.0.1:1234").(model)
	if !ok || current.ctx != ctx || current.address != "tcp://127.0.0.1:1234" {
		t.Fatalf("模型 = %+v", current)
	}
}

func TestStatusValuesRemainMachineReadable(t *testing.T) {
	if connectivity.StatusAuthenticated != "authenticated" || connectivity.StatusPortal != "portal" || connectivity.StatusOffline != "offline" {
		t.Fatal("连通性状态值发生不兼容变化")
	}
}

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

func TestUpdateOpensAccountConfigurationForm(t *testing.T) {
	current := model{
		accounts: []account.Snapshot{{ID: "primary"}},
		configuration: daemon.ConfigResponse{Accounts: []daemon.AccountConfigView{{
			ID:          "primary",
			DisplayName: "主账号",
			Username:    "user@example.test",
		}}},
	}
	updated, _ := current.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	current = updated.(model)
	if current.form == nil || !current.form.editing || current.form.id != "primary" {
		t.Fatalf("配置表单 = %+v", current.form)
	}
}

func TestAccountFormMasksPassword(t *testing.T) {
	form := newAccountForm(daemon.ConfigResponse{}, nil)
	form.fields[formPassword].value = "secret-password"
	form.fields[formPassword].cursor = len([]rune(form.fields[formPassword].value))
	view := (model{width: 80, form: form}).View()
	if strings.Contains(view, "secret-password") {
		t.Fatal("TUI 视图泄露密码")
	}
	if !strings.Contains(view, "**************") {
		t.Fatalf("TUI 视图未显示密码掩码: %q", view)
	}
}

func TestAccountFormOnlyRequestsCredentials(t *testing.T) {
	form := newAccountForm(daemon.ConfigResponse{}, nil)
	form.fields[formUsername].value = "user@example.test"
	form.fields[formPassword].value = "secret-password"
	request := form.request()
	if request.ID != "" || request.Username != "user@example.test" || request.Password != "secret-password" || !request.Enabled {
		t.Fatalf("账号请求 = %+v", request)
	}
	view := (model{width: 80, form: form}).View()
	for _, hiddenField := range []string{"Portal", "账号 ID", "显示名称", "网络接口"} {
		if strings.Contains(view, hiddenField) {
			t.Fatalf("TUI 仍显示隐藏配置项 %q: %q", hiddenField, view)
		}
	}
}

func TestUpdateConfirmsAccountDeletion(t *testing.T) {
	current := model{accounts: []account.Snapshot{{ID: "primary"}}}
	updated, command := current.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'D'}})
	current = updated.(model)
	if !current.confirmDelete || current.deleteID != "primary" || command != nil {
		t.Fatalf("删除确认状态 = %+v, command=%v", current, command)
	}
	updated, command = current.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'n'}})
	current = updated.(model)
	if current.confirmDelete || current.deleteID != "" || command != nil {
		t.Fatalf("取消删除状态 = %+v, command=%v", current, command)
	}
}

func TestViewShowsAccountInterfaceAndLineStatus(t *testing.T) {
	current := model{
		accounts: []account.Snapshot{{ID: "primary", DisplayName: "主账号", Enabled: true, State: account.StatePortal}},
		configuration: daemon.ConfigResponse{Accounts: []daemon.AccountConfigView{{
			ID:               "primary",
			Username:         "user@example.test",
			NetworkInterface: "eth1",
		}}},
		interfaces: []daemon.InterfaceStatus{{
			Name:          "eth1",
			Present:       true,
			AdminUp:       true,
			Carrier:       true,
			OperState:     "up",
			IPv4Addresses: []string{"192.0.2.10"},
		}},
		width:  100,
		height: 30,
	}
	view := current.View()
	for _, value := range []string{"账号与线路", "[主账号]", "eth1", "线路状态", "192.0.2.10", "[检测]", "[删除]"} {
		if !strings.Contains(view, value) {
			t.Fatalf("界面缺少 %q: %q", value, view)
		}
	}
	if !strings.Contains(view, "▶") {
		t.Fatalf("界面没有明确选中标记: %q", view)
	}
}

func TestMouseSelectsAccountRow(t *testing.T) {
	current := model{
		accounts: []account.Snapshot{{ID: "one"}, {ID: "two"}},
		width:    100,
		height:   30,
	}
	secondRowY := current.accountAtMouseY(7)
	if secondRowY != 1 {
		t.Fatalf("第二个账号行坐标未计算正确: %d", secondRowY)
	}
	updated, command := current.Update(tea.MouseMsg{X: 4, Y: 7, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	current = updated.(model)
	if command != nil || current.selected != 1 {
		t.Fatalf("鼠标选择 = %d, command=%v", current.selected, command)
	}
}

func TestMouseActionButtonOpensAccountForm(t *testing.T) {
	current := model{width: 100, height: 30}
	actionY := outputLineCount(current.View()) - 1
	rect := mouseButtonRects(mainMouseButtons(false), current.width, actionY)[0]
	updated, command := current.Update(tea.MouseMsg{X: rect.X + 1, Y: actionY, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	current = updated.(model)
	if command != nil || current.form == nil || current.form.editing {
		t.Fatalf("鼠标新增账号 = form=%+v command=%v", current.form, command)
	}
}

func TestMouseDeleteConfirmationUsesButtons(t *testing.T) {
	current := model{
		accounts: []account.Snapshot{{ID: "primary"}},
		width:    100,
		height:   30,
	}
	deleteY := outputLineCount(current.View()) - 1
	deleteRects := mouseButtonRects(mainMouseButtons(false), current.width, deleteY)
	var deleteRect mouseButtonRect
	for _, rect := range deleteRects {
		if rect.ID == "delete" {
			deleteRect = rect
		}
	}
	updated, command := current.Update(tea.MouseMsg{X: deleteRect.X + 1, Y: deleteY, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	current = updated.(model)
	if command != nil || !current.confirmDelete || current.deleteID != "primary" {
		t.Fatalf("鼠标删除确认 = %+v command=%v", current, command)
	}
	confirmY := outputLineCount(current.View()) - 1
	confirmRects := mouseButtonRects(mainMouseButtons(true), current.width, confirmY)
	updated, command = current.Update(tea.MouseMsg{X: confirmRects[0].X + 1, Y: confirmY, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	current = updated.(model)
	if command == nil || current.confirmDelete {
		t.Fatalf("鼠标确认删除 = confirm=%v command=%v", current.confirmDelete, command)
	}
}

func TestMouseFormFocusesFieldAndSaves(t *testing.T) {
	form := newAccountForm(daemon.ConfigResponse{}, nil)
	form.fields[formUsername].value = "user@example.test"
	form.fields[formPassword].value = "password"
	current := model{width: 80, height: 20, form: form}
	updated, command := current.Update(tea.MouseMsg{X: 2, Y: 3, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	current = updated.(model)
	if command != nil || current.form.active != formPassword {
		t.Fatalf("鼠标聚焦密码字段 = %d command=%v", current.form.active, command)
	}
	buttonY := outputLineCount(current.formView()) - 1
	rects := mouseButtonRects(formMouseButtons(), current.width, buttonY)
	updated, command = current.Update(tea.MouseMsg{X: rects[0].X + 1, Y: buttonY, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	current = updated.(model)
	if command == nil || !current.form.saving {
		t.Fatalf("鼠标保存 = saving=%v command=%v", current.form.saving, command)
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

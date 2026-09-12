package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"github.com/onprs/GiWifi-Auto/internal/account"
	"github.com/onprs/GiWifi-Auto/internal/connectivity"
	"github.com/onprs/GiWifi-Auto/internal/control"
	"github.com/onprs/GiWifi-Auto/internal/daemon"
	"github.com/onprs/GiWifi-Auto/internal/eventlog"
)

const (
	refreshInterval  = time.Second
	maxVisibleLogs   = 5
	fullAccountWidth = 78
)

type statusMessage struct {
	result daemon.StatusResponse
	err    error
}

type logsMessage struct {
	events []eventlog.Event
	err    error
}

type actionMessage struct {
	err error
}

type configMessage struct {
	result daemon.ConfigResponse
	err    error
}

type configureMessage struct {
	err error
}

type refreshMessage struct{}

type model struct {
	ctx           context.Context
	address       string
	accounts      []account.Snapshot
	interfaces    []daemon.InterfaceStatus
	events        []eventlog.Event
	selected      int
	width         int
	height        int
	loading       bool
	err           string
	showHelp      bool
	paused        bool
	filterID      string
	configuration daemon.ConfigResponse
	form          *accountForm
	confirmDelete bool
	deleteID      string
}

// NewModel 创建一个只通过本地控制协议工作的 TUI 模型。
func NewModel(ctx context.Context, address string) tea.Model {
	if ctx == nil {
		ctx = context.Background()
	}
	return model{ctx: ctx, address: address, loading: true}
}

// Run 启动 TUI。输入输出由调用方提供，便于 SSH 会话和测试注入。
func Run(ctx context.Context, address string, input io.Reader, output io.Writer) error {
	program := tea.NewProgram(
		NewModel(ctx, address),
		tea.WithContext(ctx),
		tea.WithInput(input),
		tea.WithOutput(output),
		tea.WithAltScreen(),
	)
	_, err := program.Run()
	if errors.Is(err, tea.ErrProgramKilled) && ctx != nil && ctx.Err() != nil {
		return nil
	}
	return err
}

func (current model) Init() tea.Cmd {
	return tea.Batch(fetchStatus(current.ctx, current.address), fetchConfiguration(current.ctx, current.address), fetchLogs(current.ctx, current.address), tick())
}

func (current model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.KeyMsg:
		if current.form != nil {
			return current.updateForm(message)
		}
		if current.confirmDelete {
			switch message.String() {
			case "y", "Y":
				current.confirmDelete = false
				return current, current.deleteSelected()
			case "n", "N", "esc":
				current.confirmDelete = false
				current.deleteID = ""
			}
			return current, nil
		}
		switch message.String() {
		case "ctrl+c", "q":
			return current, tea.Quit
		case "up", "k":
			if current.selected > 0 {
				current.selected--
			}
		case "down", "j":
			if current.selected+1 < len(current.accounts) {
				current.selected++
			}
		case "enter":
			return current, current.triggerSelected()
		case "a":
			current.openNewAccountForm()
		case "c":
			current.openSelectedAccountForm()
		case "l":
			return current, callAction(current.ctx, current.address, daemon.MethodReload, struct{}{})
		case "e":
			return current, current.setSelectedEnabled(true)
		case "d":
			return current, current.setSelectedEnabled(false)
		case "D", "delete":
			current.beginDeleteConfirmation()
		case "r":
			current.loading = true
			return current, current.refresh()
		case "?":
			current.showHelp = !current.showHelp
		case " ":
			current.paused = !current.paused
		case "x":
			current.events = nil
		case "f":
			if current.selected >= 0 && current.selected < len(current.accounts) {
				selectedID := current.accounts[current.selected].ID
				if current.filterID == selectedID {
					current.filterID = ""
				} else {
					current.filterID = selectedID
				}
			}
		}
	case tea.WindowSizeMsg:
		current.width = message.Width
		current.height = message.Height
	case configMessage:
		if message.err != nil {
			current.err = "配置暂时不可用"
		} else {
			current.configuration = message.result
			if current.err == "配置暂时不可用" {
				current.err = ""
			}
		}
	case configureMessage:
		if current.form == nil {
			return current, nil
		}
		current.form.saving = false
		if message.err != nil {
			current.form.err = "保存失败: " + message.err.Error()
			return current, nil
		}
		current.form = nil
		current.loading = true
		return current, current.refresh()
	case deleteMessage:
		current.loading = false
		if message.err != nil {
			current.err = "删除失败: " + message.err.Error()
			return current, nil
		}
		current.deleteID = ""
		current.err = ""
		if current.selected >= len(current.accounts)-1 {
			current.selected = maxInt(0, len(current.accounts)-2)
		}
		return current, current.refresh()
	case statusMessage:
		current.loading = false
		if message.err != nil {
			current.err = "状态暂时不可用"
		} else {
			if current.err == "状态暂时不可用" {
				current.err = ""
			}
			current.accounts = message.result.Accounts
			current.interfaces = message.result.Interfaces
			if current.selected >= len(current.accounts) {
				current.selected = len(current.accounts) - 1
			}
			if current.selected < 0 {
				current.selected = 0
			}
		}
	case logsMessage:
		if message.err == nil && !current.paused {
			current.events = message.events
		}
	case actionMessage:
		current.loading = false
		if message.err != nil {
			current.err = "操作未完成"
		} else {
			current.err = ""
		}
		return current, current.refresh()
	case refreshMessage:
		return current, tea.Batch(fetchStatus(current.ctx, current.address), fetchConfiguration(current.ctx, current.address), fetchLogs(current.ctx, current.address), tick())
	}
	return current, nil
}

func (current model) View() string {
	if current.form != nil {
		return current.formView()
	}
	width, height := current.width, current.height
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}

	var output strings.Builder
	output.WriteString(clip("GiWifi-Auto", width))
	output.WriteByte('\n')
	output.WriteString(strings.Repeat("=", minInt(width, 80)))
	output.WriteByte('\n')
	output.WriteString(clip(current.summaryLine(), width))
	output.WriteByte('\n')
	if current.confirmDelete {
		output.WriteString(clip("确认删除账号 "+current.deleteID+"？按 y 确认，按 n 或 Esc 取消", width))
		output.WriteByte('\n')
	}
	if current.loading {
		output.WriteString(clip("正在加载状态...", width))
		output.WriteByte('\n')
	} else if current.err != "" {
		output.WriteString(clip(current.err, width))
		output.WriteByte('\n')
	}
	if len(current.accounts) == 0 {
		output.WriteString(clip("没有可显示的账号", width))
		output.WriteByte('\n')
	} else {
		if width < fullAccountWidth {
			output.WriteString(clip("账号状态", width))
			output.WriteByte('\n')
			accountRows := maxInt(1, (height-8)/3)
			for index, item := range current.accounts {
				if index >= accountRows {
					break
				}
				marker := " "
				if index == current.selected {
					marker = ">"
				}
				if width < 12 {
					output.WriteString(clip(marker+" "+item.ID, width))
					output.WriteByte('\n')
					continue
				}
				rowWidth := width - 2
				idWidth := minInt(16, rowWidth/2)
				stateWidth := maxInt(1, rowWidth-idWidth-1)
				output.WriteString(marker + " ")
				output.WriteString(pad(clip(item.ID, idWidth), idWidth))
				output.WriteByte(' ')
				output.WriteString(clip(stateLabel(item.State), stateWidth))
				output.WriteByte('\n')
				output.WriteString("  ")
				output.WriteString(clip(item.DisplayName, width-2))
				output.WriteByte('\n')
				output.WriteString("  ")
				output.WriteString(clip(enabledLabel(item.Enabled)+" "+resultLabel(item.LastResult)+" "+nextAttemptLabel(item.NextAttemptAt), width-2))
				output.WriteByte('\n')
			}
		} else {
			output.WriteString(pad("", 2))
			output.WriteString(pad("ID", 18))
			output.WriteString(pad("名称", 16))
			output.WriteString(pad("状态", 16))
			output.WriteString(pad("结果", 12))
			output.WriteString(pad("下次", 10))
			output.WriteString("启用\n")
			accountRows := maxInt(1, height-8)
			for index, item := range current.accounts {
				if index >= accountRows {
					break
				}
				marker := " "
				if index == current.selected {
					marker = ">"
				}
				output.WriteString(marker + " ")
				output.WriteString(pad(item.ID, 18))
				output.WriteString(pad(item.DisplayName, 16))
				output.WriteString(pad(stateLabel(item.State), 16))
				output.WriteString(pad(resultLabel(item.LastResult), 12))
				output.WriteString(pad(nextAttemptLabel(item.NextAttemptAt), 10))
				output.WriteString(enabledLabel(item.Enabled))
				output.WriteByte('\n')
			}
		}
	}

	output.WriteByte('\n')
	output.WriteString(current.renderLineStatus(width))

	if current.showHelp {
		output.WriteByte('\n')
		output.WriteString(clip("操作", width))
		output.WriteByte('\n')
		for _, line := range []string{
			"q 退出",
			"j/k 选择账号",
			"a 添加账号（只填用户名和密码）",
			"c 配置当前账号",
			"Enter 立即检查",
			"e 启用",
			"d 停用",
			"D/Delete 删除当前账号（需确认）",
			"l 重载配置",
			"Space 暂停/继续日志",
			"x 清空日志视图",
			"f 筛选当前账号",
			"r 刷新",
			"? 返回",
		} {
			output.WriteString(clip(line, width))
			output.WriteByte('\n')
		}
	} else {
		output.WriteByte('\n')
		heading := "近期事件"
		if current.filterID != "" {
			heading += " " + current.filterID
		}
		if current.paused {
			heading += " 已暂停"
		}
		output.WriteString(clip(heading, width))
		output.WriteByte('\n')
		visibleEvents := current.filteredEvents()
		logRows := maxInt(0, height-outputLineCount(output.String())-1)
		if logRows > maxVisibleLogs {
			logRows = maxVisibleLogs
		}
		start := maxInt(0, len(visibleEvents)-logRows)
		for _, event := range visibleEvents[start:] {
			output.WriteString(renderEvent(event, width))
			output.WriteByte('\n')
		}
	}
	output.WriteByte('\n')
	if current.confirmDelete {
		output.WriteString(clip("删除确认: "+current.deleteID+"  y 确认 / n 取消", width))
	} else {
		output.WriteString(clip("↑↓选择  a添加  c编辑  Enter检测  e启用  d停用  D删除  r刷新  ?帮助  q退出", width))
	}
	return strings.TrimSuffix(output.String(), "\n")
}

func (current model) summaryLine() string {
	enabled := 0
	authenticated := 0
	attention := 0
	for _, item := range current.accounts {
		if item.Enabled {
			enabled++
		}
		if item.State == account.StateAuthenticated {
			authenticated++
		}
		switch item.State {
		case account.StateOffline, account.StatePortal, account.StateBackoff, account.StateError:
			attention++
		}
	}
	return fmt.Sprintf("账号 %d  启用 %d  已认证 %d  待处理 %d  线路 %d", len(current.accounts), enabled, authenticated, attention, len(current.interfaces))
}

func (current model) renderLineStatus(width int) string {
	var output strings.Builder
	output.WriteString(clip("账号与线路", width))
	output.WriteByte('\n')
	if len(current.accounts) == 0 {
		output.WriteString(clip("暂无账号，按 a 添加账号", width))
	} else {
		for index, item := range current.accounts {
			marker := " "
			if index == current.selected {
				marker = ">"
			}
			accountConfig, exists := current.accountConfig(item.ID)
			interfaceName := "未分配"
			username := "-"
			if exists {
				if accountConfig.NetworkInterface != "" {
					interfaceName = accountConfig.NetworkInterface
				}
				if accountConfig.Username != "" {
					username = accountConfig.Username
				}
			}
			lineStatus := current.interfaceStatus(interfaceName)
			displayName := item.DisplayName
			if displayName == "" {
				displayName = item.ID
			}
			line := fmt.Sprintf("%s [%s] -> [%s] -> [%s] -> [%s]", marker, displayName, interfaceName, interfaceLinkLabel(lineStatus), stateLabel(item.State))
			output.WriteString(clip(line, width))
			output.WriteByte('\n')
			detail := fmt.Sprintf("  用户: %s  IPv4: %s  %s  结果: %s  重试: %d  下次: %s  上次成功: %s", username, interfaceIPv4Label(lineStatus), enabledLabel(item.Enabled), resultLabel(item.LastResult), item.RetryCount, nextAttemptLabel(item.NextAttemptAt), successLabel(item.LastSuccessAt))
			output.WriteString(clip(detail, width))
			if item.LastError != "" {
				output.WriteByte('\n')
				output.WriteString(clip("  原因: "+item.LastError, width))
			}
			if index+1 < len(current.accounts) {
				output.WriteByte('\n')
			}
		}
	}

	output.WriteByte('\n')
	output.WriteString(clip("线路状态", width))
	output.WriteByte('\n')
	if len(current.interfaces) == 0 {
		output.WriteString(clip("暂无 WAN 接口状态", width))
	} else {
		for index, status := range current.interfaces {
			line := fmt.Sprintf("  %s | %s | 管理%s | 载波%s | 运行%s | IPv4 %s", status.Name, interfaceLinkLabel(&status), interfaceAdminLabel(status), interfaceCarrierLabel(status), interfaceOperStateLabel(status), interfaceIPv4Label(&status))
			output.WriteString(clip(line, width))
			if index+1 < len(current.interfaces) {
				output.WriteByte('\n')
			}
		}
	}
	return output.String()
}

func (current model) accountConfig(id string) (daemon.AccountConfigView, bool) {
	for _, accountConfig := range current.configuration.Accounts {
		if accountConfig.ID == id {
			return accountConfig, true
		}
	}
	return daemon.AccountConfigView{}, false
}

func (current model) interfaceStatus(name string) *daemon.InterfaceStatus {
	for index := range current.interfaces {
		if current.interfaces[index].Name == name {
			return &current.interfaces[index]
		}
	}
	return nil
}

func interfaceLinkLabel(status *daemon.InterfaceStatus) string {
	if status == nil {
		return "未分配"
	}
	if !status.Present {
		return "接口不存在"
	}
	if !status.AdminUp {
		return "已关闭"
	}
	if !status.Carrier && status.OperState != "unknown" {
		return "无链路"
	}
	if len(status.IPv4Addresses) == 0 {
		return "等待地址"
	}
	return "链路正常"
}

func interfaceIPv4Label(status *daemon.InterfaceStatus) string {
	if status == nil || len(status.IPv4Addresses) == 0 {
		return "-"
	}
	return strings.Join(status.IPv4Addresses, ",")
}

func successLabel(value *time.Time) string {
	if value == nil || value.IsZero() {
		return "-"
	}
	return value.Local().Format("15:04:05")
}

func interfaceAdminLabel(status daemon.InterfaceStatus) string {
	if status.AdminUp {
		return "开启"
	}
	return "关闭"
}

func interfaceCarrierLabel(status daemon.InterfaceStatus) string {
	if status.Carrier {
		return "正常"
	}
	return "无"
}

func interfaceOperStateLabel(status daemon.InterfaceStatus) string {
	if status.OperState == "" {
		return "未知"
	}
	switch status.OperState {
	case "up":
		return "在线"
	case "down":
		return "离线"
	case "unknown":
		return "未知"
	case "not_found":
		return "不存在"
	case "lowerlayerdown":
		return "下层无链路"
	case "dormant":
		return "休眠"
	default:
		return status.OperState
	}
}

func (current model) filteredEvents() []eventlog.Event {
	if current.filterID == "" {
		return current.events
	}
	filtered := make([]eventlog.Event, 0, len(current.events))
	for _, event := range current.events {
		if event.AccountID == current.filterID {
			filtered = append(filtered, event)
		}
	}
	return filtered
}

func (current model) refresh() tea.Cmd {
	return tea.Batch(fetchStatus(current.ctx, current.address), fetchConfiguration(current.ctx, current.address), fetchLogs(current.ctx, current.address), tick())
}

func (current *model) beginDeleteConfirmation() {
	if current.selected < 0 || current.selected >= len(current.accounts) {
		current.err = "没有可删除的账号"
		return
	}
	current.confirmDelete = true
	current.deleteID = current.accounts[current.selected].ID
}

func (current model) deleteSelected() tea.Cmd {
	if current.deleteID == "" {
		return nil
	}
	return deleteAccount(current.ctx, current.address, current.deleteID)
}

func (current model) triggerSelected() tea.Cmd {
	if current.selected < 0 || current.selected >= len(current.accounts) {
		return nil
	}
	id := current.accounts[current.selected].ID
	return callAction(current.ctx, current.address, daemon.MethodAccountTrigger, daemon.AccountRequest{ID: id})
}

func (current model) setSelectedEnabled(enabled bool) tea.Cmd {
	if current.selected < 0 || current.selected >= len(current.accounts) {
		return nil
	}
	id := current.accounts[current.selected].ID
	method := daemon.MethodAccountDisable
	if enabled {
		method = daemon.MethodAccountEnable
	}
	return callAction(current.ctx, current.address, method, daemon.AccountRequest{ID: id})
}

type deleteMessage struct {
	err error
}

func deleteAccount(ctx context.Context, address, id string) tea.Cmd {
	return func() tea.Msg {
		requestContext, cancel := commandContext(ctx)
		defer cancel()
		err := control.Call(requestContext, address, daemon.MethodAccountDelete, daemon.AccountRequest{ID: id}, nil)
		return deleteMessage{err: err}
	}
}

func fetchStatus(ctx context.Context, address string) tea.Cmd {
	return func() tea.Msg {
		requestContext, cancel := commandContext(ctx)
		defer cancel()
		var result daemon.StatusResponse
		err := control.Call(requestContext, address, daemon.MethodStatus, struct{}{}, &result)
		return statusMessage{result: result, err: err}
	}
}

func fetchLogs(ctx context.Context, address string) tea.Cmd {
	return func() tea.Msg {
		requestContext, cancel := commandContext(ctx)
		defer cancel()
		var result []eventlog.Event
		err := control.Call(requestContext, address, daemon.MethodRecentLogs, daemon.LogsRequest{Limit: maxVisibleLogs}, &result)
		return logsMessage{events: result, err: err}
	}
}

func callAction(ctx context.Context, address, method string, params interface{}) tea.Cmd {
	return func() tea.Msg {
		requestContext, cancel := commandContext(ctx)
		defer cancel()
		err := control.Call(requestContext, address, method, params, nil)
		return actionMessage{err: err}
	}
}

func commandContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(parent, 5*time.Second)
}

func tick() tea.Cmd {
	return tea.Tick(refreshInterval, func(time.Time) tea.Msg { return refreshMessage{} })
}

func clip(value string, width int) string {
	if width <= 0 {
		return ""
	}
	if !utf8.ValidString(value) {
		value = strings.ToValidUTF8(value, "")
	}
	if runewidth.StringWidth(value) <= width {
		return value
	}
	if width <= 3 {
		return truncateWidth(value, width)
	}
	return truncateWidth(value, width-3) + "..."
}

func truncateWidth(value string, width int) string {
	var output strings.Builder
	currentWidth := 0
	for _, character := range value {
		characterWidth := runewidth.RuneWidth(character)
		if currentWidth+characterWidth > width {
			break
		}
		output.WriteRune(character)
		currentWidth += characterWidth
	}
	return output.String()
}

func pad(value string, width int) string {
	value = clip(value, width)
	padding := width - runewidth.StringWidth(value)
	if padding < 0 {
		padding = 0
	}
	return value + strings.Repeat(" ", padding)
}

func renderEvent(event eventlog.Event, width int) string {
	prefix := strings.TrimSpace(event.AccountID + " " + stateLabel(account.State(event.State)))
	if width <= 0 {
		return ""
	}
	if runewidth.StringWidth(prefix) >= width {
		return clip(prefix, width)
	}
	message := event.Message
	if message == "" {
		message = event.Operation
	}
	available := width - runewidth.StringWidth(prefix) - 1
	return prefix + " " + clip(message, available)
}

func stateLabel(state account.State) string {
	switch state {
	case account.StateDisabled:
		return "已停用"
	case account.StateIdle:
		return "等待检查"
	case account.StateChecking:
		return "检查中"
	case account.StateOffline:
		return "网络不可达"
	case account.StatePortal:
		return "发现 Portal"
	case account.StateAuthenticating:
		return "认证中"
	case account.StateAuthenticated:
		return "已认证"
	case account.StateBackoff:
		return "等待重试"
	case account.StateError:
		return "需要处理"
	default:
		return string(state)
	}
}

func resultLabel(status connectivity.Status) string {
	switch status {
	case connectivity.StatusAuthenticated:
		return "已联网"
	case connectivity.StatusPortal:
		return "Portal"
	case connectivity.StatusOffline:
		return "不可达"
	default:
		return "-"
	}
}

func enabledLabel(enabled bool) string {
	if enabled {
		return "启用"
	}
	return "停用"
}

func nextAttemptLabel(value *time.Time) string {
	if value == nil || value.IsZero() {
		return "-"
	}
	return value.Local().Format("15:04:05")
}

func outputLineCount(value string) int {
	if value == "" {
		return 0
	}
	return strings.Count(value, "\n") + 1
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

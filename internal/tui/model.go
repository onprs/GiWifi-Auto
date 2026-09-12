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
	refreshInterval = time.Second
	maxVisibleLogs  = 5
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
	hoverAction   string
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
		tea.WithMouseCellMotion(),
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
	case tea.MouseMsg:
		return current.updateMouse(message)
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

	lines := make([]string, 0, height+8)
	add := func(value string) {
		lines = append(lines, value)
	}

	add(styledLine(titleStyle, "GiWifi-Auto  多线路认证面板", width))
	add(styledLine(subtitleStyle, "实时监控账号认证与网络线路", width))
	add(styledLine(infoStyle, current.summaryLine(), width))
	if current.confirmDelete {
		add(styledLine(badStyle, "确认删除账号 "+current.deleteID+"？按 y 确认，按 n 或 Esc 取消", width))
	}
	if current.loading {
		add(styledLine(infoStyle, "正在加载状态...", width))
	} else if current.err != "" {
		add(styledLine(badStyle, current.err, width))
	}

	add("")
	add(sectionHeading("账号与线路", width))
	if len(current.accounts) == 0 {
		add(styledLine(mutedStyle, "暂无账号，点击底部新增按钮添加账号", width))
	} else {
		accountLimit := len(current.accounts)
		if height < 30 {
			accountLimit = minInt(accountLimit, maxInt(1, (height-12-len(current.interfaces))/2))
		}
		for index, item := range current.accounts[:accountLimit] {
			selected := index == current.selected
			add(current.renderAccountRoute(item, width, selected))
			add(current.renderAccountDetail(item, width, selected))
		}
		if accountLimit < len(current.accounts) {
			add(styledLine(mutedStyle, fmt.Sprintf("还有 %d 个账号未显示", len(current.accounts)-accountLimit), width))
		}
	}

	add("")
	add(sectionHeading("线路状态", width))
	if len(current.interfaces) == 0 {
		add(styledLine(mutedStyle, "暂无 WAN 接口状态", width))
	} else {
		if width >= 80 {
			add(styledLine(mutedStyle, renderInterfaceHeader(), width))
		}
		interfaceLimit := len(current.interfaces)
		if height < 30 {
			interfaceLimit = minInt(interfaceLimit, maxInt(1, height-10-2*minInt(len(current.accounts), 4)))
		}
		for index, status := range current.interfaces[:interfaceLimit] {
			add(renderInterfaceLine(status, index, width))
		}
		if interfaceLimit < len(current.interfaces) {
			add(styledLine(mutedStyle, fmt.Sprintf("还有 %d 条线路未显示", len(current.interfaces)-interfaceLimit), width))
		}
	}

	if current.showHelp {
		add("")
		add(sectionHeading("操作", width))
		for _, line := range []string{
			"点击账号行选择账号",
			"点击底部按钮执行操作",
			"滚轮切换账号",
			"点击帮助按钮返回面板",
		} {
			add(styledLine(mutedStyle, line, width))
		}
	} else {
		add("")
		heading := "近期事件"
		if current.filterID != "" {
			heading += " · " + current.filterID
		}
		if current.paused {
			heading += " · 已暂停"
		}
		add(sectionHeading(heading, width))
		visibleEvents := current.filteredEvents()
		eventLimit := minInt(maxVisibleLogs, maxInt(0, height-outputLineCount(strings.Join(lines, "\n"))-2))
		start := maxInt(0, len(visibleEvents)-eventLimit)
		for _, event := range visibleEvents[start:] {
			add(renderEventLine(event, width))
		}
	}

	add("")
	add(renderMouseButtons(mainMouseButtons(current.confirmDelete), width, current.hoverAction))
	return strings.Join(fitViewLines(lines, height), "\n")
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

func sectionHeading(value string, width int) string {
	prefix := "── " + value + " "
	lineWidth := minInt(width, 80)
	remaining := maxInt(0, lineWidth-runewidth.StringWidth(prefix))
	return styledLine(sectionStyle, prefix+strings.Repeat("─", remaining), width)
}

func (current model) renderAccountRoute(item account.Snapshot, width int, selected bool) string {
	accountConfig, exists := current.accountConfig(item.ID)
	interfaceName := "未分配"
	if exists && accountConfig.NetworkInterface != "" {
		interfaceName = accountConfig.NetworkInterface
	}
	lineStatus := current.interfaceStatus(interfaceName)
	displayName := item.DisplayName
	if displayName == "" {
		displayName = item.ID
	}

	var route string
	if width >= 80 {
		route = routeToken(displayName, 18) + " -> " +
			routeToken(interfaceName, 16) + " -> " +
			routeToken(interfaceLinkLabel(lineStatus), 16) + " -> " +
			routeToken(stateLabel(item.State), 16)
	} else {
		route = fmt.Sprintf("[%s] -> [%s] -> [%s] -> [%s]", displayName, interfaceName, interfaceLinkLabel(lineStatus), stateLabel(item.State))
	}
	marker := "  "
	if selected {
		marker = "▶ "
	}
	available := maxInt(0, width-runewidth.StringWidth(marker))
	line := marker + clip(route, available)
	line = pad(line, width)
	return accountRouteStyle(item.State, selected).Render(line)
}

func routeToken(value string, width int) string {
	if width < 2 {
		return clip(value, width)
	}
	token := "[" + clip(value, width-2) + "]"
	return pad(token, width)
}

func (current model) renderAccountDetail(item account.Snapshot, width int, selected bool) string {
	accountConfig, exists := current.accountConfig(item.ID)
	username := "-"
	interfaceName := "未分配"
	if exists {
		if accountConfig.Username != "" {
			username = accountConfig.Username
		}
		if accountConfig.NetworkInterface != "" {
			interfaceName = accountConfig.NetworkInterface
		}
	}
	lineStatus := current.interfaceStatus(interfaceName)
	var detail string
	if width >= 80 {
		detail =
			pad("用户 "+username, 17) + "│" +
				pad("IPv4 "+interfaceIPv4Label(lineStatus), 15) + "│" +
				pad(enabledLabel(item.Enabled), 6) + "│" +
				pad("结果 "+resultLabel(item.LastResult), 10) + "│" +
				pad("重试 "+fmt.Sprintf("%d", item.RetryCount), 8) + "│" +
				"下次 " + nextAttemptLabel(item.NextAttemptAt)
		if item.LastSuccessAt != nil {
			detail += "  成功 " + successLabel(item.LastSuccessAt)
		}
		if item.LastError != "" {
			detail += "  原因 " + item.LastError
		}
	} else {
		detail = "用户: " + username + "  IPv4: " + interfaceIPv4Label(lineStatus) +
			"  " + enabledLabel(item.Enabled) + "  结果: " + resultLabel(item.LastResult) +
			"  重试: " + fmt.Sprintf("%d", item.RetryCount) +
			"  下次: " + nextAttemptLabel(item.NextAttemptAt) +
			"  上次成功: " + successLabel(item.LastSuccessAt)
		if item.LastError != "" {
			detail += "  原因: " + item.LastError
		}
	}
	line := "  " + clip(detail, maxInt(0, width-2))
	if selected {
		return selectedDetailStyle.Render(pad(line, width))
	}
	return mutedStyle.Render(line)
}

func renderInterfaceHeader() string {
	return "  " + pad("线路", 14) + "│" + pad("链路", 12) + "│" + pad("管理", 8) + "│" + pad("载波", 8) + "│" + pad("运行", 10) + "│IPv4"
}

func renderInterfaceLine(status daemon.InterfaceStatus, index, width int) string {
	var line string
	if width >= 80 {
		line = "  " +
			pad(fmt.Sprintf("%d %s", index+1, status.Name), 14) + "│" +
			pad(interfaceLinkLabel(&status), 12) + "│" +
			pad(interfaceAdminLabel(status), 8) + "│" +
			pad(interfaceCarrierLabel(status), 8) + "│" +
			pad(interfaceOperStateLabel(status), 10) + "│" +
			"IPv4 " + interfaceIPv4Label(&status)
	} else {
		line = fmt.Sprintf("%d. %s -> %s | 管理%s | 载波%s | 运行%s | IPv4 %s", index+1, status.Name, interfaceLinkLabel(&status), interfaceAdminLabel(status), interfaceCarrierLabel(status), interfaceOperStateLabel(status), interfaceIPv4Label(&status))
	}
	return interfaceStateStyle(status).Render(clip(line, width))
}

func renderEventLine(event eventlog.Event, width int) string {
	return accountStateStyle(account.State(event.State)).Render(clip(renderEvent(event, width), width))
}

func fitViewLines(lines []string, height int) []string {
	if height <= 0 || len(lines) <= height {
		return lines
	}
	if height == 1 {
		return []string{lines[len(lines)-1]}
	}
	result := append([]string(nil), lines[:height-2]...)
	result = append(result, styledLine(mutedStyle, "...", 3))
	result = append(result, lines[len(lines)-1])
	return result
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

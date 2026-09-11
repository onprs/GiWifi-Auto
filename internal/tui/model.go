package tui

import (
	"context"
	"errors"
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

type refreshMessage struct{}

type model struct {
	ctx      context.Context
	address  string
	accounts []account.Snapshot
	events   []eventlog.Event
	selected int
	width    int
	height   int
	loading  bool
	err      string
	showHelp bool
	paused   bool
	filterID string
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
	return tea.Batch(fetchStatus(current.ctx, current.address), fetchLogs(current.ctx, current.address), tick())
}

func (current model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.KeyMsg:
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
		case "l":
			return current, callAction(current.ctx, current.address, daemon.MethodReload, struct{}{})
		case "e":
			return current, current.setSelectedEnabled(true)
		case "d":
			return current, current.setSelectedEnabled(false)
		case "r":
			current.loading = true
			return current, current.refresh()
		case "?":
			current.showHelp = !current.showHelp
		case " ":
			current.paused = !current.paused
		case "c":
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
	case statusMessage:
		current.loading = false
		if message.err != nil {
			current.err = "状态暂时不可用"
		} else {
			if current.err == "状态暂时不可用" {
				current.err = ""
			}
			current.accounts = message.result.Accounts
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
		return current, tea.Batch(fetchStatus(current.ctx, current.address), fetchLogs(current.ctx, current.address), tick())
	}
	return current, nil
}

func (current model) View() string {
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

	if current.showHelp {
		output.WriteByte('\n')
		output.WriteString(clip("操作", width))
		output.WriteByte('\n')
		for _, line := range []string{
			"q 退出",
			"j/k 选择账号",
			"Enter 立即检查",
			"e 启用",
			"d 停用",
			"l 重载配置",
			"Space 暂停/继续日志",
			"c 清空日志视图",
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
	return strings.TrimSuffix(output.String(), "\n")
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
	return tea.Batch(fetchStatus(current.ctx, current.address), fetchLogs(current.ctx, current.address), tick())
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

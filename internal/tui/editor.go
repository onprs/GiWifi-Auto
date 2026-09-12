package tui

import (
	"context"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/onprs/GiWifi-Auto/internal/control"
	"github.com/onprs/GiWifi-Auto/internal/daemon"
)

const (
	formUsername = iota
	formPassword
)

type formField struct {
	label    string
	value    string
	cursor   int
	secret   bool
	readonly bool
}

type accountForm struct {
	id            string
	fields        []formField
	active        int
	editing       bool
	hasCredential bool
	saving        bool
	err           string
}

func newAccountForm(_ daemon.ConfigResponse, existing *daemon.AccountConfigView) *accountForm {
	form := &accountForm{
		fields: []formField{
			{label: "用户名"},
			{label: "密码", secret: true},
		},
	}
	if existing != nil {
		form.editing = true
		form.id = existing.ID
		form.hasCredential = existing.HasCredential
		form.fields[formUsername].value = existing.Username
	}
	for index := range form.fields {
		form.fields[index].cursor = len([]rune(form.fields[index].value))
	}
	return form
}

func (current *model) openNewAccountForm() {
	current.form = newAccountForm(current.configuration, nil)
	current.err = ""
}

func (current *model) openSelectedAccountForm() {
	if current.selected < 0 || current.selected >= len(current.accounts) {
		current.err = "没有可配置的账号"
		return
	}
	id := current.accounts[current.selected].ID
	for index := range current.configuration.Accounts {
		if current.configuration.Accounts[index].ID == id {
			current.form = newAccountForm(current.configuration, &current.configuration.Accounts[index])
			current.err = ""
			return
		}
	}
	current.err = "账号配置暂时不可用"
}

func (current model) updateForm(message tea.KeyMsg) (tea.Model, tea.Cmd) {
	if current.form == nil {
		return current, nil
	}
	if message.String() == "ctrl+c" {
		return current, tea.Quit
	}
	if message.String() == "esc" {
		current.form = nil
		return current, nil
	}
	if current.form.saving {
		return current, nil
	}
	if current.form.handleKey(message) {
		if err := current.form.validate(); err != "" {
			current.form.err = err
			return current, nil
		}
		current.form.err = ""
		current.form.saving = true
		return current, saveAccount(current.ctx, current.address, current.form.request())
	}
	return current, nil
}

func (form *accountForm) handleKey(message tea.KeyMsg) bool {
	if len(form.fields) == 0 {
		return false
	}
	switch message.String() {
	case "tab", "down":
		form.nextField(1)
		return false
	case "shift+tab", "up":
		form.nextField(-1)
		return false
	case "enter", "ctrl+s":
		if message.String() == "enter" && form.active < len(form.fields)-1 {
			form.nextField(1)
			return false
		}
		return true
	case "left":
		if form.fields[form.active].cursor > 0 {
			form.fields[form.active].cursor--
		}
		return false
	case "right":
		if form.fields[form.active].cursor < len([]rune(form.fields[form.active].value)) {
			form.fields[form.active].cursor++
		}
		return false
	case "home":
		form.fields[form.active].cursor = 0
		return false
	case "end":
		form.fields[form.active].cursor = len([]rune(form.fields[form.active].value))
		return false
	case "backspace":
		form.deleteBeforeCursor()
		return false
	case "delete":
		form.deleteAtCursor()
		return false
	}

	field := &form.fields[form.active]
	if field.readonly {
		return false
	}
	if len(message.Runes) == 0 {
		return false
	}
	var runes []rune
	for _, character := range message.Runes {
		if !unicode.IsControl(character) {
			runes = append(runes, character)
		}
	}
	if len(runes) == 0 {
		return false
	}
	value := []rune(field.value)
	updated := make([]rune, 0, len(value)+len(runes))
	updated = append(updated, value[:field.cursor]...)
	updated = append(updated, runes...)
	updated = append(updated, value[field.cursor:]...)
	field.value = string(updated)
	field.cursor += len(runes)
	return false
}

func (form *accountForm) nextField(direction int) {
	form.active += direction
	if form.active < 0 {
		form.active = len(form.fields) - 1
	}
	if form.active >= len(form.fields) {
		form.active = 0
	}
}

func (form *accountForm) deleteBeforeCursor() {
	field := &form.fields[form.active]
	if field.readonly || field.cursor <= 0 {
		return
	}
	value := []rune(field.value)
	value = append(value[:field.cursor-1], value[field.cursor:]...)
	field.value = string(value)
	field.cursor--
}

func (form *accountForm) deleteAtCursor() {
	field := &form.fields[form.active]
	if field.readonly {
		return
	}
	value := []rune(field.value)
	if field.cursor >= len(value) {
		return
	}
	value = append(value[:field.cursor], value[field.cursor+1:]...)
	field.value = string(value)
}

func (form *accountForm) validate() string {
	if strings.TrimSpace(form.value(formUsername)) == "" {
		return "用户名不能为空"
	}
	if !form.editing && form.value(formPassword) == "" {
		return "新账号必须填写密码"
	}
	if form.editing && !form.hasCredential && form.value(formPassword) == "" {
		return "请填写账号密码"
	}
	return ""
}

func (form *accountForm) request() daemon.AccountConfigureRequest {
	return daemon.AccountConfigureRequest{
		ID:       form.id,
		Username: form.value(formUsername),
		Password: form.value(formPassword),
		Enabled:  true,
	}
}

func (form *accountForm) value(index int) string {
	if index < 0 || index >= len(form.fields) {
		return ""
	}
	return form.fields[index].value
}

func (form *accountForm) formValue(index int, active bool) string {
	field := form.fields[index]
	value := field.value
	if field.secret {
		value = strings.Repeat("*", len([]rune(value)))
	}
	if !active {
		return value
	}
	runes := []rune(value)
	cursor := field.cursor
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(runes) {
		cursor = len(runes)
	}
	return string(runes[:cursor]) + "|" + string(runes[cursor:])
}

func (form *accountForm) formFieldLine(index, width int) string {
	field := form.fields[index]
	line := field.label + ": " + form.formValue(index, index == form.active)
	line = pad(clip(line, width), width)
	if index == form.active {
		return selectedStyle.Render(line)
	}
	return fieldStyle.Render(line)
}

func (current model) formView() string {
	width := current.width
	if width <= 0 {
		width = 80
	}
	var output strings.Builder
	if current.form.editing {
		output.WriteString(styledLine(titleStyle, "编辑账号", width))
	} else {
		output.WriteString(styledLine(titleStyle, "添加账号", width))
	}
	output.WriteByte('\n')
	output.WriteString(styledLine(separatorStyle, strings.Repeat("─", minInt(width, 80)), width))
	output.WriteByte('\n')
	if current.form.saving {
		output.WriteString(styledLine(infoStyle, "正在保存...", width))
		output.WriteByte('\n')
	}
	if current.form.err != "" {
		output.WriteString(styledLine(badStyle, current.form.err, width))
		output.WriteByte('\n')
	}
	for index := range current.form.fields {
		output.WriteString(current.form.formFieldLine(index, width))
		output.WriteByte('\n')
	}
	output.WriteByte('\n')
	output.WriteString(styledLine(mutedStyle, "点击字段后输入用户名和密码", width))
	output.WriteByte('\n')
	output.WriteString(renderMouseButtons(formMouseButtons(), width, ""))
	return strings.TrimSuffix(output.String(), "\n")
}

func fetchConfiguration(ctx context.Context, address string) tea.Cmd {
	return func() tea.Msg {
		requestContext, cancel := commandContext(ctx)
		defer cancel()
		var result daemon.ConfigResponse
		err := control.Call(requestContext, address, daemon.MethodConfig, struct{}{}, &result)
		return configMessage{result: result, err: err}
	}
}

func saveAccount(ctx context.Context, address string, request daemon.AccountConfigureRequest) tea.Cmd {
	return func() tea.Msg {
		if ctx == nil {
			ctx = context.Background()
		}
		requestContext, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		err := control.Call(requestContext, address, daemon.MethodAccountConfigure, request, nil)
		return configureMessage{err: err}
	}
}

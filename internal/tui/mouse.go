package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"
)

type mouseButton struct {
	ID    string
	Label string
}

type mouseButtonRect struct {
	ID string
	X  int
	Y  int
	W  int
}

func mainMouseButtons(confirm bool) []mouseButton {
	if confirm {
		return []mouseButton{{ID: "delete-confirm", Label: "确认删除"}, {ID: "delete-cancel", Label: "取消"}}
	}
	return []mouseButton{
		{ID: "add", Label: "新增"},
		{ID: "edit", Label: "编辑"},
		{ID: "trigger", Label: "检测"},
		{ID: "enable", Label: "启用"},
		{ID: "disable", Label: "停用"},
		{ID: "delete", Label: "删除"},
		{ID: "refresh", Label: "刷新"},
		{ID: "help", Label: "帮助"},
		{ID: "quit", Label: "退出"},
	}
}

func formMouseButtons() []mouseButton {
	return []mouseButton{{ID: "form-save", Label: "保存"}, {ID: "form-cancel", Label: "取消"}}
}

func buttonText(button mouseButton) string {
	return "[" + button.Label + "]"
}

func buttonWidth(button mouseButton) int {
	return runewidth.StringWidth(buttonText(button))
}

func mouseButtonRects(buttons []mouseButton, width, y int) []mouseButtonRect {
	result := make([]mouseButtonRect, 0, len(buttons))
	x := 0
	for _, button := range buttons {
		buttonWidth := buttonWidth(button)
		if x+buttonWidth > width {
			break
		}
		result = append(result, mouseButtonRect{ID: button.ID, X: x, Y: y, W: buttonWidth})
		x += buttonWidth + 2
	}
	return result
}

func renderMouseButtons(buttons []mouseButton, width int, hover string) string {
	var output strings.Builder
	x := 0
	for _, button := range buttons {
		value := buttonText(button)
		buttonWidth := runewidth.StringWidth(value)
		gap := 0
		if output.Len() > 0 {
			gap = 2
		}
		if x+gap+buttonWidth > width {
			break
		}
		if gap > 0 {
			output.WriteString("  ")
			x += gap
		}
		style := buttonStyle
		if button.ID == "delete" || button.ID == "delete-confirm" {
			style = dangerButtonStyle
		}
		if button.ID == hover {
			style = buttonHoverStyle
		}
		output.WriteString(style.Render(value))
		x += buttonWidth
	}
	return output.String()
}

func (current model) mainButtonAt(x, y int) string {
	buttons := mainMouseButtons(current.confirmDelete)
	viewLines := outputLineCount(current.View())
	actionY := viewLines - 1
	if y != actionY {
		return ""
	}
	for _, rect := range mouseButtonRects(buttons, current.viewWidth(), actionY) {
		if x >= rect.X && x < rect.X+rect.W {
			return rect.ID
		}
	}
	return ""
}

func (current model) viewWidth() int {
	if current.width > 0 {
		return current.width
	}
	return 80
}

func (current model) accountAtMouseY(y int) int {
	if y < 0 || y > current.viewHeight()-3 {
		return -1
	}
	line := 3
	if current.confirmDelete {
		line++
	}
	if current.loading || current.err != "" {
		line++
	}
	line += 2
	if len(current.accounts) == 0 {
		return -1
	}
	accountLimit := len(current.accounts)
	if current.viewHeight() < 30 {
		accountLimit = minInt(accountLimit, maxInt(1, (current.viewHeight()-12-len(current.interfaces))/2))
	}
	for index := 0; index < accountLimit; index++ {
		if y == line || y == line+1 {
			return index
		}
		line += 2
	}
	return -1
}

func (current model) viewHeight() int {
	if current.height > 0 {
		return current.height
	}
	return 24
}

func (current model) updateMouse(message tea.MouseMsg) (tea.Model, tea.Cmd) {
	if message.Action == tea.MouseActionMotion {
		if current.form != nil {
			return current, nil
		}
		current.hoverAction = current.mainButtonAt(message.X, message.Y)
		return current, nil
	}
	if message.Button == tea.MouseButtonWheelUp || message.Button == tea.MouseButtonWheelDown {
		if current.form != nil || len(current.accounts) == 0 {
			return current, nil
		}
		if message.Button == tea.MouseButtonWheelUp && current.selected > 0 {
			current.selected--
		}
		if message.Button == tea.MouseButtonWheelDown && current.selected+1 < len(current.accounts) {
			current.selected++
		}
		return current, nil
	}
	if message.Action != tea.MouseActionPress || message.Button != tea.MouseButtonLeft {
		return current, nil
	}
	if current.form != nil {
		return current.updateFormMouse(message)
	}
	if current.confirmDelete {
		switch current.mainButtonAt(message.X, message.Y) {
		case "delete-confirm":
			current.confirmDelete = false
			return current, current.deleteSelected()
		case "delete-cancel":
			current.confirmDelete = false
			current.deleteID = ""
		}
		return current, nil
	}
	if selected := current.accountAtMouseY(message.Y); selected >= 0 {
		current.selected = selected
		return current, nil
	}
	switch current.mainButtonAt(message.X, message.Y) {
	case "add":
		current.openNewAccountForm()
	case "edit":
		current.openSelectedAccountForm()
	case "trigger":
		return current, current.triggerSelected()
	case "enable":
		return current, current.setSelectedEnabled(true)
	case "disable":
		return current, current.setSelectedEnabled(false)
	case "delete":
		current.beginDeleteConfirmation()
	case "refresh":
		current.loading = true
		return current, current.refresh()
	case "help":
		current.showHelp = !current.showHelp
	case "quit":
		return current, tea.Quit
	}
	return current, nil
}

func (current model) updateFormMouse(message tea.MouseMsg) (tea.Model, tea.Cmd) {
	if current.form == nil || message.Action != tea.MouseActionPress || message.Button != tea.MouseButtonLeft {
		return current, nil
	}
	width := current.viewWidth()
	buttons := formMouseButtons()
	buttonY := outputLineCount(current.formView()) - 1
	for _, rect := range mouseButtonRects(buttons, width, buttonY) {
		if message.Y == rect.Y && message.X >= rect.X && message.X < rect.X+rect.W {
			switch rect.ID {
			case "form-save":
				if err := current.form.validate(); err != "" {
					current.form.err = err
					return current, nil
				}
				current.form.err = ""
				current.form.saving = true
				return current, saveAccount(current.ctx, current.address, current.form.request())
			case "form-cancel":
				current.form = nil
				return current, nil
			}
		}
	}

	fieldStart := 2
	if current.form.saving {
		fieldStart++
	}
	if current.form.err != "" {
		fieldStart++
	}
	if message.Y >= fieldStart && message.Y < fieldStart+len(current.form.fields) {
		current.form.active = message.Y - fieldStart
	}
	return current, nil
}

package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

type mouseAction int

const (
	mouseSwitchScreen mouseAction = iota + 1
	mouseGenFocus
	mouseGenSubmit
	mouseGenPreview
	mouseCfgFocus
	mouseCfgKeyTier
	mouseCfgSave
	mouseCfgReload
	mouseCfgAddOpen
	mouseCfgAddTier
	mouseCfgAddSubmit
	mouseCfgAddCancel
)

type mouseRegion struct {
	x, y, w, h int
	action     mouseAction
	screen     string
	index      int
}

func (r mouseRegion) contains(x, y int) bool {
	return x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h
}

func (m model) mouseRegions() []mouseRegion {
	var regions []mouseRegion
	contentX, contentY := 0, 1
	if m.width >= 76 {
		contentX = 23
		regions = append(regions,
			mouseRegion{x: 0, y: 3, w: 20, h: 1, action: mouseSwitchScreen, screen: "generate"},
			mouseRegion{x: 0, y: 4, w: 20, h: 1, action: mouseSwitchScreen, screen: "usage"},
			mouseRegion{x: 0, y: 5, w: 20, h: 1, action: mouseSwitchScreen, screen: "config"},
		)
	} else {
		regions = append(regions,
			mouseRegion{x: 35, y: 0, w: 11, h: 1, action: mouseSwitchScreen, screen: "generate"},
			mouseRegion{x: 49, y: 0, w: 8, h: 1, action: mouseSwitchScreen, screen: "usage"},
			mouseRegion{x: 60, y: 0, w: 9, h: 1, action: mouseSwitchScreen, screen: "config"},
		)
	}
	switch m.screen {
	case "config":
		_, cfgRegions := m.configLayoutLines(contentX, contentY)
		regions = append(regions, cfgRegions...)
	case "generate":
		_, genRegions := m.generateLayoutLines(contentX, contentY)
		regions = append(regions, genRegions...)
	}
	return regions
}

func (m model) switchScreen(target string) (tea.Model, tea.Cmd) {
	switch target {
	case "generate":
		m.screen = "generate"
		m.blurConfigFields()
		return m, m.setFocus(m.focus)
	case "usage":
		m.screen = "usage"
		m.blurGenerateFields()
		m.blurConfigFields()
		return m, m.refreshUsage()
	case "config":
		m.screen = "config"
		m.blurGenerateFields()
		focusCmd := m.setConfigFocus(m.cfgFocus)
		if !m.cfgLoaded || !m.configDirty() {
			return m, tea.Batch(focusCmd, m.refreshConfig())
		}
		return m, focusCmd
	}
	return m, nil
}

func (m model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Button == tea.MouseButtonWheelUp {
		switch m.screen {
		case "usage":
			m.usageOffset = max(0, m.usageOffset-1)
			return m, nil
		case "config":
			return m, m.setConfigFocus(m.cfgFocus - 1)
		default:
			return m, m.setFocus(m.focus - 1)
		}
	}
	if msg.Button == tea.MouseButtonWheelDown {
		switch m.screen {
		case "usage":
			m.usageOffset = min(m.usageOffset+1, m.maxUsageOffset())
			return m, nil
		case "config":
			return m, m.setConfigFocus(m.cfgFocus + 1)
		default:
			return m, m.setFocus(m.focus + 1)
		}
	}

	if msg.Action != tea.MouseActionPress {
		return m, nil
	}
	if msg.Button != tea.MouseButtonLeft && msg.Button != tea.MouseButtonRight {
		return m, nil
	}
	direction := "right"
	if msg.Button == tea.MouseButtonRight {
		direction = "left"
	}

	for _, r := range m.mouseRegions() {
		if !r.contains(msg.X, msg.Y) {
			continue
		}
		switch r.action {
		case mouseSwitchScreen:
			if msg.Button == tea.MouseButtonLeft {
				return m.switchScreen(r.screen)
			}
		case mouseGenFocus:
			wasFocused := m.focus == r.index
			cmd := m.setFocus(r.index)
			if wasFocused || msg.Button == tea.MouseButtonRight {
				m.cycle(direction)
			}
			return m, cmd
		case mouseGenSubmit:
			if msg.Button == tea.MouseButtonLeft {
				m.setFocus(len(m.fields) + 1)
				return m.submit()
			}
		case mouseGenPreview:
			if msg.Button == tea.MouseButtonLeft {
				m.setFocus(len(m.fields) + 2)
				return m.submitPreview(true)
			}
		case mouseCfgFocus:
			wasFocused := m.cfgFocus == r.index
			cmd := m.setConfigFocus(r.index)
			if wasFocused || msg.Button == tea.MouseButtonRight {
				// Only cycle YAML fields on re-click, not key name inputs (tier badge has its own region).
				if r.index < len(m.cfgFields) {
					m.cycleConfig(direction)
				}
			}
			return m, cmd
		case mouseCfgKeyTier:
			cmd := m.setConfigFocus(len(m.cfgFields) + r.index)
			m.cycleConfig(direction)
			return m, cmd
		case mouseCfgSave:
			if msg.Button == tea.MouseButtonLeft {
				m.setConfigFocus(m.cfgSaveFocus())
				return m.saveConfig()
			}
		case mouseCfgReload:
			if msg.Button == tea.MouseButtonLeft {
				m.setConfigFocus(m.cfgReloadFocus())
				return m, m.refreshConfig()
			}
		case mouseCfgAddOpen:
			if msg.Button == tea.MouseButtonLeft {
				return m.startAddKey()
			}
		case mouseCfgAddTier:
			cmd := m.setConfigFocus(m.cfgAddFormStart() + 1)
			m.cycleConfig(direction)
			return m, cmd
		case mouseCfgAddSubmit:
			if msg.Button == tea.MouseButtonLeft {
				m.setConfigFocus(m.cfgAddSubmitFocus())
				return m.submitAddKey()
			}
		case mouseCfgAddCancel:
			if msg.Button == tea.MouseButtonLeft {
				return m, m.cancelAddKey()
			}
		}
		return m, nil
	}
	return m, nil
}

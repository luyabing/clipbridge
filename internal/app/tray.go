package app

import (
	"fmt"
	"os"
	"runtime"
	"sync"

	"fyne.io/systray"

	"clipbridge/internal/assets"
)

type trayState struct {
	app *App

	mu       sync.Mutex
	ready    bool
	quitOnce sync.Once
	status   *systray.MenuItem
	pause    *systray.MenuItem
	auto     *systray.MenuItem
}

func (a *App) trayHandle() *trayState {
	a.trayMu.Lock()
	defer a.trayMu.Unlock()
	return a.tray
}

func (a *App) startTray() {
	t := &trayState{app: a}
	a.trayMu.Lock()
	a.tray = t
	a.trayMu.Unlock()
	go func() {
		defer func() {
			if r := recover(); r != nil {
				a.logf("托盘运行异常: %v", r)
			}
		}()
		systray.Run(t.onReady, t.onExit)
	}()
}

func (t *trayState) onReady() {
	a := t.app
	if runtime.GOOS == "windows" {
		systray.SetIcon(assets.IconICO)
	} else {
		systray.SetIcon(assets.IconPNG)
	}
	systray.SetTitle("剪贴桥")
	systray.SetTooltip("剪贴桥 · 局域网剪贴板")

	status := systray.AddMenuItem("正在启动…", "当前连接状态")
	status.Disable()
	systray.AddSeparator()
	openItem := systray.AddMenuItem("打开设置…", "在浏览器中打开控制面板")
	pause := systray.AddMenuItemCheckbox("暂停同步", "暂停发送和接收剪贴板", a.Config().Paused)
	on, _ := a.AutostartStatus()
	auto := systray.AddMenuItemCheckbox("开机自动启动", "登录后自动在托盘运行", on)
	openDL := systray.AddMenuItem("打开接收文件夹", "查看从其他设备收到的文件")
	systray.AddSeparator()
	about := systray.AddMenuItem("剪贴桥 v"+a.version, "Ubuntu ⇄ Windows 局域网剪贴板")
	about.Disable()
	systray.AddSeparator()
	quit := systray.AddMenuItem("退出剪贴桥", "停止同步并退出")

	t.mu.Lock()
	t.status, t.pause, t.auto, t.ready = status, pause, auto, true
	t.mu.Unlock()

	if runtime.GOOS == "windows" {
		systray.SetOnTapped(a.openSettings)
	}

	go func() {
		for {
			select {
			case <-a.ctx.Done():
				return
			case <-openItem.ClickedCh:
				a.openSettings()
			case <-pause.ClickedCh:
				cfg := a.Config()
				if err := a.SetPaused(!cfg.Paused); err != nil {
					a.events.Add("sys", "error", "切换暂停失败："+err.Error())
				}
				a.syncTrayState()
			case <-auto.ClickedCh:
				on, _ := a.AutostartStatus()
				if err := a.SetAutostart(!on); err != nil {
					a.events.Add("sys", "error", "设置开机自启失败："+err.Error())
				}
				a.syncTrayState()
			case <-openDL.ClickedCh:
				_ = os.MkdirAll(a.downloadRoot, 0o755)
				a.openPath(a.downloadRoot)
			case <-quit.ClickedCh:
				a.Shutdown()
				return
			}
		}
	}()

	a.syncTrayState()
	a.logf("托盘图标已启动")
}

func (t *trayState) onExit() {
	t.mu.Lock()
	t.ready = false
	t.mu.Unlock()
	t.app.debugf("托盘已退出")
}

func (a *App) syncTrayState() {
	t := a.trayHandle()
	if t == nil {
		return
	}
	t.mu.Lock()
	if !t.ready {
		t.mu.Unlock()
		return
	}
	status, pause, auto := t.status, t.pause, t.auto
	t.mu.Unlock()

	cfg := a.Config()
	count := 0
	if a.hub != nil {
		for _, p := range a.hub.Snapshot() {
			if p.Connected {
				count++
			}
		}
	}
	var text string
	switch {
	case cfg.Paused && count > 0:
		text = fmt.Sprintf("已暂停 · %d 台已连接", count)
	case cfg.Paused:
		text = "已暂停同步"
	case count > 0:
		text = fmt.Sprintf("已连接 %d 台设备", count)
	default:
		text = "等待连接"
	}
	if status != nil {
		status.SetTitle(text)
	}
	if pause != nil {
		if cfg.Paused {
			pause.Check()
		} else {
			pause.Uncheck()
		}
	}
	if auto != nil {
		on, _ := a.AutostartStatus()
		if on {
			auto.Check()
		} else {
			auto.Uncheck()
		}
	}
	systray.SetTooltip("剪贴桥 · " + text)
}

func (t *trayState) stop() {
	t.quitOnce.Do(func() {
		t.mu.Lock()
		t.ready = false
		t.mu.Unlock()
		systray.Quit()
	})
}

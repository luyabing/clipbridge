// Command clipbridge shares the clipboard between computers on the same LAN.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"clipbridge/internal/app"
	"clipbridge/internal/platform"
)

// version is replaced at build time with -ldflags "-X main.version=...".
var version = "1.0.0"

func main() {
	headless := flag.Bool("headless", false, "不创建托盘图标，只在后台运行")
	debug := flag.Bool("debug", false, "在日志中输出调试信息")
	configPath := flag.String("config", "", "指定配置文件路径（默认位于用户配置目录）")
	showVersion := flag.Bool("version", false, "显示版本号后退出")
	flag.Parse()

	if *showVersion {
		fmt.Printf("剪贴桥 ClipBridge %s\n", version)
		return
	}

	a, err := app.New(app.Options{
		ConfigPath: *configPath,
		Version:    version,
		Headless:   *headless,
		Debug:      *debug,
	})
	if err != nil {
		platform.Fatal("剪贴桥启动失败", err.Error())
		os.Exit(1)
	}

	if err := a.Start(); err != nil {
		if errors.Is(err, app.ErrAlreadyRunning) {
			// A previous instance owns the control panel. Bring it to the
			// front instead of starting a second copier of the clipboard.
			url := a.URL()
			a.Shutdown()
			if !*headless {
				if err := platform.OpenURL(url); err != nil {
					platform.Fatal("剪贴桥已在运行", "控制面板地址："+url)
					os.Exit(1)
				}
			}
			fmt.Println("剪贴桥已在运行：" + url)
			return
		}
		a.Shutdown()
		platform.Fatal("剪贴桥启动失败", err.Error())
		os.Exit(1)
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)

	select {
	case <-signals:
		a.Shutdown()
	case <-a.Done():
	}
}

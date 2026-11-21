# 剪贴桥 · ClipBridge

> Ubuntu ⇄ Windows 局域网剪贴板共享工具。  
> 自动发现、文本/图片/文件双向同步、配对码加密、托盘运行。

[![License: MIT](https://img.shields.io/badge/license-MIT-6256F3.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25%2B-00ADD8.svg)](go.mod)
[![Platform](https://img.shields.io/badge/platform-Ubuntu%20%7C%20Windows-191B24.svg)](#系统要求)

[English](README.en.md) · 简体中文

两台电脑在同一网段时自动发现，也可以手动填写 IP；支持文本、PNG 图片和文件传输，可最小化到托盘并开机自启。

整个程序是单个静态二进制，最终用户不需要安装 Go、GTK 或其它运行时。

## 功能

- **自动发现**：通过 UDP 广播寻找同一局域网内的设备。
- **手动连接**：自动发现失效时，填写 `IP` 或 `IP:端口` 即可。
- **双向同步**：文本、PNG 图片、文件列表。图片和文字会直接写入系统剪贴板。
- **文件接收**：收到的文件保存到 `下载/ClipBridge/时间戳/`，并尽量把文件列表写入剪贴板，方便直接粘贴。
- **配对码（推荐）**：两端设置相同配对码后，握手使用 HMAC 校验，传输使用 AES-256-GCM 加密；不设置时明文传输且不校验身份。
- **托盘与自启**：托盘菜单可暂停同步、打开设置、打开接收文件夹、切换开机自启。
- **本地控制面板**：`http://127.0.0.1:45878/`，只监听本机回环地址，包含设备状态、连接、设置和活动摘要。
- **敏感内容跳过**：默认跳过密码管理器标记为 sensitive 的剪贴板内容（取决于系统是否提供该标记）。
- **大小限制与安全文件名**：文本 16 MiB、PNG 图片 64 MiB、单次文件总量默认 512 MiB、最多 200 个文件；接收文件名会去掉路径和危险字符。

## 系统要求

- Ubuntu 22.04 或更新版本（Wayland / X11 均可），Windows 10 / 11。
- 两台设备需要在可以互相访问的同一网络中。
- 只有从源码构建时才需要 Go 1.25 或更新版本；本项目使用 Go 1.27.4 测试。

## 快速开始

### 从源码运行

需要 Go 1.25 或更新版本；不需要 CGO、GTK 开发头文件或其它运行时。

```bash
git clone https://github.com/luyabing/clipbridge.git
cd clipbridge
./scripts/build.sh
./dist/clipbridge_linux_amd64
```

Windows 上在源码目录运行：

```bat
scripts\build-windows.bat
dist\clipbridge_windows_amd64.exe
```

`dist/` 是本地构建产物，不提交到 Git；发布包由 GitHub Release 提供。

### Ubuntu

直接安装 `.deb` 包（推荐）：

```bash
sudo apt install ./dist/clipbridge_1.0.0_amd64.deb
```

也可以从 GitHub Release 下载 `.deb` 后安装。

安装后从应用列表启动 `剪贴桥`，或运行 `clipbridge`。卸载：

```bash
sudo apt remove clipbridge
```

也可以不安装，直接运行二进制：

```bash
chmod +x dist/clipbridge_linux_amd64
./dist/clipbridge_linux_amd64
```

程序启动后会出现在系统托盘。若托盘图标没有显示，直接打开浏览器访问 <http://127.0.0.1:45878/>。GNOME 需要启用 AppIndicator / KStatusNotifierItem 支持（Ubuntu 默认通常已启用）。

安装到用户目录并创建应用菜单项（不需要 root）：

```bash
./scripts/install-linux.sh
```

之后可以从应用列表启动 `剪贴桥`，或运行 `~/.local/bin/clipbridge`。开机自启在托盘菜单或控制面板中打开，不需要重复执行安装脚本。

### Windows

直接双击 `dist/clipbridge_windows_amd64.exe`。

- 第一次运行时 Windows Defender 防火墙可能询问是否允许网络访问，请允许**专用网络**。
- 程序没有主窗口，启动后位于托盘；双击托盘图标或选择“打开设置…”打开控制面板。
- 开机自启在托盘菜单或控制面板中切换。
- 如果防火墙没有弹窗，见下文“Windows 防火墙”手动放行。

### 首次配对

1. 两台电脑都启动剪贴桥。
2. 在同一局域网内，程序通常会在几秒内自动发现对方并连接。
3. 如果没有自动连接，在任意一端的“手动添加 IP”中填写对方的局域网 IP，例如 `192.168.122.5`，端口默认 `45876`。
4. 强烈建议两端在“设置”中填写**完全相同**的配对码并保存。只填一端会连接失败并提示“配对码设置不一致”。
5. 在任意一台电脑复制文字，另一台应能直接粘贴。

> 控制面板只监听 `127.0.0.1`，因此不能在另一台电脑上打开对方的面板。这是有意的安全限制。

## KVM / 虚拟机网络

### libvirt 默认 NAT（推荐）

`virsh net-list` 中的 `default` 网络通常使用 `192.168.122.0/24`：

- 宿主机（Ubuntu）地址：`192.168.122.1`（`virbr0`）。
- Windows 虚拟机地址：一般是 `192.168.122.x`，由 libvirt DHCP 分配。

在 Windows 中执行 `ipconfig` 查看自己的 IPv4 地址；在 Ubuntu 上可以执行：

```bash
virsh net-dhcp-leases default
ip neigh show dev virbr0
```

连接方法：

- Windows 端手动添加 `192.168.122.1`。
- Ubuntu 端手动添加 Windows 的实际地址，例如 `192.168.122.5`。
- 两端都添加也可以；重复连接会自动收敛为一条。

这个 NAT 模式下宿主机可以直接访问虚拟机，虚拟机也可以访问宿主机，因此不需要端口映射。

### Windows 防火墙

以管理员身份打开 PowerShell，放行剪贴桥的入站端口：

```powershell
New-NetFirewallRule -DisplayName "ClipBridge TCP" -Direction Inbound -Protocol TCP -LocalPort 45876 -Action Allow -Profile Any
New-NetFirewallRule -DisplayName "ClipBridge UDP" -Direction Inbound -Protocol UDP -LocalPort 45875 -Action Allow -Profile Any
```

只需要放行 TCP `45876` 和 UDP `45875`。控制面板的 TCP `45878` 只绑定回环地址，不要对它做端口转发。

### Ubuntu 防火墙

如果启用了 UFW：

```bash
sudo ufw allow 45875/udp
sudo ufw allow 45876/tcp
```

### QEMU user-mode / slirp 网络

不推荐。slirp 默认只允许虚拟机访问外网，宿主机无法主动连接虚拟机。虽然有 `hostfwd` 端口转发方案，但剪贴桥握手包中会通告虚拟机内部端口，转发后的地址容易产生重连问题。建议改用 libvirt 默认 NAT、桥接网络或 macvtap。

## 使用说明

### 控制面板

- **设备**：显示已连接、附近设备和手动地址。
- **暂停同步**：暂停发送和接收，但保持网络连接。
- **设置**：设备名、配对码、自动连接、敏感内容跳过、文本/图片/文件开关、开机自启。
- **最近活动**：只保留事件摘要和数量，不把剪贴板正文写入日志。
- **打开接收文件夹 / 配置目录**：方便查看收到的文件和日志。
- **退出程序**：停止同步并退出。

手动地址会保存到配置中，删除后不再自动重连；已经建立的连接不一定会立刻断开。

### 托盘菜单

- 当前状态（已连接设备数 / 已暂停 / 等待连接）
- 打开设置…
- 暂停同步
- 开机自动启动
- 打开接收文件夹
- 退出剪贴桥

Linux 下托盘使用 D-Bus StatusNotifierItem；Windows 下使用原生托盘图标。

### 文件与图片

- 图片以 PNG 传输。复制图片后，另一端会写入系统剪贴板，可以直接粘贴到聊天工具或图片编辑器。
- 文件会保存到接收目录，同时把接收到的文件列表写入剪贴板。接收目录可在控制面板中打开。
- 暂不支持直接发送文件夹。可以先把文件夹压缩成一个文件再发送。
- 同名文件会自动添加 `(1)`、`(2)` 等后缀，不会覆盖已有文件。
- 单次传输上限可在配置文件中通过 `maxFileMB` 调整（1–2048）。

### 命令行参数

```text
--headless        不创建托盘图标，只在后台运行
--debug           在日志中输出调试信息
--config <path>   使用指定配置文件
--version         显示版本号
```

`--headless` 适合调试或在没有托盘支持的环境中运行；控制面板仍然可用。

## 配置

首次启动会自动创建配置：

| 平台 | 配置与日志目录 |
| --- | --- |
| Ubuntu | `${XDG_CONFIG_HOME:-~/.config}/clipbridge/` |
| Windows | `%APPDATA%\clipbridge\` |

其中：

- `config.json`：设置，权限为当前用户可读写（Linux 下为 `0600`）。
- `clipbridge.log`：运行日志，超过 5 MiB 时轮转为 `clipbridge.log.old`。
- `icon.png`：托盘/控制面板图标缓存。

主要字段：

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `deviceName` | 主机名 | 显示给其它设备的名称 |
| `pairCode` | 空 | 两端相同才可认证和加密；为空则明文 |
| `autoConnect` | `true` | 自动连接发现到的设备 |
| `syncText` | `true` | 同步文本 |
| `syncImage` | `true` | 同步图片 |
| `syncFiles` | `true` | 同步文件 |
| `skipSensitive` | `true` | 跳过密码管理器标记的内容 |
| `paused` | `false` | 暂停同步 |
| `manualPeers` | `[]` | 手动地址列表 |
| `tcpPort` | `45876` | 剪贴板传输端口 |
| `discoveryPort` | `45875` | UDP 发现端口 |
| `webPort` | `45878` | 本地控制面板端口 |
| `maxFileMB` | `512` | 单次文件总大小上限 |
| `downloadDir` | 空 | 覆盖接收目录；为空表示 `下载/ClipBridge` |

端口字段修改后需要重启程序。如果修改了 TCP 端口，手动连接时要写 `IP:新端口`，或让自动发现广播新端口。三个端口必须互不相同。

配置是普通 JSON；手工修改前建议先退出程序，避免被正在运行的实例覆盖。

### 端口

| 协议 | 默认端口 | 用途 | 是否需要对局域网开放 |
| --- | --- | --- | --- |
| UDP | `45875` | 自动发现广播 | 是 |
| TCP | `45876` | 剪贴板与文件传输 | 是 |
| TCP | `45878` | 本地控制面板（仅 `127.0.0.1`） | 否 |

## 安全说明

- **不设置配对码**：任何能访问 TCP 端口的人都可以连接并向你发送剪贴板内容，流量也是明文。仅建议在完全可信的临时网络中使用。
- **设置配对码**：握手使用 HMAC-SHA256 验证双方持有相同配对码；之后的每个帧使用 AES-256-GCM 加密和认证。配对码不会直接上网传输，但会保存在本机配置中。
- 配对码同时用于认证和密钥派生，没有前向保密；请使用足够长、随机的配对码，并避免在不可信网络中使用弱口令。
- 控制面板只绑定 `127.0.0.1`，并校验 `Host`、`Origin` 和自定义请求头，防止 DNS rebinding 与跨站请求。它没有登录密码，同一台电脑上的其它本地用户理论上可以访问；多用户共用电脑时请自行评估。
- 日志只记录事件摘要、大小、设备名和错误，不记录剪贴板正文。
- 文件接收端会清理文件名中的路径和危险字符，防止目录穿越；但仍建议不要同步来源不明的可执行文件。
- 敏感内容跳过依赖操作系统/密码管理器提供标记，是“尽力而为”，不是安全边界。

## 限制

- 仅针对 Ubuntu 和 Windows 构建；其它平台未测试。
- 不支持文件夹、增量同步、断点续传或历史记录。
- 两端同时复制时以各自最后收到的内容为准；剪贴板本身没有合并语义。
- 程序退出后不再同步；Linux 下 Wayland 剪切板内容的可粘贴性依赖写入方进程存活（这是 Wayland 的机制）。
- 自动发现依赖 UDP 广播，跨 VLAN、部分虚拟网络或严格防火墙下可能失效，此时请用 IP 手动连接。
- 图片回声抑制有一个约 2.5 秒的短窗口；在这段时间内立刻复制另一张图片可能被忽略。
- 文件传输完整性依赖 TCP 和文件大小校验，没有单独的 SHA 校验和。

## 构建与打包

需要 Go 1.25+，不需要 CGO，也不需要 GTK 开发头文件。

```bash
./scripts/check.sh   # go test + go vet + Shell 语法检查
./scripts/build.sh   # 构建 Linux、Windows 和 deb
```

输出：

- `dist/clipbridge_linux_amd64`（静态 ELF）
- `dist/clipbridge_windows_amd64.exe`（GUI 子系统 PE，含图标资源）
- `dist/clipbridge_1.0.0_amd64.deb`（在安装了 `dpkg-deb` 的系统上自动生成）

单独构建 deb：

```bash
make deb
```

Windows 上原生构建：

```bat
scripts\build-windows.bat
```

或者手动执行：

```bash
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath \
  -ldflags "-s -w -H windowsgui -X main.version=1.0.0" \
  -o dist/clipbridge_windows_amd64.exe .
```

- `-H windowsgui` 让 Windows 可执行文件不弹出控制台窗口。
- `rsrc_windows_amd64.syso` 是嵌入的 Windows 图标资源，已随源码提供。需要重新生成时：

```bash
go install github.com/akavel/rsrc@latest
rsrc -ico assets/icon.ico -o rsrc_windows_amd64.syso
```

如果要看 Windows 控制台日志，构建时去掉 `-H windowsgui`，并在命令行运行。

## 故障排查

**连不上**

1. 确认两台电脑在同一网段，能互相 `ping`。
2. Windows 防火墙是否放行了 TCP `45876` / UDP `45875`；Ubuntu 是否放行了 UFW。
3. 检查两端配对码：必须同时设置且相同，或同时留空。
4. 查看控制面板“最近活动”和 `clipbridge.log`。用 `--debug` 启动可获得更详细的连接日志。
5. 端口被占用时，日志会显示“无法监听 TCP/控制面板”。修改配置端口或关闭占用程序后重启。
6. 自动发现失败时直接手动填写 IP，不依赖 UDP 广播。

**托盘图标不显示**

GNOME 需要 AppIndicator / KStatusNotifierItem 扩展。没有托盘时使用 `--headless` 运行，并直接访问 <http://127.0.0.1:45878/>；开机自启仍可通过面板设置。

**Linux 下复制没有反应**

先用 `--debug` 查看日志中的“系统剪贴板监听”与事件行。Wayland 桌面需要合成器支持 data-control（GNOME 49+ 原生支持，旧版本会回退到 XWayland）。如果 XWayland 回退也不可用，确认 `DISPLAY` 或 `WAYLAND_DISPLAY` 环境变量存在。

**提示“剪贴桥已经在运行”**

同一配置端口下只允许一个实例。再次启动会尝试打开已有实例的控制面板。如果确认没有运行却提示占用，检查 `45878` 是否被其它程序占用，或修改 `webPort`。

**Windows 上无法粘贴收到的文件**

文件已经保存在接收目录中，可以直接打开文件夹使用；部分应用不接受通过程序写入的文件列表，此时手动从文件夹拖入即可。

## 项目结构

```text
.
├── main.go                    程序入口、命令行参数、信号处理
├── internal/
│   ├── app/                   生命周期、连接、发现、文件传输、Web、托盘
│   ├── config/                JSON 配置
│   ├── platform/              开机自启、打开目录、错误对话框
│   ├── proto/                 帧协议、握手、认证、加密
│   └── assets/                内嵌图标
├── assets/                    SVG / PNG / ICO 源图标
├── internal/app/web/          本地控制面板前端
├── scripts/                   构建、deb 打包、Linux 安装与本地检查
├── rsrc_windows_amd64.syso    Windows 可执行文件图标资源
└── dist/                      本地构建产物（不提交，发布包见 Releases）
```

## 许可证

MIT，见 [LICENSE](LICENSE)。

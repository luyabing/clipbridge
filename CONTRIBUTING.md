# 贡献指南

感谢你愿意改进剪贴桥。提交前请先搜索现有 Issue，避免重复。

## 开发环境

- Go 1.25 或更新版本（`go version` 确认）
- Linux 构建 deb 需要 `dpkg-deb`；构建 Windows 产物不需要 Wine
- 无需 CGO、GTK 开发头文件或其它运行时

```bash
git clone https://github.com/luyabing/clipbridge.git
cd clipbridge
./scripts/check.sh
```

## 提交前检查

```bash
./scripts/check.sh      # go test + go vet + Shell 语法
./scripts/build.sh      # 交叉编译 Linux / Windows，可选 deb
```

如果改动了剪贴板或连接逻辑，请尽量在真实的两台设备（或 Ubuntu 宿主机 + Windows 虚拟机）上验证，并在 PR 中说明测试方式。

## 代码约定

- 使用 `gofmt` 格式化 Go 代码；提交前运行 `gofmt -w .`。
- 保持包边界：平台相关逻辑放在 `internal/platform`，协议与加密放在 `internal/proto`。
- 不要在日志中写入剪贴板正文、配对码或其它敏感内容。
- 新增配置字段时提供合理默认值，并保持对旧配置文件的兼容。
- 用户可见文案目前以中文为主，可以同时补充英文。

## 提交 PR

1. Fork 仓库并从 `main` 创建分支。
2. 提交信息使用简洁的祈使句，例如 `fix: handle empty clipboard on Wayland`。
3. 在 PR 描述中说明改动原因、测试方式和潜在影响（配置格式、协议、安全边界）。
4. 界面或托盘改动请附截图。

## 安全漏洞

请勿公开提交安全 Issue，按 [SECURITY.md](SECURITY.md) 私密报告。

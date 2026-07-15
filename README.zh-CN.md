# Hideout

<!-- markdownlint-disable MD013 -->

[English](README.md)

**让工具干活，别把整台电脑交出去。**

Hideout 把 agent 和不完全可信的 CLI 放进本地虚拟机。项目目录照常可写，
`code .` 这类本机体验仍可通过明确授权使用；所有触及主机的动作都有记录、
可检查、可拒绝。

```console
hideout run -- codex
hideout run -- code .
hideout audit show --limit 5
```

第一条命令假设 CLI 已安装在 guest 中；`code .` 需要受支持的本机编辑器和已批准
的 host-app capability。

## 命令实际运行在哪里

```text
macOS host                                      Lima VM
+----------------------------------+            +---------------------------+
| Terminal                         | start      | target CLI runs here      |
| hideout + Core                   +----------->| codex / git / npm         |
| policy / approval / audit        |            |                           |
|                                  | RW mount   | /workspace                |
| project checkout                 +===========>|                           |
|                                  | approved   | code . / open ...         |
| VS Code / browser <--------------+------------+ typed host request        |
+----------------------------------+            +---------------------------+
```

`hideout` 本身以及 policy、approval、audit 逻辑运行在主机上；
`hideout run --` 后面的目标命令运行在 VM 中。选中的项目 checkout 会映射为
`/workspace`。`code .` 这样的投射命令只会把结构化资源请求交回 Hideout Core，
真正的 VS Code 运行在主机上，guest 不会因此获得通用主机 shell。workspace
之外的主机文件仍需要显式 HostFS capability。

<!-- hideout-public-release:start -->
当前版本：[Hideout v0.1.0-alpha.1](https://github.com/vibe-agi/hideout/releases/tag/v0.1.0-alpha.1)，支持 macOS arm64。这是需要有人监督的
公开 alpha，不是 GA 或 Linux 安装包承诺。

安装包 SHA-256：`9a35bbb70b298456dd7e001a1c22825cdff180309306e8a27271e995a81473b4`。Release 页面同时提供 checksum、机器可读
release manifest 和有界验证证据。
<!-- hideout-public-release:end -->

> [English README](README.md)、[docs/STATUS.md](docs/STATUS.md) 和
> [docs/support-matrix.md](docs/support-matrix.md) 是当前能力与边界的 canonical
> 说明；本页是产品入口的中文版本。

## 安装

公开 alpha 支持 Apple Silicon Mac，使用 [Lima](https://lima-vm.io/) 提供虚拟机
隔离。

```bash
brew install lima
curl -fsSL https://raw.githubusercontent.com/vibe-agi/hideout/master/install.sh | sh
export PATH="$HOME/.local/bin:$PATH"
```

安装器不使用 `sudo`，也不会修改 shell 启动文件。安装前会校验公开 release
inventory、压缩包 SHA-256、包身份和 macOS 代码签名。希望先阅读脚本时：

```bash
curl -fsSL https://raw.githubusercontent.com/vibe-agi/hideout/master/install.sh \
  -o /tmp/hideout-install.sh
less /tmp/hideout-install.sh
sh /tmp/hideout-install.sh
```

手动下载、校验、修复和卸载见
[Distribution And Bootstrap](docs/distribution-bootstrap.md)。

## 第一次运行

请使用独立的项目 checkout。首次使用时会另行下载约 1 GB 的 developer runtime。

```bash
cd /path/to/project
hideout run -- git status --short
hideout audit show --limit 5
```

完整的 15 分钟流程还包括安装测试过的 agent CLI、用本机编辑器打开 workspace、
隐私网络和故障恢复，见 [First-Run Alpha Path](docs/first-run-alpha.md)。

## 为什么是 Hideout

- **真正的本地 VM 边界。** 目标程序运行在独立 guest kernel 中，不直接进入主机
  进程空间。
- **隔离不等于远程机器。** workspace 仍可写；通过类型化能力，可以把映射资源
  交给受支持的本机应用。
- **主机权限逐项给。** 主机文件、应用、网络、审批和导出都有 typed plan、decision
  与 audit evidence。

Hideout 不会把 guest 参数直接交给主机 shell。社区 adapter 和 host-app recipe
只能选择 Core 已审核的能力，不能获得通用的主机命令执行权限。

## 当前边界

- 当前是 macOS arm64 + Lima 的公开 supervised alpha。
- 选中的项目 workspace 对目标程序可见且可写。
- `direct` 网络不会隐藏网络身份；隐私网络需要额外的代理和 DNS 前置条件。
- 目标一旦获得 guest root，Hideout 不声称仍能保持原隔离保证。
- `--backend native` 只是开发 harness，不是隔离边界。

精确支持范围见 [Support Matrix](docs/support-matrix.md)，安全声明边界见
[Claim Boundaries](docs/claim-boundaries.md)。

## 继续了解

- [前 15 分钟](docs/first-run-alpha.md)
- [当前实现状态](docs/STATUS.md)
- [威胁模型](docs/threat-model.md)
- [支持矩阵](docs/support-matrix.md)
- [文档索引](docs/README.md)

## 从源码构建

源码开发需要 Go，公开安装包不需要。

```bash
scripts/install-local.sh
export PATH="$HOME/.local/bin:$PATH"
hideout doctor
```

开发 gate 见 [CONTRIBUTING.md](CONTRIBUTING.md)。安全问题通过
[SECURITY.md](SECURITY.md) 报告；普通 bug 和产品反馈可以使用 GitHub Issues。

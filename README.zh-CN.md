# Hideout

<!-- markdownlint-disable MD013 -->

[English](README.md)

**让工具干活，别把整台电脑交出去。**

Hideout 把 AI coding agent 和陌生 CLI 放进本地虚拟机。它们可以正常修改你选中的
项目，但不会顺手拿到整台 Mac。需要打开 VS Code、浏览器或其他主机资源时，
Hideout 只处理这一次具体请求，并记录发生了什么。

```bash
# 在 VM 中处理选中的项目。
hideout run -- git status --short

# 通过受控桥接，在主机 VS Code 中打开同一个项目。
hideout run -- code .

# 查看哪些动作跨过了 VM 边界。
hideout audit show --limit 5
```

`code .` 默认在一个**安全隔离的编辑器窗口**中打开项目——独立的 VS Code
profile、禁用扩展和自动 workspace 任务——因为该项目刚被工具改写过。这个默认
无需任何批准,只要求一个受支持、已签名的本机编辑器。若要改用你**完整的原生
VS Code** 打开,则是单独的 opt-in（trusted 模式,通过 `hideout allow host-app code`
按项目授权）；其工作方式见 [docs/first-run-alpha.md](docs/first-run-alpha.md#open-in-a-host-editor)。

## 安装

公开 alpha 支持 Apple Silicon Mac。Homebrew 会从 Vibe AGI 官方 tap 安装 Hideout
及其 [Lima](https://lima-vm.io/) 依赖：

```bash
brew install vibe-agi/tap/hideout
hideout setup
```

Formula 会校验不可变归档的 checksum、macOS 代码签名和 Hideout 包清单。安装过程
不会启动 VM 或创建 profile；交互式 `setup` 会先展示固定的默认配置，确认后只写入
配置，仍不会启动 VM 或下载 runtime。可审阅的 standalone
installer、手动下载、修复和卸载流程仍见
[Distribution And Bootstrap](docs/distribution-bootstrap.md)。

## 试一下

请使用独立的项目 checkout。首次使用会另行下载 developer runtime，大小约 1 GB。

```bash
cd /path/to/project

# 目标程序看到的是 Linux，并以非 root 用户运行。
hideout run -- sh -lc 'uname -s; id -u'

# 项目仍然是普通、可写的 Git checkout。
hideout run -- git status --short

# 查看或修改新 session 使用的网络连接。
hideout show connection
hideout connect directly
```

完整的 15 分钟流程还包括安装测试过的 agent CLI、用本机编辑器打开 workspace、
隐私网络和故障恢复，见 [First-Run Alpha Path](docs/first-run-alpha.md)。

## 命令实际运行在哪里

```text
macOS 终端              macOS 控制面                       Lima VM
+----------------+      +-----------------------+          +----------------+
| hideout 客户端 |<====>| hideoutd 进程角色      |<========>| 固定 session    |
| raw TTY/resize |      | Manager + policy      |  framed  | supervisor     |
| review/output  |      | backend/session owner |  stream  | PTY + process  |
+----------------+      | approval/audit/HostFS |          | target CLI     |
                        +-----------+-----------+          +-------+--------+
                                    |                              |
本机应用 <--- typed request --------+      项目 checkout ==========+ RW mount
```

`hideout` 是轻客户端：解析调用、展示 canonical review、管理本地终端状态，并传输
输入、输出和 resize frame。常驻的 `hideoutd` 进程角色拥有 Manager Core、policy、
Lima/SSH、每次运行的权限、活动 session、audit 和 cleanup。VM 内的固定打包
supervisor 拥有目标 PTY 和进程树。这个角色目前由同一个已安装可执行文件在内部
启动，不需要用户另装或手动启动第二个程序。

选中的项目会挂载到 profile 决定的 guest 路径。`code .` 这样的投射命令只会把
结构化资源请求交回 Manager Core；真正的 VS Code 运行在主机上，guest 不会因此
获得通用主机 shell。其他主机文件仍需要显式 HostFS permission。

## 为什么是 Hideout

- **保留真正的 VM 边界。** 目标程序运行在独立 guest kernel 中，不直接进入主机
  进程空间。
- **保留本地开发体验。** 项目仍然可写；受控桥接可以把映射资源交给受支持的
  本机应用。
- **主机访问看得见。** 主机文件、应用、网络、审批和导出受明确规则约束，并留下
  audit evidence。

社区 adapter 和 host-app recipe 可以选择 Core 已审核的能力，但不能把任意 guest
参数交给主机 shell，也不能增加通用主机执行能力。

## 当前版本

<!-- hideout-public-release:start -->
当前版本：[Hideout v0.1.0-alpha.1](https://github.com/vibe-agi/hideout/releases/tag/v0.1.0-alpha.1)，支持 macOS arm64。这是需要有人监督的
公开 alpha，不是 GA 或 Linux 安装包承诺。

安装包 SHA-256：`9a35bbb70b298456dd7e001a1c22825cdff180309306e8a27271e995a81473b4`。Release 页面同时提供 checksum、机器可读
release manifest 和有界验证证据。
<!-- hideout-public-release:end -->

> [English README](README.md)、[docs/STATUS.md](docs/STATUS.md) 和
> [docs/support-matrix.md](docs/support-matrix.md) 是当前能力与边界的 canonical
> 说明；本页是产品入口的中文版本。

## 当前边界

- 当前是 macOS arm64 + Lima 的公开 supervised alpha。
- 选中的项目 workspace 对目标程序可见且可写。
- `direct` 网络不会隐藏网络身份；隐私网络需要额外的代理和 DNS 前置条件。
- 目标一旦获得 guest root，Hideout 不声称仍能保持原隔离保证。
- `--backend native` 只是开发 harness，不是隔离边界。
- 兼容的 macOS arm64 Lima 自动运行会跨项目复用同一个 profile 对应的 VM，每个 session
  仍只获得自己精确的 `/workspace` 视图。共享 guest kernel 不等于项目间的 VM 隔离墙；
  需要独立 VM 信任域时，应创建 dedicated named environment。
- 最后一个依赖 VM 的资源及 provider cleanup 释放后，Hideout 等待 15 秒并以非破坏
  方式停止 Lima VM。environment、guest disk、cache、audit 与 staged HostFS 状态都会
  保留；ownership 或 backend 状态不明时不会自动停止。
- 初始终端尺寸和实时 SIGWINCH resize 已支持；完整终端模拟器、主题、OSC/CSI 和
  detach 行为不在当前声明内。

精确支持范围见 [Support Matrix](docs/support-matrix.md)，安全声明边界见
[Claim Boundaries](docs/claim-boundaries.md)。

同一份由程序维护的契约也可以通过 `hideout support matrix` 查看。

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

# Hideout

[English](README.md)

Hideout 是一个面向不可信开发工具和 agent CLI 的本地隐私运行器。
它把目标命令运行在可复用的 Lima 环境里，为目标提供隔离身份，
通过类型化能力路由主机访问，并记录边界证据。

当前状态：private alpha / supervised dogfood。核心 v1 路径已经可用，
但这还不是公开 GA 版本。

## Hideout 保护什么

Hideout 用显式能力替代主机上的 ambient authority：

- 目标命令会获得隔离的 home、XDG 路径、机器身份和 git 配置；
- 项目 workspace 会以读写方式挂载，用于保持正常开发体验；
- workspace 之外的主机文件需要显式 HostFS 授权；
- `open` 和 `preview.open` 这类主机逃逸通过类型化 broker 路由；
- 代理凭证可以被 Hideout 使用，但不会出现在目标进程 env 里；
- 每次运行都会写入 audit 和边界摘要证据。

重要的非承诺：

- 已经在 mounted workspace 里的 secret 对目标命令可见；
- `direct` 网络模式不会隐藏网络身份；
- `tun2socks` 隐藏的是网络出口路径，但它不是数据防泄漏系统；
- `--backend native` 只是开发 harness，不是隔离后端。

## 使用要求

macOS 上需要：

- Go；
- Lima（`limactl`）；
- Google Chrome 或其他支持的 Chromium 兼容浏览器，用于真实浏览器
  host-open 检查；
- 可选的本地代理，用于 `tun2socks` 模式。

本地开发时，从源码树安装：

```bash
scripts/install-local.sh --backend lima --network direct
export PATH="$HOME/.local/bin:$PATH"
hideout doctor --backend lima
```

安装脚本会构建：

- `hideout`；
- 主机 command shim；
- Linux guest shim；
- Linux HostFS daemon。

## 第一次运行

请使用专用的项目 checkout。不要从 `$HOME`、`~/.hideout` 或包含主机凭证
的目录运行 Hideout。workspace 会被有意挂载进 guest。

```bash
cd /path/to/sanitized/project
hideout run --backend lima -- pwd
```

可复用 Lima 环境按 profile、workspace、backend 和工具策略建立索引。
成功运行后会打印一个 resume ID：

```bash
hideout list
hideout run --resume <env-id> -- <command>
hideout stop <env-id>
hideout clean --stopped <env-id>
```

使用 `--rm` 可以创建一次性环境：

```bash
hideout run --backend lima --rm -- <command>
```

## 运行一个 CLI 工具

Hideout 不会 hardcode 某个具体产品的 CLI。你需要在 profile 上配置通用
工具供给，然后运行命令。

对于 npm CLI：

```bash
hideout profile tools default preset add node-dev
hideout profile tools default npm add --package <npm-package> --command <command>
hideout run --backend lima -- <command> --version
```

`node-dev` 和 npm global 安装会在受管理的 guest setup 阶段运行，并且
会在所选网络模式已经生效之后执行。

如果某个 CLI 需要持久化登录状态，把它放到隔离的 profile home，而不是
主机 home：

```bash
hideout profile home default import \
  --from /host/path/to/state \
  --to .config/<tool>/state
```

## 网络模式

Direct mode 是兼容性默认模式：

```bash
hideout init --no-input --backend lima --network direct
hideout run --backend lima --network direct -- <command>
```

Hidden proxy mode 会在 guest 内使用 `tun2socks`。代理 secret 保存在
host-only secret ref 中，不会传给目标进程。

如果你的主机代理监听在 `127.0.0.1:7890`，Lima guest 应该通过
`host.lima.internal:7890` 访问它：

```bash
export HIDEOUT_SECRET_DEFAULT_PROXY=socks5://host.lima.internal:7890

hideout init --no-input \
  --backend lima \
  --network tun2socks \
  --proxy-secret default-proxy

hideout run --backend lima \
  --network tun2socks \
  --proxy-secret default-proxy \
  -- <command>
```

如果代理路由无法验证，`doctor` 和 run bootstrap 会 fail closed。

## 访问 workspace 之外的主机文件

workspace 会被直接挂载。其他主机文件应该通过 HostFS grant 访问。

Run-scoped grant：

```bash
hideout run --backend lima --fs read:/absolute/file -- <command>
hideout run --backend lima --fs dir:/absolute/dir -- <command>
hideout run --backend lima --fs tree:/absolute/dir -- <command>
hideout run --backend lima --fs read:/absolute/dir/*.txt -- <command>
```

持久 profile 规则：

```bash
hideout profile fs default list
hideout profile fs default add --fs read:/absolute/file --reason "tool input"
hideout profile fs default deny --no-fs tree:/absolute/dir --reason "too broad"
hideout profile fs default remove <rule-id>
```

Hideout store 是保留的 control-plane 状态，不能通过 HostFS grant 暴露。

## Host Open 与 Preview

注册过的主机逃逸是类型化且可审计的。`host.open` 不允许原始的 host
localhost/private URL 访问。

把 guest dev server 暴露给主机浏览器：

```bash
hideout run --backend lima \
  --preview 127.0.0.1:5173 \
  -- npm run dev
```

Hideout 会创建一个 run-scoped host-to-guest 映射，并在主机浏览器打开
映射后的 endpoint。这和 `host.open` 是分开的；localhost deny 规则仍然
保持不变。

## 审计与清理

每次运行都会打印边界摘要，并写入 audit log 路径：

```text
Hideout boundary:
  audit: .../audit.jsonl
  host.open: allowed=1 denied=0
  hostfs: allowed=0 denied=1 unsupported=0
```

常用清理命令：

```bash
hideout list
hideout stop <env-id>
hideout stop --idle 2h
hideout clean --dry-run --stopped
hideout clean --stopped <env-id>
hideout cleanup --dry-run
hideout cleanup
```

`stop` 会释放 VM 内存并保留可复用环境。`clean` 会删除 stopped 或指定的
环境。`cleanup` 会移除 session-local runtime 和 secret-bearing 文件，
默认保留 audit。

## 验证

快速本地检查：

```bash
scripts/test-phase1.sh --quick
```

必需自动化 gate：

```bash
scripts/test-phase1.sh --required
```

在 macOS + Lima + 真实浏览器 + operator proxy 上验证 release dogfood：

```bash
export HIDEOUT_SECRET_DEFAULT_PROXY=socks5://host.lima.internal:7890
scripts/test-release-dogfood.sh
```

这会运行 Gate 0、native harness、真实 Lima E2E、严格 hidden proxy、
真实浏览器 host escape、capability probes 和 generic CLI dogfood smoke。

## 文档地图

- [架构原则](docs/architecture-principles.md)
- [主设计文档](docs/privacy-run-design.md)
- [威胁模型](docs/threat-model.md)
- [测试计划](docs/privacy-run-test-plan.md)
- [网络隐私](docs/network-privacy-architecture.md)
- [OpenTarget 架构](docs/opentarget-architecture.md)
- [分发与初始化](docs/distribution-bootstrap.md)

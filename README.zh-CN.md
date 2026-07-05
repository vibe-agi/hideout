# Hideout

[English](README.md)

Hideout 是一个面向不可信开发工具和 agent CLI 的本地隐私运行器。
它把目标命令运行在可复用的 Lima 环境里，为目标提供隔离身份，
通过类型化能力路由主机访问，并记录边界证据。

当前状态：private alpha / supervised dogfood。核心 v1 路径已经产出一份本机
release-candidate 证据，覆盖 Gate 0、Gate 1、Gate 2 Lima E2E、Gate 3
严格代理、Gate 4 真实浏览器 host escape、capability probes 和通用 CLI
dogfood smoke。公开 GA 仍需要面向 release artifact 的可重复证据、打包/分发
证据以及发布签核。

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

## 安装要求

如果使用 release-like tarball，macOS 上需要：

- Lima（`limactl`）；
- Google Chrome 或其他支持的 Chromium 兼容浏览器，用于真实浏览器
  host-open 检查；
- 可选的本地代理，用于 `tun2socks` 模式。

tarball 路径不需要 Go。它已经包含主机二进制、Linux guest helpers、
manifest schemas 和包内 installer。包内 installer 会使用 package 内的
`hideout` 二进制先校验 `package-manifest.json` 中的 checksum，再从解压后的
package 复制预构建产物。

如果从源码树做本地开发，还需要 Go。

本地开发时，从源码树安装。默认安装路径会初始化一个 Lima 后端、
direct 网络的 profile：

```bash
scripts/install-local.sh
export PATH="$HOME/.local/bin:$PATH"
hideout version
hideout doctor
```

如果使用 release-like tarball，先解压，然后从包根目录运行包内 installer：
包内 installer 走同一条默认 init 路径，并且不需要 Go：

```bash
tar -xzf hideout-<platform>.tar.gz
cd hideout
./install.sh
export PATH="$HOME/.local/bin:$PATH"
hideout version
hideout doctor
```

源码树安装脚本会构建：

- `hideout`；
- 主机 command shim；
- Linux guest shim；
- Linux HostFS daemon。

## 第一次运行

请使用专用的项目 checkout。不要从 `$HOME`、`~/.hideout` 或包含主机凭证
的目录运行 Hideout。workspace 会被有意挂载进 guest。

```bash
cd /path/to/sanitized/project
hideout run --profile smoke --backend lima --network direct -- pwd
```

`hideout init` 和 `hideout doctor --fix` 会打印可直接复制的下一步命令，
包括 `doctor` 检查、smoke run，以及已配置的通用 CLI 工具。

第一次运行只应该验证 backend、workspace mount 和隔离身份。这里使用
独立的 `smoke` profile，这样已有 `default` profile 上的工具策略不会在
第一次检查时触发软件包供给。不要在这一步配置 CLI 工具供给；工具供给从
下一节开始。

可复用 Lima 环境按 profile、workspace、backend 和工具策略建立索引。
使用 `hideout list` 查看可 resume 的环境：

```bash
hideout list
hideout run --profile smoke --resume <env-id> -- <command>
hideout stop <env-id>
hideout clean --stopped <env-id>
```

使用 `--rm` 可以创建一次性环境：

```bash
hideout run --profile smoke --rm -- <command>
```

## 运行一个 CLI 工具

Hideout 不会 hardcode 某个具体产品的 CLI。你需要在 profile 上配置通用
工具供给，然后运行命令。

对于 npm CLI：

```bash
hideout init \
  --profile agent \
  --backend lima \
  --network direct \
  --npm-package <npm-package> \
  --npm-command <command>

hideout run --profile agent --backend lima -- <command> --version
```

`node-dev` 和 npm global 安装会在受管理的 guest setup 阶段运行，并且
会在所选网络模式已经生效之后执行。它们会在目标命令启动前为可复用环境
完成 provision，所以改变工具策略后的第一次运行，即使目标命令本身很小，
也可能下载软件包。

同一套 setup 可以先规划或后续修复：

```bash
hideout doctor --fix --dry-run \
  --profile agent \
  --npm-package <npm-package> \
  --npm-command <command>
```

如果需要更底层地编辑 profile：

```bash
hideout profile tools agent preset add node-dev
hideout profile tools agent npm add --package <npm-package> --command <command>
```

如果某个 CLI 需要持久化登录状态，把它放到隔离的 profile home，而不是
主机 home：

```bash
hideout profile home agent import \
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
`host.lima.internal:7890` 访问它。建议配置到一个专用 profile，让网络默认值明确且可复用：

```bash
export HIDEOUT_SECRET_DEFAULT_PROXY=socks5://host.lima.internal:7890

hideout init --no-input \
  --profile privacy \
  --backend lima \
  --network tun2socks \
  --proxy-secret default-proxy

hideout run --profile privacy --backend lima \
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

profile 可以注册额外的 open-like 命令符号，但不会增加新的主机权威。
这些符号仍然走同一套 `host.open` 策略和 `open-target-v1` argv schema：

```bash
hideout profile command-proxy default add-open browser-open
hideout profile command-proxy default list
hideout profile command-proxy default remove browser-open
```

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

默认情况下，`hideout run` 会尽量接近本地命令执行体验：目标命令的 stdout
和 stderr 原样透出，Hideout 控制面进度默认保持安静。如果你需要环境提示、
resume 命令和边界摘要，使用 `--verbose`：

```bash
hideout run --verbose --profile smoke --backend lima -- pwd
```

verbose 运行会打印边界摘要和 audit log 路径：

```text
Hideout boundary:
  audit: .../audit.jsonl
  host.open: allowed=1 denied=0
  hostfs: allowed=0 denied=1 unsupported=0
```

查看审计时优先使用 Manager 支持的 redacted CLI 视图，它会返回最新匹配的
事件，不需要手动读取 raw JSONL：

```bash
hideout audit show --limit 20
hideout audit show --decision deny
hideout audit show --session <session-id> --json
hideout tui --profile agent
hideout tui --once --profile agent
```

`hideout tui` 是终端观察台，适合在第二个终端里常驻运行，用来观察另一个
终端里的 agent 或 CLI 行为。`--once` 只用于脚本和快照。

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

`stop` 和 `clean` 默认也会保持 backend 控制输出安静。排查 `limactl`
行为时，可以给这些生命周期命令加 `--verbose`。

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
默认会在 `.hideout-release-evidence/` 下写入已脱敏的证据包。设置
`HIDEOUT_RELEASE_EVIDENCE_DIR` 可以指定精确输出目录。

## 文档地图

- [架构原则](docs/architecture-principles.md)
- [主设计文档](docs/privacy-run-design.md)
- [威胁模型](docs/threat-model.md)
- [测试计划](docs/privacy-run-test-plan.md)
- [网络隐私](docs/network-privacy-architecture.md)
- [OpenTarget 架构](docs/opentarget-architecture.md)
- [分发与初始化](docs/distribution-bootstrap.md)

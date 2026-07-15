# Hideout

[English](README.md)

**让工具干活，别把整台电脑交出去。**

Run untrusted CLIs without handing them your whole machine.

> 说明：当前公开源码、尚无公开产品包的阶段以 [English README](README.md) 为 canonical
> 权威入口。本中文 README 是 best-effort 本地化摘要；如果两者冲突，以英文 README、
> [docs/STATUS.md](docs/STATUS.md) 和
> [docs/privacy-run-test-plan.md](docs/privacy-run-test-plan.md) 为准。

Hideout 把不可信的开发工具和 agent CLI 运行在隔离的 backend 边界内
（当前实现是可复用的 Lima 虚拟机），所有主机访问都必须经过类型化、
可审计、fail-closed 的授权流程，并记录可供检查的证据。隐私加固是收益
之一，不是产品定义本身。

<!-- hideout-public-release:start -->
当前公开包是 [Hideout v0.1.0-alpha.1](https://github.com/vibe-agi/hideout/releases/tag/v0.1.0-alpha.1)：面向 macOS arm64、使用 Lima
后端、需要有人监督的公开 alpha。它是 prerelease，不承诺 GA 稳定性、自动更新、
Linux 安装包、workspace DLP、guest-root containment 或 marketplace trust。

```bash
curl -fLO "https://github.com/vibe-agi/hideout/releases/tag/v0.1.0-alpha.1/download/hideout-v0.1.0-alpha.1-darwin-arm64.tar.gz"
curl -fLO "https://github.com/vibe-agi/hideout/releases/tag/v0.1.0-alpha.1/download/SHA256SUMS"
grep '  hideout-v0.1.0-alpha.1-darwin-arm64.tar.gz$' SHA256SUMS | shasum -a 256 -c -
tar -xzf "hideout-v0.1.0-alpha.1-darwin-arm64.tar.gz"
cd hideout
./install.sh --skip-init
```

安装包 SHA-256：`9a35bbb70b298456dd7e001a1c22825cdff180309306e8a27271e995a81473b4`。同一 release page 还包含有界 evidence
bundle 和机器可读的 release manifest。
<!-- hideout-public-release:end -->

gate 与发布证据定义见
[docs/privacy-run-test-plan.md](docs/privacy-run-test-plan.md)。

## Hideout 保护什么

Hideout 用显式能力替代主机上的 ambient authority：

- 目标命令会获得隔离的 home、XDG 路径、机器身份和 git 配置；
- 项目 workspace 会以读写方式挂载，用于保持正常开发体验；
- workspace 之外的主机文件需要显式 HostFS 授权；
- 环境变量遵循 profile env policy：显式 public 值、允许列表 inherit 和
  deny 模式（`profile.env.public`、`profile.env.inherit`、
  `profile.env.deny`）；
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
- 可选：安装在受支持 macOS Applications 目录、签名有效的 Visual Studio
  Code，用于 guest 内的 `code .` 投射工作流；
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

如果使用 release-like tarball，先解压，然后从包根目录运行包内 installer。
推荐的 first-run 文档路径使用 `--skip-init`，让安装和 profile 创建分离；
包内 installer 不需要 Go：

```bash
tar -xzf hideout-<platform>.tar.gz
cd hideout
./install.sh --skip-init
export PATH="$HOME/.local/bin:$PATH"
hideout version
hideout package verify "$HOME/.local"
hideout doctor
```

源码树安装脚本会构建：

- `hideout`；
- 主机 command shim；
- Linux guest shim；
- Linux HostFS daemon。

## 快速开始

```bash
hideout init \
  --template dev \
  --profile alpha-direct \
  --backend lima \
  --network direct \
  --runtime developer-standard \
  --no-input
hideout run --profile alpha-direct --backend lima -- pwd
hideout run --profile alpha-direct -- <cli>
hideout audit show --limit 20
```

这条路径只证明打包后的 VM 流程，并不声称隐藏网络身份。具备本地代理和
mediated DNS 条件后，再显式建立单独的 privacy profile：

```bash
export HIDEOUT_SECRET_PROXY_URL=socks5://host.lima.internal:7890
hideout init --template privacy --profile default --backend lima \
  --network tun2socks --proxy-secret proxy-url \
  --mediated-resolver 1.1.1.1 --runtime developer-standard --no-input
hideout run --profile default -- <cli>
hideout run --profile default --fs see-dir:/absolute/directory -- <cli>
hideout run --profile default --fs read:/absolute/file -- <cli>
hideout explain --profile default -- <cli>
```

安装包包含 macOS arm64 上已保留、digest 固定的 `developer-standard` preview
runtime。用 `hideout runtime inspect developer-standard` 检查精确镜像、来源、大小、
inventory 与 SBOM 状态。选择 runtime 必须显式进行，不会改变已有 profile；自定义
镜像和现有 template 路径仍可使用，但不会继承 runtime readiness。

以 non-root target 安装固定测试的真实 agent，不使用 `sudo` 或主机全局目录：

```bash
hideout run --profile alpha-direct -- sh -eu -c '
  rm -rf "$HOME/.npm" "$HOME/.local/lib/node_modules/@openai/codex" "$HOME/.local/bin/codex"
  npm install --global --prefix "$HOME/.local" @openai/codex@0.144.1
  "$HOME/.local/bin/codex" --version
'
```

这条路径只验证安装和执行；交互登录及持久 agent 认证不属于 supported-runtime
preview 的范围。

## 关在 VM 里，也能用本机工具

隔离不应该把本地开发变成远程开发。Hideout 会把少量、经过审核的本机能力
接到 guest 熟悉的命令上；VM 里不需要真的安装这些命令：

```bash
cd /path/to/sanitized/project
hideout run --profile default -- code .
hideout run --profile default -- code src/main.go
hideout run --profile default -- code -g src/main.go:12:3
```

guest 传出的是 workspace 内的结构化引用，不是主机绝对路径。Hideout Core
负责映射当前 workspace、重新检查符号链接边界、验证已登记的 VS Code app
bundle 和签名，然后在主机上打开。失败时直接拒绝，不会退回任意主机命令。

`code` 默认使用安全模式：每次 run 独立的编辑器配置目录、禁用扩展、关闭
自动任务，同时保留 Workspace Trust。若要使用操作者平时的完整 IDE 配置，
必须通过一个对当前 run 生效、可查看、可撤销的授权。`open`/`xdg-open` 仍是
另一组受控的主机打开能力；这个功能不提供 adb、AppleScript 或任意主机执行。

这条路径以及 privacy/hardened profile 的 `/workspace` 别名已经在真实
macOS arm64 Lima 上验证。当前证据来自 private alpha 的 dirty 工作树，
不是正式发布凭据；边界和证据见
[Host Capability Projection](docs/host-capability-projection.md)。

### 社区 Host-App Recipe

社区 host-app recipe 的 v1 生命周期已经实现，接受本地目录或固定到精确
commit 的 Git 来源。只读的 inspect/validate/test 与显式的 add、enable、
update、disable、revoke、remove 分开；enable 只对一个 profile 的未来 run
生效，不会改变旧 session。

社区 pack 只能把声明式 open-resource 命令绑定到现有 Core provider。它不能
自带 JavaScript 权限、新的 host effect、任意主机执行、raw argv、HostFS 权限或
marketplace 签名背书。`safe` 只能来自兼容的 Core-owned safety profile；被明确
接受的未签名 app 仍是 unverified，并使用 `ask-each-run`。HostFS 资源必须已经
具有同一 session 的内容权限；see-only 可见性不能打开内容。当前凭据来自
dirty private-alpha 工作树，不是干净的发布凭据。操作和贡献流程见
[Community Host-App Recipes](docs/host-app-recipes.md)。

## 第一次运行

请使用专用的项目 checkout。不要从 `$HOME`、`~/.hideout` 或包含主机凭证
的目录运行 Hideout。workspace 会被有意挂载进 guest。

```bash
cd /path/to/sanitized/project
hideout run --profile smoke --backend lima --network direct -- pwd
```

`hideout init` 和 `hideout doctor --fix --dry-run` 会打印可直接复制的下一步
命令，包括 `doctor` 检查、smoke run，以及已配置的通用 CLI 工具。安全的
doctor 修复需要显式模式：`--dry-run` 只预览，`--apply` 才应用类型化修复。

第一次运行只应该验证 backend、workspace mount 和隔离身份。这里使用
独立的 `smoke` profile，这样已有 `default` profile 上的策略不会在
第一次检查时触发额外的 guest setup。

每个可复用环境都有名字：不带 `--env` 的 run 使用按 profile+workspace
确定性派生的自动命名环境；`hideout env create` 显式创建并固化 base
image 声明。环境身份输入变化时 fail closed 并给出 recreate 提示，
不会静默切换 guest。

```bash
hideout env create work --image 'template:_images/ubuntu-lts'
hideout run --env work -- <command>
hideout env list
hideout env inspect work
hideout env recreate work
hideout stop work
hideout clean --stopped work
hideout env remove work
```

使用 `--rm` 可以创建一次性环境：

```bash
hideout run --profile smoke --rm -- <command>
```

## 运行一个 CLI 工具

Hideout 不会 hardcode 某个具体产品的 CLI，也不 ship 软件包安装
provider。guest 工具来自两条路径：

- 环境的 base image 提供基础工具链；
- 其余工具由 operator 在边界内用普通 setup 命令自行安装，和任何其他
  run 一样受同样的网络策略与审计约束。

旧的 npm 供给路径已经移除。

先用 `init` 和 `doctor` 创建 profile，然后运行你想要的 CLI：

```bash
hideout init \
  --profile agent \
  --backend lima \
  --network direct

hideout doctor --fix --dry-run --profile agent

hideout run --profile agent --backend lima -- <command> --version
```

如果 base image 里缺少某个工具，用一次针对可复用环境的普通 run 在
边界内安装：

```bash
hideout run --profile agent -- <installer command>
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
`host.lima.internal:7890` 访问它。建议配置到一个专用 profile，让网络
默认值明确且可重复：

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
hideout run --backend lima --fs see:/absolute/path -- <command>
hideout run --backend lima --fs see-dir:/absolute/directory -- <command>
hideout run --backend lima --fs see-tree:/absolute/directory -- <command>
hideout run --backend lima --fs read:/absolute/file -- <command>
hideout run --backend lima --fs dir:/absolute/dir -- <command>
hideout run --backend lima --fs tree:/absolute/dir -- <command>
hideout run --backend lima --fs 'read:/absolute/dir/*.txt' -- <command>
```

`see`、`see-dir` 和 `see-tree` 只披露名称和粗粒度节点类型，不授予文件
内容、完整元数据、执行或写入权限。读取可见但锁定的文件会立即返回
`EACCES`；请求符合条件时，会创建一个本地 `hostfs.read` 决策。操作者可
在另一个终端审核，原进程随后重试同一次读取：

```bash
hideout decision list --kind hostfs.read
hideout decision claim <decision-id>
hideout decision approve --claim-token <claim-token> <decision-id>
```

名称本身就是用户数据，可能进入 CLI 或模型上下文。V1 的 `see*` selector
拒绝 glob；已有的 glob 内容过滤继续使用 `read:` selector。

HostFS glob selector 要加引号，避免先被宿主 shell 展开。`*` 不会隐式包含
`.env` 这类 dotfile；需要显式使用 dotfile selector 授权。字面量 glob 字符
或字面量反斜杠用反斜杠转义，例如 `read:/absolute/dir/\[2026\].txt`。

持久 profile 规则：

```bash
hideout profile fs default list
hideout profile fs default add \
  --fs see-dir:/absolute/directory --reason "navigate names"
hideout profile fs default add --fs read:/absolute/file --reason "tool input"
hideout profile fs default deny --no-fs tree:/absolute/dir --reason "too broad"
hideout profile fs default remove <rule-id>
```

旧 `list:` 规则必须一次性显式迁移，并先展示披露范围；Hideout 不会静默
把它别名成更宽的 `see-dir`/`see-tree`。使用 `hideout profile fs default
migrate-list --map <rule-id>=see-dir --reason "已审阅名称披露"`，并为每条旧
规则重复传入 `--map`。onboarding 默认
`--hostfs-visibility none`；`landmarks` 只增加显式的一层目录，
`home-tree` 还必须传 `--acknowledge-name-disclosure`。

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

`hideout ui --no-open --print-url` 在本机 Manager API 之上启动 WebUI
体验面并打印地址；它是更完整的管理视图，任何首跑流程都不依赖它。

常用清理命令：

```bash
hideout env list
hideout stop <name|env-id>
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

## 可编程策略与共享

边界决策可以通过受约束的 JavaScript（goja）entrypoint 编程，例如
`command.decide` 和 `audit.redact`：脚本只在提供的 context 内做决策、
分类和脱敏，永远不会获得文件系统、网络或进程访问。生态共享覆盖策略
脚本、非敏感配置和声明式 base image reference；secret 通过 SecretRef
参数化，由每个用户在本地自行填入。参见
[docs/script-extension-architecture.md](docs/script-extension-architecture.md)
和
[docs/ecosystem-foundation-design.md](docs/ecosystem-foundation-design.md)。

## 问题反馈

普通缺陷请使用
[bug form](https://github.com/vibe-agi/hideout/issues/new/choose)，疑似安全问题请使用
[private vulnerability reporting](https://github.com/vibe-agi/hideout/security/advisories/new)。
请提供 `hideout version`、package digest、backend/platform、recovery code 和
有边界的 doctor 摘要。

诊断证据需要离开本机时，不要直接附 raw log，使用类型化导出路径：

```bash
hideout doctor --format json --evidence-out doctor-report.json
hideout audit export --source doctor-report --doctor-report doctor-report.json \
  --out doctor-export.json --acknowledge-full-fidelity
```

控制面脱敏会移除 Hideout 生成的凭据，但不会移除全部用户数据。批准或附加
导出物前，必须检查 pre-export summary 和实际内容。

## 验证

快速本地检查：

```bash
scripts/test-phase1.sh --quick
```

完整 gate 与发布证据流程定义见
[docs/privacy-run-test-plan.md](docs/privacy-run-test-plan.md)。

## 文档地图

- [架构原则](docs/architecture-principles.md)
- [主设计文档](docs/privacy-run-design.md)
- [威胁模型](docs/threat-model.md)
- [测试计划](docs/privacy-run-test-plan.md)
- [网络隐私](docs/network-privacy-architecture.md)
- [OpenTarget 架构](docs/opentarget-architecture.md)
- [分发与初始化](docs/distribution-bootstrap.md)
- [生态基础设计](docs/ecosystem-foundation-design.md)
- [脚本扩展架构](docs/script-extension-architecture.md)

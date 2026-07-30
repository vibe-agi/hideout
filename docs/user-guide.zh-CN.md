# Hideout 用户指南

Hideout 把不熟悉的 AI 或开发命令放进本地 VM 运行，并用一个控制台展示它的
边界、活动、配置和清理状态。当前支持的发布目标是 Apple 芯片 Mac。

选中的项目目录可写；直连网络不会隐藏你的网络出口。Hideout 的作用是缩小并
解释边界，而不是把有害命令变成无害命令。

## 从这里开始

普通用户第一次使用只需要：

```sh
hideout setup
hideout doctor
cd /path/to/project
hideout run -- git status --short
```

冒烟命令成功后，再替换成要运行的工具：

```sh
hideout run -- claude
```

`setup` 会先展示将要写入的内容，确认默认为 No；它不会启动 VM，也不会下载
runtime。`doctor` 会报告第一个失败的前置条件以及安全的下一步。

命令运行时，可以在另一个终端打开 HUD：

```sh
hideout tui
```

主要视图是 Overview、Activity、Config、Operations 和 Help。按 `?` 查看当前
视图真正可用的按键，选中一行后按 Enter 查看详情。配置和环境操作会先打开
复核弹窗，第一次 Enter 不会直接修改状态。

同一套 Manager 数据也可以在本地浏览器里查看：

```sh
hideout ui
```

浏览器 URL 带有短时有效的本地 token，请勿分享。服务只监听 loopback。

## 查找命令

默认帮助展示可执行的用户路径，而不是把内部命令全部倾倒出来：

```sh
hideout help
hideout help run
hideout help search proxy
hideout help all
```

`hideout help --all` 是 `hideout help all` 的兼容别名。上下文帮助固定说明用途、
前置条件、影响、安全边界、恢复方式和下一条安全命令。完整目录把 stable、
advanced 和不受支持的 lab 命令分开。

## 代理密钥

不要把代理 URL 放进命令参数或长期环境变量。通过正在运行的 daemon 保存，
输入过程不会回显：

```sh
hideout daemon start
hideout secret set local-proxy
hideout secret status local-proxy
hideout connect through local-proxy using 1.1.1.1
hideout show connection
```

`connect` 会先展示权威 diff、在线或下次 attach 的影响、阻断项和回滚方式，
然后在终端中请求确认。自动化可拆成 `hideout connect plan ...`，再执行输出
中的准确命令 `hideout connect apply <operation-id> --yes`；未确认的计划不会
修改 Desired。

值可以是 `socks5://127.0.0.1:7890`，但应当输入到隐藏提示中，而不是写进上面
的命令。配置、计划、输出和历史里只出现引用名 `local-proxy`。
在受支持的 Mac 上，daemon 管理的安全存储就是当前用户的 macOS Keychain。
Hideout 不会把值复制到 profile 或本地数据目录。

### 不需要停止 daemon

健康的 `secret set` 或 `secret rotate` 会由正在运行的 daemon 接收。不需要
停止 daemon，也不需要重建 VM。连接命令会分别告诉你：变化已经在线生效，
还是等待下一次符合条件的 attach。

为了单版本迁移兼容，`HIDEOUT_SECRET_<REF>` 只会从 daemon 启动时的环境读取，
不会自动导入 Keychain。daemon 启动后再执行 export 也不会改变它。请重新输入
一次：

```sh
hideout secret set local-proxy
```

随后从 shell 配置中移除旧 export，避免凭据出现在进程环境或 shell history。

## 期望状态、生效状态与待处理状态

配置包含四种不能混为一谈的事实：

- **Desired（期望状态）**：复核并 Apply 后保存到 profile 的值。
- **Effective（生效状态）**：有证据证明当前已 attach workload 正在使用的值。
- **Transition（转换状态）**：在线生效、阻塞、回滚，或
  `pending-next-attach`。
- **Evidence（证据）**：支撑该结论的 operation 和证明。

例如：

```sh
hideout connect through local-proxy using 1.1.1.1
hideout show connection
```

第一条命令会先复核并确认一个准确 operation，之后才修改 Desired。`--yes`
只用于显式、非交互地确认当前命令或 `hideout connect plan` 已展示的计划。

Desired 改变不会重写已经 attach 的进程。如果该环境存在已证明的在线
gateway，新建连接可在 stage/probe/activate/prove 完成后在线切换；已经接受
的连接仍保留原路由。否则由下一次符合条件的 attach 使用新的 Desired
连接。daemon 重启后，旧的持久化路由不会冒充 Effective；在新 gateway
重新建立并证明前显示 `not-observed`。隐私模式的前置条件失败时会 fail
closed，不会悄悄退回 direct。

在 TUI 中按 `3` 进入 Config，选择能力后按 Enter。弹窗严格遵循：

```text
Draft → Manager Plan → diff 与影响 → Confirm → Apply → 终态证据
```

如果另一个客户端改了 profile，当前复核计划会变成 stale，不能 Apply。刷新
后必须重新看 diff，并确认新签发的计划。

## 观测与保留

Hideout 观测 `--` 后启动的命令以及它在该 run workload 边界中的所有后代。
记录的是用来判断 CLI 是否异常的元数据：

- 命令执行身份、父子关系、时间和结果；
- 文件 open/read/write 元数据：哪个进程、哪个路径、什么操作、次数和时间；
- guest provider 能证明归因时的进程到 IP/端口活动和 DNS 查询；
- 可解释的风险规则以及对应事件引用。

Hideout **不会**记录文件内容、环境变量值、键盘输入或完整 PTY 录屏。

Linux observer 会保留所有已观测到的文件写入和变更。为避免动态加载器和系统库
噪声淹没有界中继，它会过滤 `/bin`、`/sbin`、`/usr`、`/lib`、`/lib64`、
`/proc`、`/sys`、`/dev` 下以及 `/etc/ld.so.cache` 的非变更型
`open`/`read`/`mmap`。工作区、Home、profile/HostFS、临时文件和其他配置路径
的读取仍然可见。因此 File coverage 保持 `Partial`，并显示
`system-runtime-read-noise-filtered` 证据；这里不是完整的操作系统审计日志。

常用查询：

```sh
hideout activity summary
hideout activity events --session <id>
hideout activity executions --session <id>
hideout activity risks --session <id>
hideout activity coverage --session <id>
```

`--session` 始终只查看这一次 run；即使多个 run 共用同一个可复用环境，也不会
混入其他会话。只有明确需要查看整个精确 VM incarnation 的保留历史时，才使用
`--environment <id> --incarnation <id>`。

Coverage 是结论的一部分：

- `Available`：请求的子系统和时间窗受支持，且没有已知丢失破坏结论。
- `Partial`：明确列出丢失、截断、provider 缺口或归因限制。
- `Unavailable`：不能把“没有事件”解释成“没有行为”。

本地活动绑定到精确的 environment/VM incarnation，以当前用户私有方式保存，
生命周期与该环境一致。clean 或 recreate 环境时，其保留观测也会删除。容量
有上限；发生截断时 coverage 会降为 Partial，而不会伪装成完整记录。

默认每个精确 owner 最多 256 MiB，不设置墙钟 TTL（显示为
`owner-lifecycle`）；另外有只读的 1 GiB 全局安全上限和有界 active segment
余量。配置 TTL 时按 sealed segment 清理，而不是假装能逐条准时删除。保留
策略修改只作用于未来 owner；已经 attach 的可复用环境继续使用已绑定的
Effective 策略，直到 clean 或 recreate。`hideout doctor --feature activity`
会显示当前 coverage、owner/全局余量、TTL 状态以及裁剪或损坏。

范围只包括本次启动命令和它在 workload 边界内的后代。无关的 host 进程或
另一个 VM workload 不得归因给该命令。

## 环境停止与清理

修改生命周期前先确认精确身份：

```sh
hideout env list
hideout env inspect <name>
hideout stop --dry-run <environment-id>
hideout stop <environment-id>
hideout clean --dry-run <environment-id>
hideout clean <environment-id>
```

stop 保留环境数据。clean 会删除选中环境的 runtime 和 incarnation 绑定的
观测数据，且不可恢复。活动 session、活动 workspace view、无法证明的 owner
或 stale plan 都会阻止 Apply。

TUI Overview 中按 `e` 会打开同一个精确目标 stop/clean 计划。clean 必须输入
完整 environment ID，不是一个按键就全删的宽泛操作。

## 复核与分享

本地路径是有用证据，因此在本机界面可见。Hideout 无法猜出每个项目路径是否
属于个人敏感信息，所以分享是独立的复核边界：

```sh
hideout support report --out ./hideout-support.json
hideout audit export --source boundary-summary --out ./boundary.json
```

export 会确定性移除已知 Hideout secret、URI userinfo、认证字段、敏感参数或
query 值以及控制面 token。发送前仍需人工复核。完整保真 export 需要单独明确
确认。

可分享 support report 不包含任何 activity record、本地活动路径、命令 argv、
域名、IP 或精确 activity owner ID。Boundary Summary 只携带分类后的观测、
隐私与保留契约。它不会证明空 activity 查询等于“没有行为”；这种结论仍要求
相关子系统在完整时间窗内都是 `Available`。

## 升级与卸载

Homebrew 安装应由 Homebrew 管理：

```sh
brew upgrade vibe-agi/tap/hideout
brew reinstall vibe-agi/tap/hideout
brew uninstall vibe-agi/tap/hideout
```

不要手工修改或删除 Cellar 中的文件。普通 upgrade、reinstall 和 uninstall
会保留 Hideout 持久数据。

验证过的独立安装包使用：

```sh
hideout package verify <prefix>
hideout package repair --prefix <prefix> --dry-run
hideout package uninstall --prefix <prefix> --dry-run
```

如果持久数据也要删除，先预览 purge，再重复精确 store 路径确认：

```sh
hideout package uninstall --prefix <prefix> --purge --dry-run
hideout package uninstall --prefix <prefix> --purge --confirm-purge <exact-store>
```

purge 不可恢复。

## 故障排查

### `secret ref local-proxy is not set`

选中的连接引用了当前 daemon 无法解析的凭据：

```sh
hideout secret set local-proxy
hideout secret status local-proxy
hideout run -- <command>
```

使用隐藏输入，不要再加一个环境变量 export。

### Configuration plan is stale

复核之后另一个客户端改了 profile，旧计划没有被 Apply：

```sh
hideout tui
hideout show connection
```

刷新、复核新 diff，然后确认新签发的 operation。

### Capability is unsupported

当前 daemon 没有为该操作声明可证明的 provider：

```sh
hideout support matrix
hideout version
```

升级 Hideout 或选择已声明的 provider；不要误用 weak-isolation 或 lab flag
绕过安全边界。

### TUI 显示 STALE 或 disconnected

STALE 会保留最后一次事实，但关闭所有修改操作：

```sh
hideout daemon status --human
hideout daemon start
hideout tui
```

只有新的权威 snapshot 能恢复修改能力。

### Activity 为空

先检查 coverage 和精确 workload owner：

```sh
hideout session list
hideout activity coverage --session <id>
hideout activity summary --session <id>
```

Partial 或 Unavailable 不能支撑“什么都没发生”的结论。

### Clean 被阻止

查看精确环境及其 session：

```sh
hideout env inspect <name>
hideout session list
hideout stop --dry-run <environment-id>
```

不要绕过活动 session、workspace view 或 owner 证明 blocker。

### Direct networking 警告

direct 是 setup 默认值，不会隐藏网络出口。运行网络敏感命令前先复核隐私连接：

```sh
hideout help connect
hideout doctor --network tun2socks --proxy-secret <ref> --mediated-resolver <ip>
```

## 开发与自动化路径

先走普通路径，再使用 advanced 目录：

```sh
hideout help all
hideout explain --profile <name> --backend lima -- <command>
hideout doctor --format json
hideout activity summary --json
hideout version --json
```

机器输出需要显式选择；TUI 面向人。Lab 命令不受支持，并要求明确 opt-in。

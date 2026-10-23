# cc-connect ACP 后端切换 V1（本机）

仅适用于现有项目 **tujia-feishu-hermes**。项目名、飞书应用、工作目录、凭据、其他四个项目及 Generic ACP 实现均不变。代码位于 tools/cc-acp-switch.py；入口为 tools/cc-acp。

## 当前五套后端

| 后端 | ACP CLI | extension | 其他 |
| --- | --- | --- | --- |
| Hermes | hermes -p tujia acp | hermes | 首次纳管时保留现有 mode、model_command、compress_command |
| CodeBuddy | /opt/homebrew/bin/codebuddy --acp | generic | 模型目录与 session/set_model 使用标准 ACP；compact 由服务端公告动态启用 |
| QoderCN | ~/.local/bin/qoderclicn --acp | generic | 模型目录与 session/set_model 使用标准 ACP；模型展示用名称，切换用 ID |
| Cline | /opt/homebrew/bin/cline --acp --auto-approve false | generic | 保留人工审批，复用 Generic ACP 的会话级模型列表和切换 |
| Codex | ./tools/cc-codex acp | generic | 使用 ~/.cc-connect/.codex-home 独立认证、模型与会话，通过 codex-acp 连接 |

2026-10-23 在本机核验：CodeBuddy 2.158.0 和 QoderCN 1.1.76 均通过 stdio ACP initialize 握手。Cline 3.0.73 通过 initialize 与 session/new，返回 316 个模型条目、2 种模式和 4 个配置选项。以上预检不能证明模型都可用，也不代表已完成真实 prompt、权限审批、会话恢复或工具调用验收。

## 命令

在 cc-connect 主工作区根目录执行（无独立 worktree）：

~~~sh
./tools/cc-acp status
./tools/cc-acp list
./tools/cc-acp check codebuddy
./tools/cc-acp check qodercn
./tools/cc-acp check cline
./tools/cc-codex login-status
./tools/cc-codex login --device-auth  # 首次独立登录
./tools/cc-acp check codex
./tools/cc-acp use codebuddy --dry-run
./tools/cc-acp use cline --dry-run
./tools/cc-acp use codex --dry-run
./tools/cc-acp use codebuddy --yes
./tools/cc-acp use qodercn --yes
./tools/cc-acp use cline --yes
./tools/cc-acp use codex --yes
./tools/cc-acp use hermes --yes
./tools/cc-acp rollback --yes
./tools/cc-acp recover --yes  # 仅在检测到未完成的切换事务时
~~~

--yes 表示已确认所有 cc-connect 项目没有正在进行的消息/任务；脚本执行的是 launchd 整体服务重启，而不是单独热更换一个 Agent。切换过程中所有 5 个项目短暂中断；不要在其他项目有未完成工作时运行。--dry-run 不修改配置、会话或服务。check 只发送 ACP initialize，不启动 session/new 或 prompt。

不建议未经预检便运行 use。目标 ACP 握手失败时脚本会直接拒绝切换；不会改变 Hermes 配置。

## 会话隔离、备份与回滚

- 在停止 launchd 服务之后，脚本才触碰会话文件。目标是 ~/.cc-connect/sessions/tujia-feishu-hermes_<workdir-hash>.json（会自动匹配旧版 legacy 位置）。会话文件依 backend 保存到 ~/.cc-connect/acp-switch/tujia-feishu-hermes/sessions/<backend>.json。
- 第一次切到 CodeBuddy 时备份当前 Hermes 会话文件；新后端从空的会话文件开始。切回来时恢复之前保存的 Hermes 会话快照，避免同属 agent type=acp 而跨后端恢复错误会话 ID。
- 切换时保留完整配置和目标会话备份：~/.cc-connect/acp-switch/tujia-feishu-hermes/transactions/<timestamp>-<pid>-<nonce>/。私有目录权限 0700；备份文件权限 0600，**含会话历史或敏感配置，请勿提交到 Git**。
- 切换失败会尝试恢复原 config、会话文件并启动旧服务。极端情况下如果 launchd 或文件系统故障导致自动回滚失败，脚本会报告手动恢复备份的位置。不要直接覆盖当前运行中的会话文件。
- 每次切换前会写入私有 pending.json 事务记录。若脚本在完成服务启动与持久化状态之间被意外中断，后续命令会拒绝继续切换；用 recover --yes 从事务备份恢复之前的 config、会话文件、各后端快照、管理状态及服务。
- rollback 的语义是“切回上一次 Agent 后端”，不是 Git revert；本工具不会执行 git commit/merge/deploy。
- 仅管理 ACP 选项 cmd、args、extension、display_name、mode、model_command、compress_command，不改 work_dir、reset_on_idle_mins、Feishu 凭据和其他配置。纳管后如手动改动这些选项，脚本会拒绝覆盖，需先明确处理冲突。
- V1 仅支持单工作区。若启用了 multi-workspace 或留有项目的 *_ws_*.json 会话快照，将拒绝切换，防止跨 ACP 后端恢复错会话。\n- 如果项目曾通过运行时 project-state 设置 work_dir_override，V1 会明确拒绝切换（避免计算出错误会话文件路径）。该限制不影响当前无 override 的 Hermes 项目。 同时兼容 work_dir 中的 ~/ 和 HOME 环境占位符；其他未经核对的环境变量占位符会拒绝切换。
- 当前尚未实现 **针对运行中会话的原子无中断切换**，亦不会自动判断其他项目是否正在执行任务；这是 V1 的明确限制。后续若需要飞书内按会话切换，另行设计 V2。

## 验证

~~~sh
python3.14 -m unittest discover -s tools -p 'test_cc_acp_switch.py' -v
./tools/cc-acp status
./tools/cc-acp use codebuddy --dry-run
./tools/cc-acp use qodercn --dry-run
~~~

切换后的实测顺序：cc-acp status；飞书普通对话；/models；选择模型并验证回复；/compact（只有 ACP 服务端正式公告 compact 时开放，须核验真实历史压缩）；工具权限与会话恢复；切回 Hermes 并测试 /models 的列表。五个后端均提供 check 预检，但真实对话与权限审批验证是独立验收门禁。

## Generic ACP 模型与压缩（2026-10-23）

- CodeBuddy 2.158.0 与 QoderCN 1.1.76 的实际 ACP `session/new` 返回 `models.availableModels`，分别有 20、11 个模型，两个后端的当前模型 `session/set_model` 同 ID RPC 均成功。
- cc-connect 在 `session/new|load` 缓存模型 ID、显示名、描述；每个存活会话保留自己的当前模型。飞书 `/models` 采用显示名，模型切换发送真实 `modelId`，RPC 失败不报成功；Hermes 仍走专属扩展。
- CodeBuddy 的 ACP `available_commands_update` 确认公告 `compact`。Generic 只有收到公告后才开放 `/compact`；不依赖厂商名称和额外的预设配置。
- 隔离空会话 `/compact` 可返回协议 `end_turn`，但非空历史测试尚未完成一个可验证的初始回合，**真正减少上下文 token 尚未验收**。飞书实测前不得声称完整压缩 PASS。
- 飞书脚注请给 `Ctx` 加空值判断；目标项目的 `footer_template` 必须包含 `{{if .Ctx}} · ctx {{.Ctx}}%{{end}}`，不能直接固定拼接 `ctx {{.Ctx}}%`，否则 QoderCN 缺上下文数据时仍显示 `ctx %`。

## 上下文压缩补充排查（2026-10-23）

- CodeBuddy 2.158.0 的隔离 ACP `available_commands_update` 包含 `compact` 与 `_compact`，其原生手动压缩命令存在，但飞书 `/compress` 曾长时间停留在压缩中。
- 找到 cc-connect 事件背压问题：`runForwardedCommand` 原来同步等待 `session/prompt` 返回，随后才读取事件；ACP Session 的 128 条事件缓冲区满时，读循环会阻塞，从而无法继续读取 JSON-RPC 的完成响应。已用 200 条同步事件故障注入复现，修复为异步发送同时实时消费；发送失败也立即回报。
- 新增压缩绝对上限 5 分钟（与其他普通 Agent 消息的超时分离），超过后明确报错并清理卡住的会话，而不是继续等待默认的 2 小时空闲超时。该上限是安全兜底，不代表实际压缩已成功。
- QoderCN 1.1.76 的 `available_commands_update` 未公告手动 `compact`。官方 ACP 文档列出「上下文压缩」支持，但当前 ACP 斜杠命令列表未包含手动压缩。因此应区分「自动上下文压缩机制支持」与「用户主动通过 ACP 斜杠命令压缩」：暂不向 QoderCN 暴露手动按钮，不能按 CLI 交互模式的 `/compact` 支持情况猜测 ACP 行为。
- 以上修复仍需真实 CodeBuddy 长会话、飞书压缩回合和压缩前后上下文用量复测，不把单元测试当作终端验收。

## Cline ACP 扩展（2026-10-23）

- 本机实际 CLI：`/opt/homebrew/bin/cline`，版本 `3.0.73`。官方使用 `cline --acp` 提供 stdio ACP，认证数据仍由 Cline 自身管理；预设不会修改 `~/.cline` 的认证和模型设置。
- 新预设 `--acp --auto-approve false` 明确保留人工审批，不依赖 Cline CLI 的自动批准默认值；不要未经评估改成 true。
- 模型列表、显示名和会话切换复用 Generic ACP 的 `models.availableModels`、`session/set_model`；压缩仍根据实际 ACP `available_commands_update` 决定是否开放。**未公告就不声称手动压缩受支持。**
- 旧版 V1 的 `state.json` 只有三种预设。新脚本在 `status/list/check/dry-run` 时**仅在内存补充 Cline**，旧状态和配置文件保持原样；首次真正 `use cline --yes` 成功后才持久化新预设。Cline 独立使用 `sessions/cline.json`，不复用其他 Agent 的 ACP Session ID；`rollback --yes` 仍可切回上一后端。
- 切换会重启整个 cc-connect launchd 服务，影响全部 5 个项目；本次仅部署切换工具能力，不主动切换正在运行的 CodeBuddy。

## Codex ACP 扩展（2026-10-23）

- Codex CLI 本身不提供原生 `--acp`；通过 `./tools/cc-codex acp` 启动本机已安装的 `@agentclientprotocol/codex-acp 2.2.2`。它将 ACP stdio 转发到 Codex App Server，**后续改用独立 CODEX_HOME 和独立认证，不再复用桌面版**。不要将 `acpx codex` 客户端误当 ACP 服务端。
- 已在 Mac 上对真实进程完成隔离验证：`initialize` 和 `session/new` 都通过，`models.availableModels` 共有 40 个模型，4 种 ACP 模式（read-only、workspace-write、agent、agent-full-access），模型的 `session/set_model` 同 ID 调用成功。
- 适配器默认启动模式实测为 `agent`；本预设未添加全权限参数，也未绕过审批。Codex 可通过 ACP mode/configOption 改变运行模式，实际文件变更审批需要首次真实飞书任务测试后验收。新 Home 使用自己的 config.toml，adapter 默认使用 npm 附带的兼容 Codex 二进制。
- 现有 Generic ACP 模型目录和会话内模型切换复用此预设；手动 `/compact` 只有该后端的 ACP 命令公告确实包含 `compact` 时才开放，不能以适配器文档列出 slash 命令代替运行时验证。
- V1 状态兼容：原有四后端状态仅在内存中补新 Codex 预设；从旧共享 Home 迁移时，将旧线程另存 `sessions/codex.json`，独立 Home 的线程另存 `sessions/codex-isolated.json`，避免跨 Home 恢复 ACP 会话 ID。
- `cc-acp use codex --yes` 会停止并重启整个 cc-connect，可能中断 5 个项目。当前仅安装了切换功能，没有更改正在运行的 CodeBuddy 服务。

## cc-connect 独立 Codex Home（2026-10-23）

固定位置：`~/.cc-connect/.codex-home/`，面向**整个 cc-connect**，不是某一飞书项目的 `acp-switch` 私有目录。其他 cc-connect Codex 任务可设置 `CODEX_HOME=$HOME/.cc-connect/.codex-home` 直接共用它。权限：目录 0700、独立 `config.toml` 0600，不在 Git 中维护其文件。

- 独立配置：默认模型 `gpt-6-luna`、思考等级 `medium`、`sandbox_mode = "workspace-write"`；后续改这里不影响桌面版 `~/.codex/config.toml`。
- 认证**完全独立**：没有复制、软链接或导入桌面版 `~/.codex/auth.json`。首次需在 Mac 终端运行 `cc-codex login --device-auth`，按 CLI 给出的设备授权说明完成登录，再运行 `cc-codex login-status` 复核。不要共享或复制认证文件。
- 通用入口 `~/.local/bin/cc-codex` 指向项目的启动包装器；`cc-codex acp`、`cc-codex login`、`cc-codex login-status` 均固定使用上述 CODEX_HOME。
- 对已经在线使用旧版共享 Home 的 Codex，`cc-acp status/list/check/dry-run` 不修改运行态。独立登录后，`cc-acp use codex --dry-run` 将显示 `codex -> codex (isolate Codex Home)`；执行 `cc-acp use codex --yes` 才通过事务停止服务、备份旧会话到 `sessions/codex.json`，然后在独立 Home 新建会话。中断可用 `recover --yes` 恢复旧运行态。`rollback --yes` 切回前一个**其他**后端。
- 独立登录之前一律拒绝真实 Codex 切换，避免切到未经认证的服务。旧 Codex Desktop 中已出现的历史会话不会自动删除。

### cc-codex 作为终端 Codex CLI（2026-10-23）

`cc-codex` 已是原生 Codex CLI 的通用包装器。无参数启动交互模式，其余参数（例如 `-m`、`-c`、`-C`）原样转交给 Codex：

```bash
cc-codex                              # 终端交互，默认 gpt-6-luna (medium)
cc-codex -C /path/to/project          # 在指定项目根目录启动
cc-codex exec "检查当前项目问题"         # 非交互运行
cc-codex exec -C /path/to/project "检查代码"
cc-codex resume --last                # 恢复独立 Home 的最近会话
cc-codex review --uncommitted         # 独立 Home 下进行审查
cc-codex -m gpt-6-sol                 # 仅本次覆盖模型
cc-codex login-status                 # 查询独立认证
cc-codex acp                          # ACP stdio 入口
```

所有命令均导出 `CODEX_HOME=$HOME/.cc-connect/.codex-home`，不修改 `HOME`。独立目录 `config.toml` 当前默认模型为 `gpt-6-luna`、思考等级 `medium`；修改这里不影响桌面版本。`-C` 指工作目录而不是 Codex Home；`resume` 访问独立会话存储。此前共享 Home 的飞书历史会话不会自动迁移或删除。

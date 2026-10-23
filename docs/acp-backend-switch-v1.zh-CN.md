# cc-connect ACP 后端切换 V1（本机）

仅适用于现有项目 **tujia-feishu-hermes**。项目名、飞书应用、工作目录、凭据、其他四个项目及 Generic ACP 实现均不变。代码位于 tools/cc-acp-switch.py；入口为 tools/cc-acp。

## 当前三套后端

| 后端 | ACP CLI | extension | 其他 |
| --- | --- | --- | --- |
| Hermes | hermes -p tujia acp | hermes | 首次纳管时保留现有 mode、model_command、compress_command |
| CodeBuddy | /opt/homebrew/bin/codebuddy --acp | generic | 模型目录与 session/set_model 使用标准 ACP；compact 由服务端公告动态启用 |
| QoderCN | ~/.local/bin/qoderclicn --acp | generic | 模型目录与 session/set_model 使用标准 ACP；模型展示用名称，切换用 ID |

2026-10-23 在本机核验：CodeBuddy 2.158.0 和 QoderCN 1.1.76 均通过 stdio ACP initialize 握手。这只证明基础协议握手，不证明 session/new、prompt、permissions、session/load 或工具调用都可用。正式切换后需在飞书实测。

## 命令

在 cc-connect 主工作区根目录执行（无独立 worktree）：

~~~sh
./tools/cc-acp status
./tools/cc-acp list
./tools/cc-acp check codebuddy
./tools/cc-acp check qodercn
./tools/cc-acp use codebuddy --dry-run
./tools/cc-acp use codebuddy --yes
./tools/cc-acp use qodercn --yes
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

切换后的实测顺序：cc-acp status；飞书普通对话；/models；选择模型并验证回复；/compact（只有 ACP 服务端正式公告 compact 时开放，须核验真实历史压缩）；工具权限与会话恢复；切回 Hermes 并测试 /models 的列表。三个后端均可通过 check，但真实对话验证是独立验收门禁。

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

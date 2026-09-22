# HANDOFF — cc-connect

更新时间：2026-09-13。仓库：origin=kleinlsl/cc-connect（fork，已迁往 PineLu/cc-connect，push 有重定向提醒但可用），upstream=chenhg5/cc-connect（主仓库）。

## 当前目标
1. **【已提交·未验证未部署】`/models` 内置命令**（提交 `d96e5972`，10 文件 +969/−14）：
   `core/interfaces.go` 新增 `ModelLister`/`ModelDetail`；`core/engine.go` 内置 `/models`
   （卡片下拉选中直切，无卡片平台降级文本）；`agent/acp/model_list.go` 读 Hermes
   profile 配置+缓存（离线可用）；`agent/claudecode` 包 `AvailableModels()`；footer
   切后即时刷新（`acpSession.SetModel`）；`runForwardedCommand` 改返回 `(string,error)`。
   回归测试 3 项全绿。**二进制尚未编译部署**（当前运行仍是 16:13 版），已 push（2026-09-13）。
   详见知识库 `cc-connect-knowledge.md` §十二。
2. **【已修复并上线】问题 A**：Hermes(ACP) 长任务结束后「最终回复在飞书发两遍」。三层修复 + 回归测试完成，已上线（提交 `6157c133`）。
3. **【已部署·待实测】问题 B**（已提交 `aba8824c`）：ACP 状态行 `in` 显示会话累计值（in 21.1M 超 1M 窗口）。
   修复方式：`ContextUsage` 加 `CumulativeInputTokens` 标记，ACP 置位，footer 的 `in` 改用 `UsedTokens`、`cw/cr` 省略。
   **已编译 arm64 并重启部署（PID 7830，2026-09-10 23:14:02 启动）**，待飞书给 hermes-tujia 发长任务消息实测。
4. **【已部署·已提交】`stripModelFillerLines` 过滤**（`53fc21ea` + 注释修复 `d7adf66a`，与问题 B 同一次部署）：
   部分模型（实测 muse-spark / provider=opencode-free，低频偶发）在 tool call 间吐 U+25FC "◼" 进度符号，污染飞书可见消息。
   过滤纯 ◼ 行（入口 `buildReplyContent`、`buildCardJSON`），日志/存档保留原始内容。
   **注意**：当前日志里能查到的 6 个 ◼ 均为用户自己发的飞书消息原文（2026-09-09 讨论本问题时的发言），不是模型输出残留；
   新二进制上线后再出现泄漏才算真触发。
5. 工作区干净。**【已上线】sync-upstream-0913**（提交 `3de24d79`，12 文件 +1426/−70，已推远端，已编译部署）：
   上游 3 commit（`3a6534d5` 飞书回执 reaction + `312144c2` codex stdio + `757b4df0` cron 睡眠钳制）。
   `feishu.go` 1 文件冲突（replyContext 结构体 + dispatch 入口），解法：字段全保留，dispatch 经新增
   `dispatchWithReceiptAck` 让文本 ack 与 reaction 共存；`sendAckAndGetThreadID` 加 `client nil` 保护。
   上游 receipt 测试 13 FAIL + 1 hang：根因是分支文本 ack 同步发 reply，抢了 mock 的一次性 `close(started)`；
   修法是 mock 加 reply 路径放行（5 处，`receipt_ack_test.go`），产品逻辑不动。全量 `go test ./platform/feishu/...` 绿（65s）。
   线上 hermes-tujia 已配 `ack_emoji = "Get"`，回执 reaction 飞书实测生效。

## 当前分支
`sync-upstream-0913`（本次 sync 分支，HEAD=`3de24d79`，已 push）。`feat/strip-model-filler`、`sync-upstream-0905`、`main` 均已 push（2026-09-13）。

## 当前 commit
- HEAD=`3de24d79`（`sync-upstream-0913`）。本波新增：
  - `1603e948` docs: HANDOFF push 状态同步（`feat/strip-model-filler` 上）
  - `3de24d79` merge: upstream/main 2026-09-13（12 文件，receipt reaction + codex stdio + cron sleep）
- 之前：HEAD=`96d2a0ee`。本波 4 个提交：
  - `aba8824c` fix(acp): 状态行 in 改用上下文占用（问题 B，6 文件 +213/−18）
  - `d7adf66a` docs(feishu): stripModelFillerLines 注释断句笔误修复
  - `53fc21ea` feat(feishu): 过滤模型进度符号 ◼ 纯符号行（feishu.go +37 / feishu_test.go +30）
  - `96d2a0ee` docs: 交接文档同步
- 更早：`4df07e5e` footer_template ctxPct 修复；`6157c133` Hermes-over-ACP 支持 + outbox 重复发送根治（问题 A）；`9212f834` 上游 merge（v1.5.1-beta.1）。
- 二进制 `cc-connect-arm64` 现为 47.7M（`-s -w` 剥离后）；旧版 69.1M 备份两份
  （`.bak-20260909-174919` / `.bak-20260910_231332`），确认稳定后可清理。

## 已完成
1. **上游同步**（9212f834，33 文件）：feishu.go 我方 ack/thread 隔离/allow_p2p/卡片超链接/话题回显与上游群聊历史、大资源 Range 下载均保留。
2. **ack 开关** `ack_show_session_key`（默认 true；hermes-tujia 配 false 隐藏 `[session:..]`），含回归测试，已上线。
3. **ACP Hermes 回复状态行**（已上线）：`agent/acp/session.go` 报 usage/model；`core/engine.go` 方法版 `buildClaudeStatusLineFooter`（无 cw/cr 时省略该段，Claude 零变化）；文档 4.12/4.13。
4. **问题 A：outbox 重复发送根治（三层，已上线）**
   - **根因**：即时发送（sendFinalWithOutbox）与后台 replayer（sweep）是两个并发 goroutine，对同一回复无互斥、无幂等，只靠 Add 时 `NextAt=now+30s` 初始 grace 时间窗防撞；Hermes 长 turn + 本机慢发送跨过该窗，sweep 用独立路径再发一次，飞书无幂等去重 → 两条。样本 ob id `ob-8bd5b95834567ce1`。
   - **第 1 层 进程内互斥（`core/outbox.go`）**：OutboxItem 加 `immediateHeld bool json:"-"`；Add 即由即时路径持有，`dueLocked` 跳过 held 项（与发送耗时解耦，不再赌时间窗）；即时路径可重试失败 `Fail` 才释放交接 replayer，成功 `Complete` 删除；held **不持久化**，进程崩溃重启自动释放、恢复补发不受影响。
   - **第 2 层 平台幂等（飞书 uuid）**：OutboxItem 加持久化 `UUID`（Add 时 `uuid.NewString()`）；新增 `core/outbox_ctx.go`（`WithOutboxUUID`/`OutboxUUIDFromContext`）；engine 即时路径与 replay 路径都把同一 uuid 注入 ctx；`platform/feishu/feishu.go` 在 `replyMessage`/`createMessage` 底层出口把 uuid 设进请求体 `body.Uuid`（飞书相同 uuid 1h 内至多成功一次）。非 outbox 发送 ctx 无 uuid，行为不变。
   - **第 3 层 日志 + 回归测试**：Add 补 `outbox: final reply parked for immediate send`（id/uuid/platform/session）；新增单测见下。
5. **顺带根治测试关停竞态（被 outbox 放大暴露）**：`Engine.Stop` 原先 cancel 后不等待后台 goroutine，测试 `t.TempDir` 清理时仍有写盘 → 偶发 `TempDir RemoveAll: directory not empty`（功能断言从不受影响、生产无此场景）。三处治理：
   - `core/outbox.go` 新增 `Stop()`（内部 runCtx/runDone，停 replayer 并等其退出；stopped 后 persist/remove 跳过磁盘 I/O）。
   - `core/engine.go` 加 `turnWg sync.WaitGroup`，3 处 `go processInteractiveMessageWith` 全部纳入；`Stop` 顺序=cancel→`sessions.StopPersistence()`→停 platform→关 agent session→`turnWg.Wait()`→…→最后 `outbox.Stop()`。
   - `core/session.go` 加 `StopPersistence()`（stopped 后 `saveLocked` 直接 no-op，任何后台 goroutine 都无法在关停后写 sessions.json）。
   - `core/cuj_test.go`：13 处 `NewEngine` 统一补 `t.Cleanup(func(){ x.Stop() })`（**注意：在 newCUJEnv 这类 helper 里必须用 t.Cleanup 而非 defer，defer 会在 helper 返回时就 Stop，导致 engine 当场报废、所有用例收不到回复——已踩过并修正**）。
6. **问题 B：ACP 状态行 `in` 累计虚高（已提交 `aba8824c`，待实测）**
   - **结论（已核实上游源码）**：不是 bug，是 ACP 协议口径。`acp/schema.py::Usage` 明确写
     `inputTokens` = "Total input tokens **across all turns**"；Hermes `turn_finalizer.py` 取
     `agent.session_prompt_tokens`，该计数器在 `turn_usage.py` 只 `+=`、`agent_init.py` 初始化后
     **整个会话从不重置**。长 turn 累加破窗是必然。`cachedReadTokens/WriteTokens` 同源累计，且是 prompt 的子集。
   - **为何 ctx% 正常**：占用走另一条通道 `usage_update.used`（`server.py::_build_usage_update` 用
     `_estimate_tokens(history)` 估算当前上下文），与 prompt usage 互不覆盖。
   - **采用方案 A（不改 ~/.hermes 第三方源码）**：cc-connect 侧标记 + 换口径。
     - `core/interfaces.go`：`ContextUsage` 新增 `CumulativeInputTokens bool`。
     - `agent/acp/session.go::absorbPromptUsage`：吸收 prompt usage 时置位。
     - `core/engine.go` 两处渲染：`in` 改用 `UsedTokens`；`cw/cr` 整段省略；累计且无 `UsedTokens`
       时 `in`/`ctx%` 均不渲染（fallback 求和会凭空编造占用，故限定 `!CumulativeInputTokens`）；
       `out` 保留（输出是真实新增，非重复计数）。
     - 单测 3 个：`_CumulativeInputUsesContextSize` / `_WithoutInputTokensSkipsIn` / `_SkipsCacheTierGrouping`；文档 §4.13.1。
   - **验证**：`go build ./...` OK；`gofmt` 干净；`go test ./core/ ./agent/acp/ -count=1` 全绿；已编译部署。
   - 已明确「不改」的相邻项：压缩边界 7%↔47% 瞬时错位（用户拍板不改），与本题无关。

## 未完成
### 1. 端到端实测（需用户在飞书手动触发）
- 给 **hermes-tujia** 项目发一条**长任务**（5 分钟以上）消息，确认：
  - 问题 B：状态行 `in` 不再破窗（原 `in 21.1M`，应变为当前上下文量级，如 `in 471.0k`）；
  - 问题 A：最终回复**只回一条**（可 grep `~/.cc-connect/logs/cc-connect.log` 的 `outbox:` 与 uuid）；
  - ◼ 过滤：回复中不再出现纯 ◼ 行。
- 短消息可能触发不到累计虚高，需真实长 turn。

### 2. 推送 / 仓库卫生（2026-09-13 已 push）
- 本地提交均已推远端（`sync-upstream-0913` + `feat/strip-model-filler` + `sync-upstream-0905` + `main`）。
- GitHub 提醒 origin 已迁移 `kleinlsl/cc-connect` → `PineLu/cc-connect`，当前 remote 仍可用（重定向），空闲时可改 remote URL。
- **切勿 `git add cc-connect-arm64*`**（二进制不入库，已在 .gitignore）。
- 清理二进制备份 `cc-connect-arm64.bak-*`（确认新版稳定后）。

## 验证命令
```bash
go build ./... && go vet ./core/ ./platform/feishu/
go test ./... -p 1
go test ./core/ -run TestCUJ -count=2
# 本波实际使用的交叉编译（no_web 标签 + 剥离符号）
CGO_ENABLED=0 GOARCH=arm64 go build -trimpath -tags 'no_web' -ldflags="-s -w" -o cc-connect-arm64.new ./cmd/cc-connect
# 部署：cp -p 备份 → mv .new 原子替换 → xattr -c，然后：
launchctl unload ~/Library/LaunchAgents/com.cc-connect.service.plist && sleep 2 && launchctl load ~/Library/LaunchAgents/com.cc-connect.service.plist
grep "is running" ~/.cc-connect/logs/cc-connect.log   # projects=3
```

## 验证结果
- **2026-09-13（sync-upstream-0913，已上线）**：main fast-forward 同步上游 3 commit 无冲突；
  `feishu.go` 冲突解完 `go build ./...` + `go vet` + `gofmt` 全干净；`go test ./core/...` 绿（19.7s）；
  `go test ./platform/feishu/...` 全绿（65s，含上游新增 receipt 7 用例）；交叉编译 arm64（50.3M）
  原子替换后重启，16:24:39 `cc-connect is running`，projects=3；hermes-tujia 配 `ack_emoji="Get"` 后飞书回执 reaction 实测生效。
- **2026-09-10（本波）**：`go build ./...` OK；`gofmt` 仅动过文件干净；`go test ./core/ ./agent/acp/ -count=1` 全绿；
  `go test ./platform/feishu/ -run TestStripModelFillerLines` 通过。交叉编译 arm64 成功（47.7M，Mach-O arm64），
  原子替换后重启，PID 7830，23:14:02 `cc-connect is running`，飞书消息正常接收处理。
- **2026-09-06（历史，问题 A 波次）**：`go test ./... -p 1` EXIT=0，45 包全 ok；CUJ count=2 全过；
  交叉编译后 PID 5849 上线。

## 风险 / 注意事项
- outbox 原则：「不丢」优先于「不重」；问题 A 是在保留 at-least-once 落盘补发骨架的前提下补全互斥+幂等，不是推翻。
- `Engine.Stop` 现在会 `turnWg.Wait()` 等待在途消息处理收敛（由 cancel + 关 agent session 驱动退出）；若未来新增不响应 ctx 的处理 goroutine，需一并纳入 turnWg，否则 Stop 可能变慢。
- 重启会 kill 在跑 hermes/claude 子进程，挑群里无在跑会话时。
- **不改 `~/.hermes` 第三方源码**（用户明确）。
- 日志盲区：cc 运行期看不到 Hermes 逐行 API call；Hermes 侧看 `~/.hermes/profiles/tujia/logs/agent.log` 与只读 `state.db`。
- **当前 3 个 project**（以 `~/.cc-connect/config.toml` 为准）：`workspace`=claudecode、`tujia-feishu-agent-claude`=claudecode、`hermes-tujia`=acp。
  （旧记录写的 `opencode-workspace`=acp 已不在配置中，本波启动日志 `projects=3` 与此一致。）
- 关键路径：plist `~/Library/LaunchAgents/com.cc-connect.service.plist`；运行配置 `~/.cc-connect/config.toml`（含 secret，不入库）；socket `~/.cc-connect/run/api.sock`；outbox 目录 `~/.cc-connect/outbox/`（正常应为空）。
- 遗留运维：飞书 Hermes 应用 cli_aaf783e8d5b99d1d 需补 `im:message.group_msg` 并发版（否则收不到不 @ 群消息，API 230027）；本机 :3003 网关 prefill 慢/偶发缓存不命中 cc 侧无解；1M 长上下文需网关侧加 [1M] 渠道。
- 任何文档/提交不得写入 app_secret 等凭证明文。

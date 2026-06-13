# Empty Tool Name Conversion Chain Test Report

日期：2026-06-13

分支：`codex/fix-empty-tool-name-state`

PR：<https://github.com/kittors/CliRelay/pull/430>

## 结论

本轮已经完成针对“OpenAI Chat Completions 空/空白 `function.name` 转 Claude `tool_use`”问题的系统分析、功能测试、接口级测试、场景链路测试和全仓回归。

当前修复不是宣称数学意义上的“绝对保证”。更准确的结论是：本项目内与该问题相关的转换边界、SDK 注册链路、executor HTTP 接口链路、历史回放链路和全仓回归均已覆盖并通过；真实第三方供应商未来可能输出新的非标准事件形态，仍需要后续按样本补充回归。

## 问题范围

风险链路：

1. 上游 OpenAI-compatible stream/non-stream 响应返回 `tool_calls`，但 `function.name` 为空字符串或空白字符串。
2. 转换器若仍创建 Claude `tool_use`，会产生非法 `tool_use.name`。
3. 转换器若没有创建 `content_block_start`，但仍输出 `input_json_delta` 或 `content_block_stop`，会产生孤儿 content block 事件。
4. 只有空工具名时，如果仍把 OpenAI `finish_reason=tool_calls/function_call` 映射成 Claude `stop_reason=tool_use`，客户端会以为需要执行工具，但响应里没有合法工具块。
5. 下一轮 Claude 历史回放时，空 `tool_use` 可能被转成 OpenAI `tool_calls`，对应 `tool_result` 可能变成没有前置调用的孤儿 `role=tool` 消息。

## 修复策略

响应侧：`internal/translator/openai/claude/openai_claude_response.go`

- `ToolCallAccumulator` 增加 `Started` 与 `BlockIndex` 状态。
- 只有 `strings.TrimSpace(function.name)` 非空时，才分配 Claude content block index 并发送 `content_block_start`。
- `arguments` 可以先缓存；等后续 chunk 收到有效 name 后再补发合法 `input_json_delta`。
- `finalizeToolContentBlocks` 只结束已经 `Started` 的工具块，不为未启动的空工具名调用发送 `input_json_delta` 或 `content_block_stop`。
- `tool_calls/function_call` finish reason 只有在实际发出有效 `tool_use` 时才映射为 `tool_use`；否则映射为 `end_turn`。
- 非流式路径同样跳过空/空白 `function.name`，并用有效工具存在性决定 `stop_reason`。

请求侧：`internal/translator/openai/claude/openai_claude_request.go`

- Claude `tool_use.name` 为空或空白时，不转成 OpenAI `tool_calls`。
- 用 `validToolUseIDs` 记录真正保留的工具调用 ID；对应 `tool_result` 只有命中该集合时才转成 OpenAI `role=tool`。
- `tools[].name` 为空或空白时跳过。
- `tool_choice.type=tool` 但 `name` 为空或空白时降级为 `auto`。

## 参考项目对比

参考源码为 2026-06-13 浅克隆快照：

- `QuantumNous/new-api`：`51475c8`
- `Wei-Shaw/sub2api`：`e34ad2b`

`new-api` 观察结论：

- `service/convert.go` 的 OpenAI stream -> Claude 逻辑会在首个 tool chunk 直接用 `toolCall.Function.Name` 创建 `tool_use`。
- 中间 chunk 分支只在 `toolCall.Function.Name != ""` 时发送 `content_block_start`，但 `Function.Arguments` 非空时仍会发送 `input_json_delta`。
- 结束时 `stopReasonOpenAI2Claude(info.FinishReason)` 仍按 `tool_calls -> tool_use` 映射。
- 非流式 `ResponseOpenAI2Claude` 也直接把 `toolUse.Function.Name` 写入 Claude `tool_use`。
- 未检索到针对空/空白工具名的专门测试。

`sub2api` 观察结论：

- `backend/internal/pkg/apicompat/responses_to_anthropic_request.go` 对 `tool_use/tool_result` 邻接关系有系统修复，会丢弃无匹配关系的孤儿工具结果。
- `backend/internal/pkg/apicompat/chatcompletions_responses_bridge.go` 在 ChatCompletions stream -> Responses stream 时，首次看到 `tool_calls` 就创建 `function_call` item，`Name` 取当时的 `stored.Function.Name`；如果 name 尚未到达或为空，会把空 name 带入后续 Responses 事件。
- `backend/internal/pkg/apicompat/responses_to_anthropic.go` 把 `function_call` 直接转成 Claude `tool_use`，未看到空 name 门禁。
- 未检索到针对空/空白工具名的专门测试。

对本项目的启发：

- 只修邻接关系不够；空工具名要在“工具块真正启动”的状态边界处理。
- 只在 `content_block_start` 上判空也不够；必须同时处理 `input_json_delta`、`content_block_stop` 和 `stop_reason`。
- 请求侧必须连同历史回放一起修，避免下一轮请求重新制造非法 `tool_calls` 或孤儿 `tool` message。

## 功能测试

文件：`internal/translator/openai/claude/openai_claude_response_test.go`

覆盖点：

- 流式空 `function.name` 不产生 `tool_use`、`input_json_delta`、孤儿 `content_block_stop`。
- 流式空白 `function.name` 同样跳过。
- 文本后接空工具名时，保留文本，跳过非法工具。
- usage-only chunk 延后到达时，`stop_reason` 仍为 `end_turn`，且只发送一次 `message_stop`。
- `arguments` 先到、有效 `name` 后到时，缓存参数并输出完整合法工具块。
- 有效工具和空工具混合时，只保留有效工具。
- 非流式 message-level `tool_calls` 空名跳过，`stop_reason=end_turn`。
- 非流式 content-array `tool_calls` 空名跳过，`stop_reason=end_turn`。

文件：`internal/translator/openai/claude/openai_claude_request_test.go`

覆盖点：

- Claude 空 `tool_use.name` 不转成 OpenAI `tool_calls`。
- 被跳过的 `tool_use` 对应 `tool_result` 不转成孤儿 OpenAI `role=tool`。
- 有效 `tool_use` 与匹配 `tool_result` 保持正常回放。
- 空/空白 `tools[].name` 被跳过。
- 空白 `tool_choice.name` 降级为 `auto`。

执行结果：

```text
rtk go test ./internal/translator/openai/claude -count=1 -v
Go test: 32 passed in 1 packages
```

## 场景链路测试

文件：`test/openai_claude_empty_tool_chain_test.go`

该测试不直接调用包内函数，而是走 public `sdktranslator` registry，覆盖更接近实际使用的转换注册链路。

覆盖点：

- OpenAI 流式空 `function.name` -> Claude SSE：不产生非法工具块，`stop_reason=end_turn`。
- Dirty Claude 历史回放 -> 下一轮 OpenAI 请求：不产生空 `tool_calls` 或孤儿 `role=tool`。
- `arguments` 先到、有效 `name` 后到：合法工具保留，历史回放也保留。
- 有效工具和空白工具混合：只保留有效工具。
- 非流式空工具名：不产生 Claude `tool_use`，`stop_reason=end_turn`。

执行结果：

```text
rtk go test ./test -run 'TestOpenAIClaude.*Tool.*Chain|TestOpenAIClaudeEmptyToolNameNonStreamingChain' -count=1 -v
Go test: 4 passed in 1 packages
```

## 接口级测试

文件：`internal/runtime/executor/openai_compat_executor_compact_test.go`

新增测试：

- `TestOpenAICompatExecutorClaudeStreamSkipsEmptyToolNameEndToEnd`
- `TestOpenAICompatExecutorClaudeNonStreamSkipsEmptyToolNameEndToEnd`

该组测试通过真实 `OpenAICompatExecutor` 和 `httptest.NewServer` mock OpenAI-compatible `/v1/chat/completions`，覆盖 executor 层的入站、出站和响应转换：

1. 入站 Claude dirty history 包含空/空白 `tool_use.name` 与对应 `tool_result`。
2. executor 转给 mock OpenAI 上游的请求中，不允许出现空 `tool_calls` 或孤儿 `role=tool`。
3. mock 上游返回空 `function.name` 的 OpenAI stream/non-stream 响应。
4. executor 转回 Claude 响应时，不允许出现非法 `tool_use`、孤儿 `input_json_delta` 或孤儿 `content_block_stop`。
5. 空工具名-only 响应必须落到 `stop_reason=end_turn`。

执行结果：

```text
rtk go test ./internal/runtime/executor -run 'TestOpenAICompatExecutorClaude(Stream|NonStream)SkipsEmptyToolNameEndToEnd' -count=1 -v
Go test: 2 passed in 1 packages
```

## 回归测试

包级回归：

```text
rtk go test ./internal/translator/... -count=1
Go test: 107 passed in 30 packages

rtk go test ./test -count=1
Go test: 274 passed in 1 packages

rtk go test ./internal/runtime/executor -count=1
Go test: 219 passed in 1 packages
```

全仓回归与静态检查：

```text
rtk go test ./... -count=1
Go test: 2037 passed in 147 packages

rtk go vet ./...
Go vet: No issues found

rtk go build ./...
Go build: Success

rtk git diff --check
passed
```

## 未覆盖边界

- 未连接真实第三方供应商账号做线上流式采样；本轮用确定性 mock 覆盖协议形态，避免真实供应商输出波动导致测试不可复现。
- 未修改 OpenAI Responses API 与 Gemini/Bedrock 等无关转换链路；本问题限定在 OpenAI Chat Completions 与 Claude Messages 互转链路。
- 不能证明未来所有非标准 provider 输出都安全；如果后续出现新的 chunk 顺序或字段形态，需要把真实样本补成回归测试。

## PR 与合并状态

PR #430 当前 base 为 `dev`。普通 build 已通过；但仓库策略 `translator-path-guard / ensure-no-translator-changes` 会阻止 PR 修改 `internal/translator/**`，因此该 PR 不能直接按普通策略合并。该阻塞不是测试失败，而是仓库治理策略。

后续选项：

- 维护者临时批准 translator 路径变更后合并 PR #430。
- 或按维护 issue #431 先调整治理策略，再合并。
- 或由维护者在受信任分支接管同等修复并合并。

# Empty Tool Name Conversion Chain Test Report

日期：2026-06-13

分支：`codex/fix-empty-tool-name-state`

PR：<https://github.com/kittors/CliRelay/pull/430>

## 结论

本轮已经完成针对“OpenAI Chat Completions 空/空白 `function.name` 转 Claude `tool_use`”问题的系统分析、功能测试、接口级测试、场景链路测试、高保真模拟测试、不变量生成测试和全仓回归。

当前修复不能宣称覆盖未来所有第三方 provider 可能发明的新协议形态；但对本项目当前声明支持的 OpenAI Chat Completions ↔ Claude Messages 相关状态空间，已经把“不得出现空工具名、不得出现孤儿工具 delta/stop、不得误报 `stop_reason=tool_use`、不得在下一轮回放产生孤儿 `tool` message”转成不变量，并用生成测试覆盖通过。

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

## 高保真模拟设计

由于当前没有真实第三方供应商账号和可复现线上样本，本轮没有停留在普通 mock，而是根据 `new-api` 与 `sub2api` 的状态机行为推理出更接近真实风险的输入形态：

- `new-api` 风险形态 A：首个 tool chunk 已经包含 `arguments`，但 `function.name` 缺失或为空；结束 chunk 仍给 `finish_reason=tool_calls`。
- `new-api` 风险形态 B：代码只在 `content_block_start` 上看 name，但 arguments 分支独立执行，因此可能出现没有 start 的 `input_json_delta`。
- `sub2api` 风险形态 C：ChatCompletions bridge 首次看到 `tool_calls` 就创建后续工具状态；如果 name 当时尚未到达，后续可能携带空 name 继续进入 Responses/Anthropic 转换。
- 并行工具风险形态 D：空工具调用在 `index=0`，有效工具调用在 `index=1`；修复不能因为跳过空 index 而破坏有效并行工具。

对应新增模拟：

- 转换器层新增 `TestOpenAIStreamingToolArgumentsWithoutValidNameSkipped`：arguments 到达但有效 name 永远不到，不能产生任何 Claude 工具事件，`stop_reason=end_turn`。
- 转换器层新增 `TestOpenAIStreamingWhitespaceNameAfterBufferedArgumentsSkipped`：先缓存 arguments，后续 name 只有空白，仍不能启动工具块。
- 转换器层新增 `TestOpenAIStreamingEmptyIndexBeforeValidIndexKeepsValidOnly`：并行工具空 index 先到，有效 index 保留，空工具完全不输出。
- SDK 场景层新增 `TestOpenAIClaudeProviderStyleMissingToolNameStreamingChain`：走 public registry 验证 provider-shaped missing-name stream。
- SDK 场景层新增 `TestOpenAIClaudeProviderStyleParallelEmptyFirstIndexStreamingChain`：走 public registry 验证并行工具空 index 先到的场景。
- executor 接口层新增 `TestOpenAICompatExecutorClaudeStreamSkipsMissingToolNameArgumentsEndToEnd`：Claude dirty history -> OpenAI upstream request -> provider-shaped missing-name stream -> Claude response 全链路。
- executor 接口层新增 `TestOpenAICompatExecutorClaudeStreamPreservesValidParallelToolWhenEmptyIndexFirstEndToEnd`：mock OpenAI-compatible HTTP SSE 返回空 index + 有效 index，验证最终 Claude stream 只保留有效工具。

## 不变量生成测试

为把“绝对没问题”落到可验证边界，本轮新增有限状态空间生成测试，而不是只靠固定样例。

输出侧不变量：

- 任意 `tool_use` content block 的 `name` 必须 `TrimSpace(name) != ""`。
- 任意 `input_json_delta` 必须挂在已经打开的 `tool_use` block 上。
- 任意 `text_delta` 必须挂在已经打开的 `text` block 上。
- 任意 `thinking_delta` 必须挂在已经打开的 `thinking` block 上。
- 任意 `content_block_stop` 必须有对应的 `content_block_start`。
- 流结束时不能留下未关闭 content block。
- 若没有实际发出有效 `tool_use`，`message_delta.delta.stop_reason` 不能是 `tool_use`。
- 每条流必须且只能有一个 `message_stop`。

生成覆盖：

- `function.name` 状态：字段缺失、空字符串、空白字符串、首 chunk 有效、后续 chunk 有效。
- `function.arguments` 状态：无参数、首 chunk 参数、参数分片、后续 chunk 参数。
- finish reason：`tool_calls` 与 legacy `function_call`。
- usage：有 usage-only chunk 与无 usage-only chunk。
- 并行工具：无效 `index=0` 与有效 `index=1` 同时/随后出现。
- 非流式形态：`message.tool_calls` 与 content-array `tool_calls`。

请求侧不变量：

- Claude 历史中的空/空白/缺失 `tool_use.name` 不能转成 OpenAI `tool_calls`。
- 被跳过的 `tool_use` 对应 `tool_result` 不能转成 OpenAI `role=tool`。
- 有效 `tool_use` 必须保留对应 `tool_calls` 与 `tool_result`。
- 空/空白/缺失 `tools[].name` 不能进入 OpenAI `tools`。
- 空/空白/缺失 `tool_choice.name` 必须降级为 `auto`。

新增生成测试：

- `TestOpenAIStreamingToolNameStateInvariantsGenerated`
- `TestOpenAIStreamingParallelToolNameStateInvariantsGenerated`
- `TestOpenAINonStreamingToolNameStateInvariantsGenerated`
- `TestConvertClaudeRequestToOpenAI_ToolUseNameInvariantsGenerated`
- `TestConvertClaudeRequestToOpenAI_ToolsAndToolChoiceNameInvariantsGenerated`

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
Go test: 174 passed in 1 packages
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
Go test: 6 passed in 1 packages
```

## 接口级测试

文件：`internal/runtime/executor/openai_compat_executor_compact_test.go`

新增测试：

- `TestOpenAICompatExecutorClaudeStreamSkipsEmptyToolNameEndToEnd`
- `TestOpenAICompatExecutorClaudeStreamSkipsMissingToolNameArgumentsEndToEnd`
- `TestOpenAICompatExecutorClaudeStreamPreservesValidParallelToolWhenEmptyIndexFirstEndToEnd`
- `TestOpenAICompatExecutorClaudeNonStreamSkipsEmptyToolNameEndToEnd`

该组测试通过真实 `OpenAICompatExecutor` 和 `httptest.NewServer` mock OpenAI-compatible `/v1/chat/completions`，覆盖 executor 层的入站、出站和响应转换：

1. 入站 Claude dirty history 包含空/空白 `tool_use.name` 与对应 `tool_result`。
2. executor 转给 mock OpenAI 上游的请求中，不允许出现空 `tool_calls` 或孤儿 `role=tool`。
3. mock 上游返回空 `function.name` 的 OpenAI stream/non-stream 响应。
4. executor 转回 Claude 响应时，不允许出现非法 `tool_use`、孤儿 `input_json_delta` 或孤儿 `content_block_stop`。
5. 空工具名-only 响应必须落到 `stop_reason=end_turn`。

执行结果：

```text
rtk go test ./internal/runtime/executor -run 'TestOpenAICompatExecutorClaude(StreamSkipsEmptyToolNameEndToEnd|StreamSkipsMissingToolNameArgumentsEndToEnd|StreamPreservesValidParallelToolWhenEmptyIndexFirstEndToEnd|NonStreamSkipsEmptyToolNameEndToEnd)' -count=1 -v
Go test: 4 passed in 1 packages
```

## 回归测试

包级回归：

```text
rtk go test ./internal/translator/... -count=1
Go test: 249 passed in 30 packages

rtk go test ./test -count=1
Go test: 276 passed in 1 packages

rtk go test ./internal/runtime/executor -count=1
Go test: 221 passed in 1 packages
```

全仓回归与静态检查：

```text
rtk go test ./... -count=1
Go test: 2183 passed in 147 packages

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

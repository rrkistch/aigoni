# Agent Note: 稳定 Wiki 任务的创建回执

Status: implemented

## Problem

回归中出现 202 创建响应已经进入 running、带有部分答案的情况。创建函数先启动 goroutine，再读取任务快照；快速执行器会抢先推进状态，与既有 pending 回执契约冲突。

## Decision

在任务登记锁内复制 pending 快照，再启动执行器。202 返回该回执，后续状态仍通过轮询和 SSE 获取。

## Alternatives considered

放宽测试接受任意状态会掩盖创建接口的不确定性；延迟启动执行器依赖调度时机。锁内快照明确区分创建回执与实时状态，不改变执行流程。

## Consequences

Admin Chat 和机器 Ask 的创建响应稳定，既有权限、并发和终态接口保持不变。现有测试覆盖初始 pending 状态。

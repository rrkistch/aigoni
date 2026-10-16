# Agent Note: 保留 Firefly 原版，适配 aigoni 数据与私人交互

Status: implemented

## Problem

用户要求保留 Firefly 原版，而不是在 aigoni 的压缩前端上仿制主题。现有 Go 前端托管仅支持 SPA，不能直接服务 Astro 多页面产物。

## Decision

使用独立的 Firefly 源码目录，固定上游提交。只在内容集合、公开配置、路由兼容及私人工作区处添加适配，继续使用原版布局和构建链。Go 的 Firefly 模式显式启用，默认 SPA 行为不变。

Go 管理串行构建、公开快照、发布版本及失败状态。只有公开文章和页面进入快照；笔记与 Wiki 通过原有管理员 API 实时获取。静态发布失败不覆盖上一版本；撤回内容时阻断旧公开页面与索引，直到安全版本上线。

私人工作区复用原来的 Session、CSRF、revision 和 Agent run 接口；不新增公开 AI 服务，也不把 API Key 放到浏览器。笔记附件现在也要求管理员会话。

## Alternatives considered

- 给 aigoni 原前端换皮：不能保留 Firefly 原版，不采用。
- 将 AI 和存储重写进 Astro：重复已有后端能力，不采用。
- 常驻 Node SSR：用户明确选择发布后静态构建，同机 Go 托管更直接。
- 构建失败一律继续旧版：撤回内容时会继续暴露旧页面，因此仅对普通更新保留旧版。

## Consequences

保留上游主题与原 aigoni 回退路径，代价是部署需要 Node/pnpm 构建工具和版本目录空间。首次构建完成前公共站点不可用，管理入口仍保留。

上游预览工具在 AI 环境会自动启动后台进程；开发验证使用前台运行，避免与 Windows 沙箱生命周期冲突。Windows 相对路径校验需接受规范的正斜杠路径，同时拒绝绝对路径与目录穿越。

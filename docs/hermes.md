# 让 Hermes 受控管理博客

## 第一次接入

1. 在网站后台打开“助手与审批”，填写名称。
2. 连接地址推荐使用 `https://你的博客域名/mcp`。同机原生Hermes可用 `http://127.0.0.1:18080/mcp`；若Hermes也在Docker，localhost指它自己，应使用可访问的域名或同网络服务名，如 `http://aigoni:8080/mcp`。HTTP只用于可信内网。
3. 保留默认权限，点击“创建专用凭据”。默认允许公开查询、草稿读写、提交公开和外观候选；私人笔记、Wiki默认不勾选。
4. 立即复制一次性凭据到Hermes部署的私密环境配置，变量名 `AIGONI_MCP_TOKEN`。无法再次查看，丢失时撤销并重建。
5. 复制页面生成的连接配置，合并到Hermes的 `~/.hermes/config.yaml`，不要覆盖已有 `mcp_servers`。Docker部署应编辑你持久化的Hermes配置目录。

```yaml
mcp_servers:
  aigoni:
    url: https://你的博客域名/mcp
    headers:
      Authorization: Bearer ${AIGONI_MCP_TOKEN}
    timeout: 30
    connect_timeout: 15
```

保存后重启Hermes，确认发现 `mcp_aigoni_...` 工具。页面的协议检查不调用模型，仅检查本应用内的凭据与初始化，不能证明Hermes宿主机的DNS、端口或证书可用。地址和代理Host必须与凭据绑定的地址一致；换连接地址请创建新凭据并撤销旧凭据。

## 日常怎么说

- “帮我把今天的想法整理成一篇博客草稿，先别发布。”
- “读一下这篇公开文章，改进介绍部分，提交候选让我确认。”
- “把音乐播放音量改成40%，提交候选。”
- “看看刚才批准的文章是否真正上线。”

草稿直接保存。公开修改、发布、撤回、外观和内容模块变更会生成不可变候选：在后台查看修改前后，批准或拒绝。拒绝可以附说明。提交后目标有变化，审批会失效，需要重新读取再提交。

笔记授权含私人数据；`notes.write`包含读取与编辑但不含删除。Wiki授权只读规范Wiki文档，不能写Wiki、读取任意文件或启动收费AI。授权内容可能进入Hermes模型上下文，按需要勾选。

## 保存与上线是两件事

操作返回 `saved=true` 仅表示文件已保存。公开变更还要查询 `operation_get`，只有 `publication_status=published` 才表示对应内容已在当前公开版本中。`queued`仍在构建，`failed`或`queue_failed`需要后台重试；`blocked`表示撤回后旧版本已停供；`superseded`表示后续修改改变了这一候选的内容。

每个逻辑写请求必须带稳定 `request_id`，格式为1至64位字母、数字、下划线或连字符。同一操作重试保持原标识和参数；不能换标识盲目重试。`needs_review`表示应用阶段中断，先在后台核对目标内容，系统不会自动重放。

## 维护与撤销

凭据只管理授权范围内的工具，不授予管理员密码、服务器Shell或数据卷权限。删除、备份恢复、主题安装、AI连接密钥、凭据管理和批准不作为助手工具开放。

后台可以撤销凭据；撤销后后续调用和未批准候选不能继续执行。主题回退或管理界面故障时，使用“自定义后台路径/maintenance”查看审批并紧急撤销。

MCP源码使用官方Go SDK，原生构建需要Go1.25+；Docker已带所需程序与Node运行环境，不需要额外MCP容器。现有 `AIGONI_API_KEY` 合同不变，不要把旧机器密钥或管理员密码交给Hermes。

协议与配置依据：[官方Go SDK](https://github.com/modelcontextprotocol/go-sdk)、[Hermes MCP配置](https://hermes-agent.nousresearch.com/docs/user-guide/features/mcp)。


验收记录：官方Hermes源码 `81a55437f3f31a9f12a30c53b6c5d49d420898aa` 的 `cmd_mcp_test` 在隔离配置发现8个默认工具，并通过其原生连接器成功执行 `content_list`；未调用模型，也未修改生产Hermes。官方Go SDK客户端另外覆盖写草稿、审批、权限与撤销路径。

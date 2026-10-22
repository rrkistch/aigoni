# v0.3.1：Firefly统一后台与独立主题

## 部署这两个版本

- 后端：`ghcr.io/rrkistch/aigoni:v0.3.1`（Linux amd64）。
- 主题：[theme-v0.3.0及校验文件](https://github.com/rrkistch/aigoni/releases/tag/theme-v0.3.0)。

后端与主题独立版本，不要求编号相同。日常文章、音乐、壁纸、导航和站点信息在后台保存，不需要更新这两个制品。

## 现有服务器升级

1. 先创建并下载数据备份，另行保管凭据。
2. `git pull --ff-only`，不要覆盖已有`compose.env`、`docker.env`或数据卷。
3. 只把`compose.env`的`AIGONI_IMAGE`改成上述固定版本，保留现有18080及资源限制。
4. 拉取镜像并使用原来的Compose文件组合执行`up -d --no-build --pull never`。
5. 进入“自定义后台路径/maintenance”，上传主题0.3.0，验证后启用；等待已上线再使用完整后台。

完整命令见[部署与迁移教程](ghcr-deployment.md)。日常点击操作见[后台指南](admin-guide.md)，Hermes配置见[助手指南](hermes.md)。

## 验收结果

[完整CI](https://github.com/rrkistch/aigoni/actions/runs/39751455727)通过：Linux Go测试、1CPU/2GiB旧卷迁移、独立主题升级/回退、MCP审批、私人附件、备份与恢复、安全副本、容器重建和启动故障维护。前端检查、实际构建、桌面/手机视觉及连贯浏览器操作通过。官方Hermes客户端实际连接并执行只读工具，AI自动测试未调用收费模型。

[稳定镜像发布](https://github.com/rrkistch/aigoni/actions/runs/39753258034)复用已验收源码提交`d729ee769bd1bd9f9ed982676f3e19533ebfa093`的候选镜像，未重新构建。生产服务器没有被修改。

Windows原生agent的符号链接权限和glob路径测试仍有平台缺口；这里交付并验收的是Linux Docker部署。

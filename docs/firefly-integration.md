# 使用 Firefly 前台与私人工作区

Firefly 源码位于项目的 firefly 目录；aigoni 原来的 frontend/web 和 frontend/admin 保留。默认仍为原版 aigoni，不会自动替换已有部署。

## Docker 部署

习惯使用 Docker 时，优先参照 [Docker 部署指南](docker-deployment.md)。无需在服务器单独安装 Go、Node 或 pnpm。

## 首次运行

服务器需要 Go 1.22+（构建 aigoni）、Node.js 22.23+ 和 pnpm。Firefly 依赖以 pnpm-lock.yaml 为准。

1. 在 firefly 目录执行 pnpm install --frozen-lockfile。
2. 在项目根目录按原有方式配置 .env，设置管理员密码及可选的 Wiki 模型参数。
3. 在 .env 设置 FRONTEND_MODE=firefly；FIREFLY_SOURCE_DIR=firefly；FIREFLY_BUILD_DIR=.firefly；FIREFLY_NODE=node。
4. 执行 go build -o Aigoni ./cmd/Aigoni，随后从含 config.yaml 的目录启动 Aigoni。
5. 首次构建期间公开站点返回 503；原管理后台仍可登录。首次构建失败时先查看构建日志，修复依赖后重启，或在原后台编辑保存公开内容重新触发构建。构建成功后，未配置 ADMIN_FRONTEND_DIR 时，管理入口自动使用 Firefly 私人工作区。

同一进程提供公开页面、登录及 API。反向代理应将整个域名转发到 aigoni，而不是直接公开 .firefly 或内容目录。HTTPS 部署设置 COOKIE_SECURE=true；保留 SSE 长连接，不对管理 API 缓存。

## 配置归属

- aigoni 后台：站点名称、描述、作者、头像、Logo、文章、页面、笔记、分类标签与发布状态。
- Firefly 的 src/config：原版布局、字体、壁纸、动效及可选模块。保留上游配置结构；修改主题配置后在工作区执行重新构建。
- 不把私人笔记或 Wiki 放进 Firefly 的 src/content 或 public。
- 示例博客文章不会混入集成模式的文章集合。Firefly 其他模块仍按原版配置运行；正式使用前检查其中的演示介绍、友链、相册和第三方服务配置。
- ADMIN_PATH 沿用现有设置。浏览器 API 固定为 /api/admin/v1；模型密钥和机器 API Key 仅留在 Go 后端。

## 发布状态与故障恢复

保存和上线是两个不同状态。公开内容修改成功后进入串行构建队列；连续更新合并，旧内容快照不会覆盖新版本。外部直接修改 Markdown 会由定时检查发现。

工作区概览展示内容版本、已上线版本和构建状态。失败时内容仍已保存，普通修改保留上一成功版本；修复后点击重试。构建日志位于 FIREFLY_BUILD_DIR/build.log，不通过 HTTP 提供。

撤回或删除公开内容后，旧页面、索引和公开 HTML 暂停提供，直到安全的新版本上线。管理入口保持可用。已被访客下载的内容或第三方缓存无法收回。

FIREFLY_BUILD_DIR 保存状态、公开快照和版本目录。切换版本不覆盖正在服务的目录，旧版带版本号的编译资源继续可访问。目录不是发布根目录，不可直接映射为 Nginx 静态目录。

每次构建运行固定的 scripts/aigoni-build.mjs，再使用 Firefly 原版构建链；不会执行 API 请求传入的 shell 命令。构建进程不继承管理员密码或模型密钥。首轮依赖安装需要网络，上游部分功能的构建脚本可能访问其配置的公共服务。

## 回退

停止服务，将 FRONTEND_MODE 改回 spa 并重新启动。原有嵌入式前台或 WEB_FRONTEND_DIR / ADMIN_FRONTEND_DIR 配置恢复生效。内容文件、Wiki 与上传资源无需迁移；不要删除它们。

## 验证

- go test ./... 与 go build ./...
- firefly 目录：pnpm check、pnpm type-check、pnpm build
- 集成构建：node firefly/scripts/aigoni-build.mjs SNAPSHOT_JSON RELEASE_DIRECTORY
- 视觉验收以 docs/firefly-upstream.json 固定的上游版本和相同主题配置为准。
- 在 firefly 目录运行 pnpm test:aigoni:build 后运行 pnpm test:aigoni。测试使用仓库内公开样例与模拟 API，不需要真实模型密钥，不调用付费模型。可用 PLAYWRIGHT_BROWSER 指定已安装的浏览器可执行文件。
- 可选真实发布测试：设置 AIGONI_FIREFLY_REAL_BUILD=1，执行 go test ./internal/server -run TestFireflyRealCompiler -count=1。它验证真实 Astro 构建和管理员 API 修改后的版本切换。

## 上游来源

Firefly： https://github.com/CuteLeaf/Firefly
固定提交：91cbbefe53716e70b77ca9c41e27af10f5b27cab
保留 firefly/LICENSE、README 中的原作者与图片来源说明。后续升级先在独立版本完成视觉和接口回归，再更新固定提交记录。

## 本次验证记录（2026-10-16）

- Firefly 原版提交的静态基线已构建，并与集成首页、文章页及私人工作区进行桌面/移动、明暗模式截图检查。截图保存在项目本地 .firefly/evidence。
- 完整 Firefly 构建、Astro 检查与 TypeScript 独立声明检查通过；私人工作区排除在 sitemap 与 Pagefind 索引之外。
- 浏览器回归通过：公开搜索、登录、CSRF、内容创建、编辑冲突保留、Wiki 浏览、模拟 AI 流式响应、明暗窄屏与退出。
- Go 服务和配置包回归通过；真实编译器测试验证了“管理员 API 修改文章 → Astro 构建 → 切换公开版本”。
- Windows 本地可执行文件以 go build -buildvcs=false 构建，位于 .tools/Aigoni.exe。关闭的仅是 VCS 元数据嵌入；沙箱无法执行系统 Git 来采集该元数据。临时 Go 编译器位于 .tools/go/bin/go.exe，没有安装到系统。
- Windows 旧版沙箱中两项 Agent 测试曾受符号链接/读取权限限制；新增 Docker 构建已在 Linux、Go 1.26 下运行 go test ./...，全部通过，未跳过这些测试。
- 未部署生产环境，未调用真实付费模型。Wiki/AI 的真实服务连接仍使用原有 WIKI_APIKEY、WIKI_BASEURL、WIKI_MODEL 配置。

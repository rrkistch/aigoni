# 从 GitHub 镜像部署（推荐小内存服务器）

GitHub Actions 分别发布后端镜像和带锁定依赖的主题包。后端通过容器安装与发布检查后推送 GHCR；主题通过搬迁构建后发布到 Releases。服务器不编译 Go 或安装前端依赖，仅在发布时生成静态页面。当前镜像支持 Linux amd64（`uname -m` 显示 x86_64）；ARM 服务器暂不使用此镜像。

## 首次发布与公开权限

推送 main 或在 Actions 的 Container image 中手动运行，会生成：

- `ghcr.io/rrkistch/aigoni:latest`：最近正式发布的稳定镜像；普通 main 提交不更新它。
- `ghcr.io/rrkistch/aigoni:sha-完整提交号`：通过 CI 的 main 候选版本，仍需查看该提交的功能完成状态。
- `ghcr.io/rrkistch/aigoni:v主版本.次版本.修订号`：推送 `container-v主版本.次版本.修订号` 标签后的正式版本，同时更新 latest。完整回退可固定使用镜像 digest。

查看仓库 Actions 的 Container image，只有整个任务绿色成功才算发布完成。首次创建的 GHCR 包默认可能是私有：在 GitHub 用户主页 Packages → aigoni → Package settings → Change visibility 中选择 Public。公开仓库不代表镜像自动公开。公开后服务器无需登录；密码、文章、笔记不会进入镜像。

工作流使用临时 GITHUB_TOKEN，不需要把个人令牌写入仓库。若发布报 packages 权限错误，在仓库设置中检查 Actions 是否获准运行及组织包策略。手动运行使用 main；正式发布先等待该提交的 main 候选镜像通过，再推送 container-v 标签。main候选执行完整迁移、主题升级回退、MCP审批和恢复门禁；标签流程核对源码标识并复用该候选镜像，再更新正式版本及latest，不重复执行同一份已通过的证据；不复用已有版本号。镜像不会自动更新正在运行的服务器。

## 服务器启动

需要 Docker 和 Compose 2.30+。不要重装 Docker 或停止其他项目。尚未克隆时：

```sh
mkdir -p ~/apps
cd ~/apps
git clone https://github.com/rrkistch/aigoni.git
cd aigoni
```

已经克隆时在项目目录执行 `git pull --ff-only`。

**已有 compose.env / docker.env 时不要覆盖。** 仅将 compose.env 中的 AIGONI_IMAGE 改为 `ghcr.io/rrkistch/aigoni:latest`，保留原端口、密码等配置。全新安装才执行：

```sh
cp compose.registry.env.example compose.env
cp docker.env.example docker.env
chmod 600 docker.env
```

编辑 docker.env，设置 ADMIN_PASSWORD；HTTP 隧道试用保留 COOKIE_SECURE=false，正式 HTTPS 改为 true。其他配置与 [Docker 部署指南](docker-deployment.md) 相同。

先确认架构与配置，再拉取和启动（不要加 --build）：

```sh
uname -m
docker compose --env-file compose.env config --quiet
docker compose --env-file compose.env pull aigoni
docker compose --env-file compose.env up -d --no-build --pull never
```

如果出现 denied / unauthorized，先确认镜像已经公开且工作流发布成功，不要通过重新本地构建绕过。连接 ghcr.io 超时则需排查服务器网络。

## 4 GB 共享服务器：给应用设置上限

建议首次启动就使用以下命令代替上面的 up，限制整个应用最多使用 1 个 CPU 和 2 GiB 内存：

```sh
docker compose --env-file compose.env \
  -f compose.yaml -f deploy/docker/compose.limited.yaml \
  up -d --no-build --pull never
```

之后每次 up 仍带同样的两个文件；只用原 compose.yaml 执行 up 会移除上限。可以在 compose.env 中设置 AIGONI_CPUS 和 AIGONI_MEMORY 覆盖默认值。该限制保护宿主机，但不是内存预留；宿主机仍需有实际可用内存。内存不足时应用可能被终止，需检查日志并按实际余量调整，不能靠无限重启解决。

首次启动需先通过维护入口安装主题，随后每次公开变更仍需要在服务器构建 Firefly 静态页面，受 CPU 限制后会更慢。镜像方式移走的是程序编译、测试和依赖安装，并未消除运行时静态构建。首次构建期间首页可能 503；healthy 只表示 Go API 正常。

如果代理也在 Docker 网络，额外保留原有 `-f deploy/docker/compose.proxy.yaml` 和 AIGONI_PROXY_NETWORK，不要丢掉代理网络配置。

```sh
docker compose --env-file compose.env ps
docker compose --env-file compose.env logs --tail=100 aigoni
```

默认只监听服务器 127.0.0.1:8080。在自己电脑运行 `ssh -N -L 18080:127.0.0.1:8080 用户名@服务器IP`，保持连接，然后访问 http://localhost:18080/admin。正式域名、HTTPS、数据备份见 [部署指南](docker-deployment.md)。

## 从一体化版本迁移

1. 登录现有后台“网站维护”创建并下载数据备份；旧版无此功能时先按部署指南备份数据卷。
2. 保留旧镜像、现有数据卷、密码及 `compose.env` 的原端口（你现有18080不要改回示例8080）。
3. 按下节更新到已通过CI、包含独立主题接口的镜像版本；不要覆盖 docker.env / compose.env，也不要执行 down -v。
4. 访问“自定义后台路径/maintenance”，登录并上传 [正式可搬迁主题包](https://github.com/rrkistch/aigoni/releases)。使用 theme-v0.1.2 或以后素材完整版本。
5. 验证通过后点击启用，等待主题和站点均已上线，再检查文章与私人工作区。

详见 [安装与回退](independent-theme.md)。当前开发提交仍分阶段交付，普通 main 的 sha 镜像不是全部计划完成的稳定版本。

## 升级与回退

先按部署指南备份数据，再保留旧镜像标签：

```sh
docker image tag \
  "$(docker compose --env-file compose.env images -q aigoni)" \
  aigoni-firefly:before-upgrade
git pull --ff-only
docker compose --env-file compose.env pull aigoni
docker compose --env-file compose.env \
  -f compose.yaml -f deploy/docker/compose.limited.yaml \
  up -d --no-build --pull never
```

上述升级假设 compose.env 的镜像为 latest。固定 sha 标签时，先把 AIGONI_IMAGE 改为 Actions 成功发布的新 sha 标签。拉取失败时不要继续切换；旧容器仍可运行。

回退：把 compose.env 的 AIGONI_IMAGE 改为 `aigoni-firefly:before-upgrade`，再执行上面带相同附加文件的 `up -d --no-build --pull never`，不要 pull。回退程序不撤销文章修改。不要执行 down -v，会删除数据。

## 保留本地构建入口

需要修改源码后本地构建时继续使用 compose.env.example 和原来的 `up -d --build` 流程；资源上限配置只限制运行容器，不限制 Docker 镜像构建。不要在内存紧张的服务器上使用该入口。

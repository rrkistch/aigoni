# 用 Docker 部署 aigoni + Firefly

这套部署只需要一个应用容器，不需要数据库。aigoni 提供网站、管理后台和 API；容器内的 Node/pnpm 在发布时构建 Firefly。

## 1. 第一次启动

服务器需安装 Docker Engine 和 Docker Compose 2.30+，使用 Linux 容器。镜像构建、首次发布及 AI 服务需要能访问相关网络服务。建议给构建留出至少 2 GB 可用内存，文章多时适当增加。

在项目根目录执行：

```sh
cp compose.env.example compose.env
cp docker.env.example docker.env
chmod 600 docker.env
openssl rand -hex 24
```

把生成的随机值填入 docker.env 的 ADMIN_PASSWORD，不要保留空值。该文件按原样读取：值不要加引号，也不要在值末尾写注释，密码中的 $ 和 # 不会被 Compose 替换。

- 先用 HTTP 本地试用：COOKIE_SECURE=false。
- 通过正式 HTTPS 域名访问：COOKIE_SECURE=true。
- 使用 AI 时再填 WIKI_APIKEY、WIKI_BASEURL 和 WIKI_MODEL；不配置不影响博客和笔记管理。

启动：

```sh
docker compose --env-file compose.env up -d --build
docker compose --env-file compose.env ps
docker compose --env-file compose.env logs -f --tail=100 aigoni
```

默认只在服务器本机监听 127.0.0.1:8080，适合接到现有反向代理。它不会直接向公网暴露 8080 端口。

第一次启动会自动在数据卷生成配置和目录，并进行首次构建。公开首页在构建完成前返回 503；/admin 或 /login 仍可打开原后台。构建完成后切换到 Firefly 工作区。

**首次登录后**，在设置中填写自己的站点名称、作者和正式站点地址，例如 https://blog.example.com。保存后会重新构建，避免 RSS、canonical 和 sitemap 继续使用 localhost。

## 2. 接入域名和 HTTPS

域名的 DNS 指向服务器，再由现有 Nginx/网关将该域名反向代理到应用。

### Nginx 装在宿主机

将 deploy/docker/nginx-location.conf.example 中的 location 块放入已有的 HTTPS server 块，目标为 http://127.0.0.1:8080。证书由你现有的网关或证书工具管理。

示例已关闭代理缓冲，给 AI SSE 流式请求保留 660 秒超时，并允许 20 MiB 文件及表单开销。不要缓存管理 API，也不要把数据卷直接配置成静态目录。

### Nginx 或网关也在 Docker 中

容器内的 127.0.0.1 指向网关自己，不是 aigoni。把两个服务放到现有的同一个 Docker 网络：

```sh
AIGONI_PROXY_NETWORK=你的现有代理网络 \
docker compose --env-file compose.env \
  -f compose.yaml -f deploy/docker/compose.proxy.yaml up -d --build
```

网关的目标改为 http://aigoni:8080。外部网络必须已存在；之后的升级、停止等命令也使用相同的两个 Compose 文件。

如果还没配域名，可通过 SSH 隧道临时试用，避免公开 HTTP 后台：

```sh
ssh -L 8080:127.0.0.1:8080 用户名@服务器
```

随后在自己电脑访问 http://localhost:8080/admin。

## 3. 哪些东西会保留

默认使用 Compose 命名卷 data，挂载到 /data。应用以非 root 的 node 用户运行，空卷首次创建时由镜像提供目录权限。

| 位置 | 内容 |
|---|---|
| /data/content | 文章、页面、私人笔记和对应附件 |
| /data/wiki | AI 维护的知识库 |
| /data/public | 上传图片及站点资源 |
| /data/config.yaml | 后台保存的站点设置 |
| /data/.firefly | 已发布版本、公开快照、构建状态与日志 |

源代码和依赖放在镜像的 /opt/firefly，不要把空目录挂载到这里。升级镜像会更新源码，数据卷不会覆盖新源码。

docker.env 留在宿主机，不进入镜像。它和 compose.env 已加入 Git 忽略规则；构建上下文也排除了原始内容、Wiki、秘密配置和本机依赖缓存。不要执行 docker compose down -v，这会删除数据卷。

如果改为宿主机目录挂载，要先把该目录设置为容器 UID/GID 1000:1000 可写。不要用 chmod 777 解决权限问题。

## 4. 日常写作、状态与故障处理

写文章和发布不需要重建 Docker 镜像。保存后等待工作区概览显示“已上线”。

```sh
# 应用日志
docker compose --env-file compose.env logs --tail=100 aigoni

# Firefly 构建日志
docker compose --env-file compose.env exec aigoni tail -n 100 /data/.firefly/build.log

# 应用 API 健康检查
docker compose --env-file compose.env exec aigoni node /usr/local/lib/aigoni/healthcheck.cjs
```

healthy 表示 Go API 可用，不代表首轮静态构建已经完成。网站是否上线看工作区构建状态及首页响应；不要因为首次首页 503 就反复重启。

普通构建失败会保留上一成功版本。修复后在工作区重试；首次构建还没有新版工作区时，修复依赖后重启，或在原后台编辑保存公开内容重新触发。撤回内容后的旧页面和索引会暂停提供，直到安全版本上线。

## 5. 备份与恢复

先等待 AI 任务完成或在后台停止任务，再停止应用，保证备份时没有内容或 Wiki 写入。以下是 Linux 服务器命令，运行目录为项目根目录：

```sh
mkdir -p backups
chmod 700 backups
docker compose --env-file compose.env stop aigoni
umask 077
docker compose --env-file compose.env run --rm --no-deps -T \
  --entrypoint tar aigoni -C /data -czf - . \
  > "backups/data-$(date +%Y%m%d-%H%M%S).tar.gz"
docker compose --env-file compose.env start aigoni
```

另行安全备份 docker.env、compose.env，以及自定义主题源码；不要把含密钥的文件公开上传。上面的归档包含发布版本，空间紧张时可在确认恢复流程后另做仅原始数据备份。

恢复时停止服务，将可信备份复制到容器可见的临时位置，再以应用用户解包。首次迁移到新机器可直接使用：

```sh
docker compose --env-file compose.env stop aigoni
docker compose --env-file compose.env run --rm --no-deps -T \
  --entrypoint tar aigoni -C /data -xzf - < backups/你选择的备份.tar.gz
docker compose --env-file compose.env up -d --no-build
```

**恢复应使用空的数据卷**；上面的解包不会删除卷中备份未包含的旧文件。已有数据时先备份，并在单独的 Compose 项目/数据卷中验证恢复，确认无误后再切换。不要直接覆盖正在使用的站点。

## 6. 升级与回退

升级前先备份数据，并给正在使用的镜像保留一个标签：

```sh
docker image tag \
  "$(docker compose --env-file compose.env images -q aigoni)" \
  aigoni-firefly:before-upgrade

docker compose --env-file compose.env build --pull
docker compose --env-file compose.env up -d --no-build
```

Docker 模式默认 FIREFLY_REBUILD_ON_START=true：每次启动都会用当前镜像源码重新构建，解决“镜像换了但内容未变，仍显示旧主题”的问题。普通重建期间仍提供上一成功版本。

回退程序：

```sh
AIGONI_IMAGE=aigoni-firefly:before-upgrade \
docker compose --env-file compose.env up -d --no-build --pull never
```

若要长期保持旧镜像，把 compose.env 中 AIGONI_IMAGE 也改成该标签。程序回退不会撤销内容修改；跨版本出现数据格式变化时，需要配套备份恢复。

只想切回原 aigoni 界面：把 docker.env 中 FRONTEND_MODE 改为 spa，然后执行 up -d --force-recreate --no-build。文章和 Wiki 不需要迁移。

## 配置文件分工

- compose.env：宿主机端口、绑定地址、镜像标签和应用环境文件位置。
- docker.env：管理员密码、后台路径、HTTPS Cookie、AI 配置和运行模式。
- Dockerfile：多阶段构建 Go 程序和前端依赖，运行时仅保留所需工具。
- compose.yaml：单应用容器、命名数据卷、自动重启、日志限制和停止宽限期。
- 主题个性化仍修改 firefly/src/config，然后重新构建镜像；正文只在后台编辑。

Compose 的 raw 环境文件格式要求 2.30+，见 [Docker 官方说明](https://docs.docker.com/reference/compose-file/services/#env_file)。

## 本地验收脚本

运行 node --test deploy/docker/healthcheck.test.cjs 验证健康检查。构建 aigoni-firefly:docker-smoke 镜像后，运行 node deploy/docker/smoke.cjs 可验证首次构建、发布文章、私人附件及重建持久化。脚本使用随机项目名和临时密码，结束后只清理自己创建的容器、网络和测试卷，不调用真实模型。

## 已完成的容器验收

2026-10-16，linux/amd64 实测通过：镜像构建、Linux Go 全量测试、非 root 启动、空数据卷首次构建、真实文章发布、私人附件鉴权、包含 $/# 的密码、容器重建后的文章/笔记保留，以及启动重新构建新发布版本。基础和可选代理 Compose 配置均通过校验，健康检查的四项单测通过。

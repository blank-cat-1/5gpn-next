# 原功能分格面板更新

版本：`v0.13.27-custom.20261016.1`；Linux x86_64。
支持从原版 `v0.13.27`、第一版定制 `v0.13.27-custom.20261015.1` 或已部署的 `v0.13.27-custom.20261015.2` 更新。

本次只调整规则页显示：自定义规则置顶，自定义与内置规则分别折叠，点击标题展开。添加、删除后条数自动更新，实际规则匹配顺序不变。

继续保留原项目的管理功能与路由语义。
网页和 Telegram 都使用原有内网访问限制，不要求网页验证 Telegram 身份。

## 更新

从 [本仓库 Releases](https://github.com/blank-cat-1/5gpn-next/releases/tag/v0.13.27-custom.20261016.1) 下载更新包，上传到服务器 `/root/`，以 root 执行：

```sh
cd /root
tar -xzf 5gpn-next-v0.13.27-custom.20261016.1-linux-amd64.tar.gz
cd 5gpn-next-v0.13.27-custom.20261016.1-linux-amd64
bash update.sh
5gpnd version
```

本次命令没有开启鉴权的附加参数。脚本不会写入配置文件，也不会用示例配置覆盖已有配置。
已有的节点、订阅、规则、默认出口、Bot 凭据、DNS、面板开关和内网网段均沿用。
如第一版定制配置保留了 `routing_mode` 或 `panel.telegram_auth`，新程序会忽略这些已撤销的功能字段。

不要重跑安装器。脚本检查当前版本、包校验和、原配置和服务路径，备份原程序及
`/etc/5gpn-next`，停止服务后还会备份已有的 `/var/lib/5gpn-next`、`/var/lib/mihomo-5gpn`。
备份目录是仅 root 可访问的 `/root/5gpn-upgrade-backup-XXXXXXXX`。
服务会短暂停止，备份较大时等待时间增加。脚本不替换证书、mihomo 配置、服务单元或防火墙。

启动后检查服务状态和运行程序 SHA256；检查失败会尝试恢复原程序并重启。
成功不等于完成真实网络验证，仍需检查 Bot、管理网页和手机联网。

更新后网页仍使用原地址。在 Bot 私聊发 `/start`，点击「管理面板」，选择小程序或网页入口。
`/panel` 也能打开入口。面板仍取决于原有 `panel.enabled` 设置，更新不擅自开启已关闭的面板。

## 回退

将 `BACKUP` 改成此次更新打印的真实备份目录。以下命令恢复更新前的程序，配置一直未改动。

```sh
BACKUP=/root/5gpn-upgrade-backup-实际目录后缀
bash -s -- "$BACKUP" <<'SH'
set -e
BACKUP=$1
test -f "$BACKUP/5gpnd"
cp -p "$BACKUP/5gpnd" /usr/local/bin/5gpnd.rollback
trap 'systemctl start 5gpn-next' EXIT
systemctl stop 5gpn-next
mv -f /usr/local/bin/5gpnd.rollback /usr/local/bin/5gpnd
systemctl start 5gpn-next
trap - EXIT
5gpnd version
systemctl is-active 5gpn-next
SH
```

## 构建

Go 1.23.12。在源码目录执行：

```sh
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags '-s -w -X main.version=v0.13.27-custom.20261016.1 -X github.com/w0ven/5gpn-next/internal/update.repo=' -o 5gpnd-linux-amd64 ./cmd/5gpnd
```

手动构建将发布源置空，防止内置更新器下载上游原版覆盖定制面板，不修改用户的更新配置。
以后继续使用本仓库的定制更新包。安装器及源码中的默认更新源已指向 `blank-cat-1/5gpn-next`；这不会改变已安装手动构建的行为。

包内 `update.sh` 来自 `scripts/upgrade-preserve-config.sh`；`check-config.py` 来自
`scripts/check-upgrade-config.py`；`VERSION` 存放版本号；`SHA256SUMS` 校验其余文件。
本更新包已通过编译、脚本语法与配置保留检查；本次规则分组的展开收起、添加删除与手机布局已通过本地浏览器检查。尚未在你的服务器执行本次更新。

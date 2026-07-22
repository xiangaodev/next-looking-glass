# Next Looking Glass 部署

在旧 PHP 版 VPS 上并行部署新 Go 版，Apache 反代到 Go 后端 `:8080`。
可随时回滚（注释掉 ProxyPass 即恢复旧 PHP 版）。

## 一次性准备（本机）

```bash
# 1. 编译 Linux 二进制
cd ..
make release          # 产出 bin/next-looking-glass-linux-amd64

# 2. 生成各节点配置文件
cd deploy
bash gen-configs.sh   # 复制 config.yaml 到 configs/<fqdn>.yaml

# 3. 编辑每个节点的 IP
vi configs/lg-tw-g.nimbus.com.tw.yaml      # 改 ipv4 / logo_text 等
vi configs/lg-tw-cnd.nimbus.com.tw.yaml
# ... 为每个节点做同样的事
```

## 部署到所有节点

```bash
cd deploy
ansible-playbook -i production/hosts site.yml
```

## 部署单个节点

```bash
ansible-playbook -i production/hosts site.yml --limit lg-tw-g.nimbus.com.tw
```

## playbook 做的事

1. 装系统依赖（`iputils-ping` / `dnsutils` / `curl`）
2. 装 **nexttrace** 到 `/usr/local/bin/nexttrace`
3. 上传 Go 二进制到 `/usr/local/bin/next-looking-glass`
4. 复制对应节点的 `config.yaml` 到 `/etc/next-looking-glass/config.yaml`
5. 创建 systemd 服务（`next-looking-glass.service`）
6. 启用 Apache `mod_proxy` + `mod_proxy_http`
7. 配置 Apache `ProxyPass / http://127.0.0.1:8080/`（反代）
8. 重启 Apache 并验证后端响应

## 验证

```bash
curl http://127.0.0.1:8080/api/info          # 直接访问 Go
curl http://lg-tw-g.nimbus.com.tw/api/info   # 通过 Apache 反代
```

## 回滚

注释掉 Apache proxy 配置中的 ProxyPass 行，`systemctl reload apache2` 即恢复旧 PHP 版。

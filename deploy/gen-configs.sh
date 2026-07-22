#!/bin/bash
# 生成各节点 config.yaml。从旧 inventory 读 IP。
# 运行前先在 config.yaml 里确认通用配置正确。

BASE=$(dirname "$0")/..
DEFAULT="$BASE/config.yaml"

nodes=(
  "lg-tw-g.nimbus.com.tw:TW - Global"
  "lg-tw-cnd.nimbus.com.tw:TW - China Direct"
  "lg-tw-g-antiddos.nimbus.com.tw:TW - Global AntiDDoS"
  "lg-hk-g.nimbus.com.tw:HK - Global"
  "lg-hk-cnd.nimbus.com.tw:HK - China Direct"
  "lg-hk-cndp.nimbus.com.tw:HK - China Direct Premium"
  "lg-hk-g-antiddos.nimbus.com.tw:HK - Global AntiDDoS"
)

for node in "${nodes[@]}"; do
  name="${node##*:}"
  fqdn="${node%%:*}"
  target="$BASE/deploy/configs/${fqdn}.yaml"
  cp "$DEFAULT" "$target"
  # 替换节点位置
  sed -i '' "s/server_location:.*/server_location: \"$name\"/" "$target"
  echo "  $name -> configs/${fqdn}.yaml"
done
echo "Done — 请手动编辑各文件中的 ipv4 / logo 等字段"

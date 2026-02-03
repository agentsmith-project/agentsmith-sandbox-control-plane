#!/usr/bin/env bash
set -euo pipefail

NS="${SANDBOX_NAMESPACE:-sandbox}"
SEL="${LABEL_SELECTOR:-app=llm-sandbox}"
SKEW="${NOW_SKEW_SECONDS:-5}"

now_epoch="$(date -u +%s)"

pods_json="$(kubectl -n "${NS}" get pod -l "${SEL}" -o json)"
count="$(echo "${pods_json}" | jq '.items | length')"

if [ "${count}" -eq 0 ]; then
  exit 0
fi

echo "${pods_json}" | jq -c '.items[] | {name: .metadata.name, expiresAt: (.metadata.annotations["sandbox/expiresAt"] // ""), lastActiveAt: (.metadata.annotations["sandbox/lastActiveAt"] // ""), ttl: (.metadata.annotations["sandbox/ttlSeconds"] // "900")}' | while read -r item; do
    name="$(echo "${item}" | jq -r '.name')"
    expiresAt="$(echo "${item}" | jq -r '.expiresAt')"
    lastActiveAt="$(echo "${item}" | jq -r '.lastActiveAt')"
    ttl="$(echo "${item}" | jq -r '.ttl')"

    expire_epoch=""
    if [ -n "${expiresAt}" ] && [ "${expiresAt}" != "null" ]; then
      expire_epoch="$(date -u -d "${expiresAt}" +%s 2>/dev/null || echo "")"
    fi

    if [ -z "${expire_epoch}" ]; then
      if [ -n "${lastActiveAt}" ] && [ "${lastActiveAt}" != "null" ]; then
        last_epoch="$(date -u -d "${lastActiveAt}" +%s 2>/dev/null || echo "")"
        if [ -n "${last_epoch}" ]; then
          expire_epoch="$(( last_epoch + ttl ))"
        fi
      fi
    fi

    if [ -z "${expire_epoch}" ]; then
      # 无法解析时间：按保守策略删除，避免残留
      echo "delete ${name} (no valid timestamps)"
      kubectl -n "${NS}" delete pod "${name}" --grace-period=0 --force || true
      continue
    fi

    if [ $(( now_epoch + SKEW )) -ge "${expire_epoch}" ]; then
      echo "delete ${name} (expired)"
      kubectl -n "${NS}" delete pod "${name}" --grace-period=0 --force || true
    fi
done

exit 0

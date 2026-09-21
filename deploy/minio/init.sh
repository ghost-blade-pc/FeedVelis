#!/bin/sh
set -eu

case "${MINIO_WEB_ORIGIN}" in
  http://localhost:*|http://127.0.0.1:*|https://*) ;;
  *)
    echo "MINIO_WEB_ORIGIN 必须是精确的本地 HTTP 或 HTTPS Origin" >&2
    exit 1
    ;;
esac
case "${MINIO_WEB_ORIGIN}" in
  *'*'*|*'?'*|*'#'*|*'&'*|*'<'*|*'>'*)
    echo "MINIO_WEB_ORIGIN 不得含通配符、查询、片段或 XML 特殊字符" >&2
    exit 1
    ;;
esac

mc alias set velis http://minio:9000 "${MINIO_ROOT_USER}" "${MINIO_ROOT_PASSWORD}"
mc mb --ignore-existing "velis/${MINIO_ASSET_BUCKET}"
mc anonymous set none "velis/${MINIO_ASSET_BUCKET}"

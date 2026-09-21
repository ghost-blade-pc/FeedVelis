# Web 端到端测试

`i2-content-supply.sh` 是 I2 的可复现 HTTP 演示脚本，覆盖用户直接发布与匿名读取、编辑、双标签版本冲突、作者/管理员下架与恢复，以及 Source 新增、暂停/恢复、手动抓取、RSS 自动发布和联合 latest。

它会创建测试用户、文章和 Source，只允许默认本机或显式 `.test` 地址，不能用于生产。API 必须开启认证与注册，并预先创建管理员；Feed URL 必须是 API 网络策略允许访问的公开 HTTP(S) 地址。

```bash
I2_E2E_ADMIN_USERNAME=root \
I2_E2E_ADMIN_PASSWORD='仅用于本地测试的密码' \
I2_E2E_FEED_URL='https://example.com/feed.xml' \
./web/e2e/i2-content-supply.sh
```

浏览器自动化仍待后续按交互风险补充；当前脚本验证真实 HTTP、数据库与抓取闭环，不替代浏览器兼容性测试。
